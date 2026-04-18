package rules

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/rules/builtins"
)

//go:embed builtin
var embeddedRules embed.FS

// ruleFileDoc is the top-level structure for external rule YAML files.
type ruleFileDoc struct {
	Rules []ruleYAML `yaml:"rules"`
}

// ruleYAML maps the YAML schema for a rule definition.
type ruleYAML struct {
	ID          string          `yaml:"id"`
	Description string          `yaml:"description"`
	Severity    string          `yaml:"severity"`
	Match       matchFilterYAML `yaml:"match"`
	Expression  string          `yaml:"expression"`
	Message     string          `yaml:"message"`
}

type matchFilterYAML struct {
	Kinds             []string          `yaml:"kinds"`
	APIGroups         []string          `yaml:"api_groups"`
	Namespaces        []string          `yaml:"namespaces"`
	ExcludeNamespaces []string          `yaml:"exclude_namespaces"`
	Labels            map[string]string `yaml:"labels"`
}

// LoadBuiltIns reads the embedded rule YAML files and applies config overrides.
// Disabled rules are included with Enabled=false so they appear in `glint rules list`.
func LoadBuiltIns(cfg config.RulesConfig) ([]RuleDef, error) {
	var rules []RuleDef

	err := fs.WalkDir(embeddedRules, "builtin", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}

		data, err := embeddedRules.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading embedded rule %q: %w", path, err)
		}

		var ry ruleYAML
		if err := yaml.Unmarshal(data, &ry); err != nil {
			return fmt.Errorf("parsing embedded rule %q: %w", path, err)
		}
		if ry.ID == "" {
			return nil
		}

		def := ruleYAMLToDef(ry, "built-in")
		def.Enabled = true // default enabled; overridden below

		// Apply config overrides for built-in rules.
		def = applyBuiltInOverride(def, cfg)

		// Apply param substitution.
		if sub, ok := builtins.ParamSubstitutors[def.ID]; ok {
			if params := builtInParams(def.ID, cfg); len(params) > 0 {
				if expr := sub(params); expr != "" {
					def.Expression = expr
				}
			}
		}

		// Pre-process expression: expand hasLabel/hasAnnotation macros.
		def.Expression = preprocessExpression(def.Expression)

		rules = append(rules, def)
		return nil
	})
	return rules, err
}

// LoadRuleFiles reads external rule YAML files matched by glob patterns.
func LoadRuleFiles(patterns []string) ([]RuleDef, error) {
	var rules []RuleDef
	seen := map[string]bool{}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
		for _, path := range matches {
			if seen[path] {
				continue
			}
			seen[path] = true

			loaded, err := loadRuleFile(path)
			if err != nil {
				return nil, err
			}
			rules = append(rules, loaded...)
		}
	}
	return rules, nil
}

// LoadCustomInline converts inline custom rule definitions from config to RuleDefs.
func LoadCustomInline(cfg config.RulesConfig) ([]RuleDef, error) {
	var rules []RuleDef
	for _, c := range cfg.Custom {
		def := RuleDef{
			ID:          c.ID,
			Description: c.Description,
			Severity:    Severity(c.Severity),
			Enabled:     true,
			Match: MatchFilter{
				Kinds:             c.Match.Kinds,
				APIGroups:         c.Match.APIGroups,
				Namespaces:        c.Match.Namespaces,
				ExcludeNamespaces: c.Match.ExcludeNamespaces,
				Labels:            c.Match.Labels,
			},
			Expression: preprocessExpression(c.Expression),
			Message:    c.Message,
			Source:     "glint.yaml",
		}
		if def.Severity == "" {
			def.Severity = SeverityError
		}
		rules = append(rules, def)
	}
	return rules, nil
}

// loadRuleFile reads a single external YAML rule file (supports single rule or rules: list).
func loadRuleFile(path string) ([]RuleDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading rule file %q: %w", path, err)
	}

	// Try as rules-list first.
	var doc ruleFileDoc
	if err := yaml.Unmarshal(data, &doc); err == nil && len(doc.Rules) > 0 {
		defs := make([]RuleDef, 0, len(doc.Rules))
		for _, ry := range doc.Rules {
			if ry.ID == "" {
				continue
			}
			def := ruleYAMLToDef(ry, path)
			def.Enabled = true
			def.Expression = preprocessExpression(def.Expression)
			defs = append(defs, def)
		}
		return defs, nil
	}

	// Fall back to single rule.
	var ry ruleYAML
	if err := yaml.Unmarshal(data, &ry); err != nil {
		return nil, fmt.Errorf("parsing rule file %q: %w", path, err)
	}
	if ry.ID == "" {
		return nil, nil
	}
	def := ruleYAMLToDef(ry, path)
	def.Enabled = true
	def.Expression = preprocessExpression(def.Expression)
	return []RuleDef{def}, nil
}

func ruleYAMLToDef(ry ruleYAML, source string) RuleDef {
	sev := Severity(ry.Severity)
	if sev == "" {
		sev = SeverityError
	}
	return RuleDef{
		ID:          ry.ID,
		Description: ry.Description,
		Severity:    sev,
		Match: MatchFilter{
			Kinds:             ry.Match.Kinds,
			APIGroups:         ry.Match.APIGroups,
			Namespaces:        ry.Match.Namespaces,
			ExcludeNamespaces: ry.Match.ExcludeNamespaces,
			Labels:            ry.Match.Labels,
		},
		Expression: ry.Expression,
		Message:    ry.Message,
		Source:     source,
	}
}

func applyBuiltInOverride(def RuleDef, cfg config.RulesConfig) RuleDef {
	var override *config.BuiltInRuleConfig
	switch def.ID {
	case "no-latest-tag":
		c := cfg.BuiltIn.NoLatestTag
		override = &c
	case "resource-requests":
		c := cfg.BuiltIn.ResourceRequests
		override = &c
	case "deprecated-apis":
		c := cfg.BuiltIn.DeprecatedAPIs
		override = &c
	}
	if override == nil {
		return def
	}
	def.Enabled = override.Enabled
	if override.Severity != "" {
		def.Severity = Severity(override.Severity)
	}
	return def
}

func builtInParams(_ string, _ config.RulesConfig) map[string]any {
	return nil
}

// preprocessExpression rewrites hasLabel("key") and hasAnnotation("key") to
// native CEL expressions since those helpers access ambient variables.
var (
	reHasLabel      = regexp.MustCompile(`hasLabel\(("(?:[^"\\]|\\.)*")\)`)
	reHasAnnotation = regexp.MustCompile(`hasAnnotation\(("(?:[^"\\]|\\.)*")\)`)
)

func preprocessExpression(expr string) string {
	expr = reHasLabel.ReplaceAllString(expr, `($1 in labels && labels[$1] != "")`)
	expr = reHasAnnotation.ReplaceAllString(expr, `($1 in annotations && annotations[$1] != "")`)
	return strings.TrimSpace(expr)
}
