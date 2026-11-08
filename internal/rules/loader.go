package rules

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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
	OnEvalError string          `yaml:"on_eval_error"`
}

type matchFilterYAML struct {
	Kinds             []string          `yaml:"kinds"`
	APIGroups         []string          `yaml:"api_groups"`
	Namespaces        []string          `yaml:"namespaces"`
	ExcludeNamespaces []string          `yaml:"exclude_namespaces"`
	Labels            map[string]string `yaml:"labels"`
}

// builtInEntry is one embedded rule file with the category derived from its path.
type builtInEntry struct {
	def      RuleDef
	category string // "" for top-level files directly under builtin/
}

// readEmbeddedBuiltIns reads every embedded built-in rule in WalkDir (lexical)
// order without applying any config. A file in builtin/<category>/ has that
// category; files directly in builtin/ have none. Category rules are validated
// strictly (non-empty id, valid severity); top-level files without an id are skipped.
func readEmbeddedBuiltIns() ([]builtInEntry, error) {
	var entries []builtInEntry
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

		category := ""
		if rel := strings.TrimPrefix(path, "builtin/"); strings.Contains(rel, "/") {
			category = strings.SplitN(rel, "/", 2)[0]
		}

		if category != "" {
			if ry.ID == "" {
				return fmt.Errorf("embedded rule %q: empty id", path)
			}
			if ry.Severity != "" && !config.ValidSeverity(ry.Severity) {
				return fmt.Errorf("embedded rule %q: unknown severity %q (must be error, warning, or info)", path, ry.Severity)
			}
		} else if ry.ID == "" {
			return nil
		}

		source := "built-in"
		if category != "" {
			source = "built-in:" + category
		}
		entries = append(entries, builtInEntry{def: ruleYAMLToDef(ry, source), category: category})
		return nil
	})
	return entries, err
}

// normalizeRuleKey makes "-" and "_" interchangeable when matching rule IDs
// against rules.built_in keys.
func normalizeRuleKey(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// namedBuiltInKeys are the built-in rules that have dedicated fields in
// config.BuiltInRulesConfig, keyed by normalised rule ID.
var namedBuiltInKeys = map[string]bool{
	"no_latest_tag":     true,
	"resource_requests": true,
	"deprecated_apis":   true,
}

// validateBuiltInConfig checks rules.catalogs names and the inline
// rules.built_in overrides against the embedded rules. Maps are walked in
// sorted order so errors are deterministic.
func validateBuiltInConfig(cfg config.RulesConfig, entries []builtInEntry) error {
	var categories []string
	catRules := map[string]bool{} // normalised category rule ID
	for _, e := range entries {
		if e.category == "" {
			continue
		}
		if !slices.Contains(categories, e.category) {
			categories = append(categories, e.category)
		}
		catRules[normalizeRuleKey(e.def.ID)] = true
	}
	sort.Strings(categories)

	catNames := make([]string, 0, len(cfg.Catalogs))
	for n := range cfg.Catalogs {
		catNames = append(catNames, n)
	}
	sort.Strings(catNames)
	for _, n := range catNames {
		if !slices.Contains(categories, n) {
			return fmt.Errorf("rules.catalogs: unknown catalog %q (available: %s)", n, strings.Join(categories, ", "))
		}
	}

	keys := make([]string, 0, len(cfg.BuiltIn.Rules))
	for k := range cfg.BuiltIn.Rules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]string{}
	for _, k := range keys {
		nk := normalizeRuleKey(k)
		switch {
		case namedBuiltInKeys[nk]:
			return fmt.Errorf("rules.built_in.%s: use rules.built_in.%s", k, nk)
		case !catRules[nk]:
			return fmt.Errorf("rules.built_in.%s: unknown built-in rule", k)
		}
		if prev, ok := seen[nk]; ok {
			return fmt.Errorf("rules.built_in.%s: duplicate of rules.built_in.%s (same rule)", k, prev)
		}
		seen[nk] = k
	}
	return nil
}

// applyCategoryOverride resolves enabled state and severity of a category
// rule: per-rule override, then category shorthand, then the file severity.
func applyCategoryOverride(def RuleDef, category string, cfg config.RulesConfig) RuleDef {
	cc := cfg.Catalogs[category]
	def.Enabled = cc.Enabled
	if cc.Severity != "" {
		def.Severity = Severity(strings.ToLower(cc.Severity))
	}
	nid := normalizeRuleKey(def.ID)
	for k, o := range cfg.BuiltIn.Rules { // keys are validated unique after normalisation
		if normalizeRuleKey(k) != nid {
			continue
		}
		if o.Enabled != nil {
			def.Enabled = *o.Enabled
		}
		if o.Severity != "" {
			def.Severity = Severity(strings.ToLower(o.Severity))
		}
	}
	return def
}

// BuiltInCategoryRuleIDs returns the rule IDs of one built-in category
// (e.g. "cis") in load order, or an error for an unknown category.
func BuiltInCategoryRuleIDs(category string) ([]string, error) {
	entries, err := readEmbeddedBuiltIns()
	if err != nil {
		return nil, err
	}
	var ids []string
	var categories []string
	for _, e := range entries {
		if e.category == "" {
			continue
		}
		if !slices.Contains(categories, e.category) {
			categories = append(categories, e.category)
		}
		if e.category == category {
			ids = append(ids, e.def.ID)
		}
	}
	if !slices.Contains(categories, category) {
		sort.Strings(categories)
		return nil, fmt.Errorf("rules.catalogs: unknown catalog %q (available: %s)", category, strings.Join(categories, ", "))
	}
	return ids, nil
}

