package rules

import (
	"bytes"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"text/template"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"log/slog"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
	celenv "github.com/lukashankeln/glint/internal/rules/cel"
)

// CompiledRule pairs a RuleDef with its compiled CEL program and pre-parsed
// message template so we don't re-parse it on every violation.
type CompiledRule struct {
	Def     RuleDef
	program cel.Program
	msgTmpl *template.Template // nil when Def.Message is empty or unparseable
}

// Engine evaluates policy rules against manifests using CEL.
type Engine struct {
	env        *cel.Env
	rules      []CompiledRule
	exceptions map[string][]ExceptionEntry // keyed by rule ID
}

// NewEngine loads and compiles all enabled rules. Returns an error immediately
// if any CEL expression fails to compile.
func NewEngine(cfg config.RulesConfig) (*Engine, error) {
	env, err := celenv.NewEnv()
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment: %w", err)
	}

	var defs []RuleDef

	// Built-in rules
	builtIns, err := LoadBuiltIns(cfg)
	if err != nil {
		return nil, fmt.Errorf("loading built-in rules: %w", err)
	}
	defs = append(defs, builtIns...)

	// Inline custom rules from config
	inline, err := LoadCustomInline(cfg)
	if err != nil {
		return nil, fmt.Errorf("loading inline custom rules: %w", err)
	}
	defs = append(defs, inline...)

	// External rule files
	if len(cfg.RuleFiles) > 0 {
		external, err := LoadRuleFiles(cfg.RuleFiles)
		if err != nil {
			return nil, fmt.Errorf("loading rule files: %w", err)
		}
		defs = append(defs, external...)
	}

	compiled := make([]CompiledRule, 0, len(defs))
	for _, def := range defs {
		if def.Expression == "" {
			continue
		}
		ast, issues := env.Compile(def.Expression)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("rule %q: CEL compile error: %w", def.ID, issues.Err())
		}
		prog, err := env.Program(ast)
		if err != nil {
			return nil, fmt.Errorf("rule %q: CEL program error: %w", def.ID, err)
		}
		var msgTmpl *template.Template
		if def.Message != "" {
			if t, err := template.New("msg").Parse(def.Message); err == nil {
				msgTmpl = t
			}
		}
		compiled = append(compiled, CompiledRule{Def: def, program: prog, msgTmpl: msgTmpl})
	}

	for _, exc := range cfg.Exceptions {
		found := slices.ContainsFunc(compiled, func(cr CompiledRule) bool {
			return cr.Def.ID == exc.Rule
		})
		if !found {
			slog.Warn("exception references unknown rule ID, skipping", "rule", exc.Rule)
		}
	}

	return &Engine{env: env, rules: compiled, exceptions: BuildExceptionIndex(cfg.Exceptions)}, nil
}

// NewCELEnv creates a fresh CEL environment with all glint declarations.
// Exposed for external tools (e.g., glint rules validate).
func NewCELEnv() (*cel.Env, error) {
	return celenv.NewEnv()
}

// Rules returns all loaded rules (enabled and disabled) for listing purposes.
func (e *Engine) Rules() []RuleDef {
	out := make([]RuleDef, len(e.rules))
	for i, r := range e.rules {
		out[i] = r.Def
	}
	return out
}

// Evaluate runs all enabled rules against the given manifests concurrently.
func (e *Engine) Evaluate(manifests []manifest.Manifest) []Violation {
	if len(manifests) == 0 || len(e.rules) == 0 {
		return nil
	}

	type result struct {
		idx        int
		violations []Violation
	}

	workers := runtime.NumCPU()
	if workers > len(manifests) {
		workers = len(manifests)
	}

	jobs := make(chan int, len(manifests))
	results := make(chan result, len(manifests))

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				m := manifests[idx]
				vs := e.evaluateManifest(m)
				results <- result{idx: idx, violations: vs}
			}
		}()
	}

	for i := range manifests {
		jobs <- i
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect in manifest order for deterministic output.
	ordered := make([][]Violation, len(manifests))
	for r := range results {
		ordered[r.idx] = r.violations
	}

	var all []Violation
	for _, vs := range ordered {
		all = append(all, vs...)
	}
	return all
}

func (e *Engine) evaluateManifest(m manifest.Manifest) []Violation {
	vars := celenv.ManifestToVars(m)
	var violations []Violation

	for _, rule := range e.rules {
		if !rule.Def.Enabled {
			continue
		}
		if !rule.Def.Match.Matches(m) {
			continue
		}
		if entries, ok := e.exceptions[rule.Def.ID]; ok {
			if matchesException(entries, m) {
				continue
			}
		}

		out, _, err := rule.program.Eval(vars)
		if err != nil {
			slog.Warn("CEL evaluation error, skipping", "rule", rule.Def.ID, "resource", m.Kind+"/"+m.Name, "err", err)
			continue
		}

		if out == types.True {
			continue // rule passes, no violation
		}

		msg := renderMessage(rule.msgTmpl, rule.Def.Message, m)
		violations = append(violations, Violation{
			RuleID:       rule.Def.ID,
			Severity:     rule.Def.Severity,
			Message:      msg,
			Source:       "cel",
			APIVersion:   m.APIVersion,
			ResourceKind: m.Kind,
			ResourceName: m.Name,
			ResourceNS:   m.Namespace,
			FilePath:     m.SourcePath,
			Rendered:     m.Rendered,
		})
	}
	return violations
}

// renderMessage executes the pre-compiled message template with manifest fields.
// Falls back to the raw message string on execution errors.
func renderMessage(tmpl *template.Template, fallback string, m manifest.Manifest) string {
	if tmpl == nil {
		return fallback
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{
		"kind":       m.Kind,
		"name":       m.Name,
		"namespace":  m.Namespace,
		"apiVersion": m.APIVersion,
	}); err != nil {
		return fallback
	}
	return buf.String()
}