// LoadBuiltIns reads the embedded rule YAML files and applies config overrides.
// Disabled rules are included with Enabled=false so they appear in `glint rules list`.
// Category rules (builtin/<category>/) are disabled unless enabled through
// rules.catalogs.<category> or a per-rule rules.built_in.<rule-id> entry.
func LoadBuiltIns(cfg config.RulesConfig) ([]RuleDef, error) {
	entries, err := readEmbeddedBuiltIns()
	if err != nil {
		return nil, err
	}
	if err := validateBuiltInConfig(cfg, entries); err != nil {
		return nil, err
	}

	var rules []RuleDef
	for _, e := range entries {
		def := e.def
		if e.category != "" {
			def = applyCategoryOverride(def, e.category, cfg)
		} else {
			def.Enabled = true // default enabled; overridden below
			def = applyBuiltInOverride(def, cfg)

			// Apply param substitution. Rules with a substitutor always use the
			// generated expression regardless of whether the user set params.
			if sub, ok := builtins.ParamSubstitutors[def.ID]; ok {
				if expr := sub(builtInParams(def.ID, cfg)); expr != "" {
					def.Expression = expr
				}
			}
		}

		// Pre-process expression: expand hasLabel/hasAnnotation macros.
		def.Expression = preprocessExpression(def.Expression)

		rules = append(rules, def)
	}
	return rules, nil
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
		if c.Severity != "" && !config.ValidSeverity(c.Severity) {
			return nil, fmt.Errorf("rule %q: unknown severity %q (must be error, warning, or info)", c.ID, c.Severity)
		}
		def := RuleDef{
			ID:          c.ID,
			Description: c.Description,
			Severity:    Severity(strings.ToLower(c.Severity)),
			Enabled:     true,
			Match: MatchFilter{
				Kinds:             c.Match.Kinds,
				APIGroups:         c.Match.APIGroups,
				Namespaces:        c.Match.Namespaces,
				ExcludeNamespaces: c.Match.ExcludeNamespaces,
				Labels:            c.Match.Labels,
			},
			Expression:  preprocessExpression(c.Expression),
			Message:     c.Message,
			Source:      "glint.yaml",
			OnEvalError: c.OnEvalError,
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
	listErr := yaml.Unmarshal(data, &doc)
	if listErr == nil && len(doc.Rules) > 0 {
		defs := make([]RuleDef, 0, len(doc.Rules))
		for _, ry := range doc.Rules {
			if ry.ID == "" {
				continue
			}
			if ry.Severity != "" && !config.ValidSeverity(ry.Severity) {
				return nil, fmt.Errorf("rule file %q: rule %q: unknown severity %q (must be error, warning, or info)", path, ry.ID, ry.Severity)
			}
			def := ruleYAMLToDef(ry, path)
			def.Enabled = true
			def.Expression = preprocessExpression(def.Expression)
			defs = append(defs, def)
		}
		return defs, nil
	}

	// A document with a top-level rules key that failed to decode must be an
	// error: the single-rule fallback ignores the key and would silently drop
	// every rule in the file.
	if listErr != nil && hasRulesKey(data) {
		return nil, fmt.Errorf("parsing rule file %q: %w", path, listErr)
	}

	// Fall back to single rule.
	var ry ruleYAML
	if err := yaml.Unmarshal(data, &ry); err != nil {
		return nil, fmt.Errorf("parsing rule file %q: %w", path, err)
	}
	if ry.ID == "" {
		if listErr != nil {
			return nil, fmt.Errorf("parsing rule file %q: %w", path, listErr)
		}
		return nil, nil
	}
	if ry.Severity != "" && !config.ValidSeverity(ry.Severity) {
		return nil, fmt.Errorf("rule file %q: rule %q: unknown severity %q (must be error, warning, or info)", path, ry.ID, ry.Severity)
	}
	def := ruleYAMLToDef(ry, path)
	def.Enabled = true
	def.Expression = preprocessExpression(def.Expression)
	return []RuleDef{def}, nil
}

// hasRulesKey reports whether data is a YAML mapping with a top-level rules key.
func hasRulesKey(data []byte) bool {
	var probe struct {
		Rules yaml.Node `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Rules.Kind != 0
}

func ruleYAMLToDef(ry ruleYAML, source string) RuleDef {
	sev := Severity(strings.ToLower(ry.Severity))
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
		Expression:  ry.Expression,
		Message:     ry.Message,
		Source:      source,
		OnEvalError: ry.OnEvalError,
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
		def.Severity = Severity(strings.ToLower(override.Severity))
	}
	return def
}

func builtInParams(id string, cfg config.RulesConfig) map[string]any {
	switch id {
	case "no-latest-tag":
		return cfg.BuiltIn.NoLatestTag.Params
	case "resource-requests":
		return cfg.BuiltIn.ResourceRequests.Params
	case "deprecated-apis":
		return cfg.BuiltIn.DeprecatedAPIs.Params
	}
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
