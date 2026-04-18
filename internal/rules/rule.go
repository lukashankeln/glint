package rules

import (
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// RuleDef is the internal representation of a policy rule.
type RuleDef struct {
	ID          string
	Description string
	Severity    Severity
	Enabled     bool
	Match       MatchFilter
	Expression  string // CEL expression; true = compliant, false = violation
	Message     string // Go text/template; vars: .kind .name .namespace .apiVersion .severity
	Source      string // "built-in" | file path
}

// MatchFilter is a pre-CEL filter that scopes a rule to specific resources.
// All non-empty fields must match (AND).
type MatchFilter struct {
	Kinds             []string
	APIGroups         []string
	Namespaces        []string          // glob whitelist (empty = all)
	ExcludeNamespaces []string          // glob blacklist
	Labels            map[string]string // required label selector (AND)
}

// Matches returns true if the manifest passes all filter conditions.
func (f MatchFilter) Matches(m manifest.Manifest) bool {
	if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, m.Kind) {
		return false
	}

	if len(f.APIGroups) > 0 {
		group := extractAPIGroup(m.APIVersion)
		if !slices.Contains(f.APIGroups, group) {
			return false
		}
	}

	if len(f.Namespaces) > 0 && !matchesAnyGlob(m.Namespace, f.Namespaces) {
		return false
	}

	if len(f.ExcludeNamespaces) > 0 && matchesAnyGlob(m.Namespace, f.ExcludeNamespaces) {
		return false
	}

	if len(f.Labels) > 0 && !matchesLabels(m, f.Labels) {
		return false
	}

	return true
}

// Violation is a policy violation produced by the rule engine.
type Violation struct {
	RuleID       string
	Severity     Severity
	Message      string
	Source       string // "cel"
	APIVersion   string
	ResourceKind string
	ResourceName string
	ResourceNS   string
	FilePath     string
	Rendered     bool
}

func extractAPIGroup(apiVersion string) string {
	if idx := strings.LastIndex(apiVersion, "/"); idx >= 0 {
		return apiVersion[:idx]
	}
	return "" // core group (v1)
}

func matchesAnyGlob(value string, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := doublestar.Match(p, value); matched {
			return true
		}
	}
	return false
}

// ExceptionEntry is the engine-internal form of a single resource exception selector.
type ExceptionEntry struct {
	Kind      string // exact match; empty = any
	Name      string // glob; empty = any
	Namespace string // glob; empty = any
}

// BuildExceptionIndex converts config exceptions into a map keyed by rule ID
// for O(1) per-rule lookup. Called once at engine construction time.
func BuildExceptionIndex(cfgExceptions []config.ExceptionConfig) map[string][]ExceptionEntry {
	idx := make(map[string][]ExceptionEntry, len(cfgExceptions))
	for _, exc := range cfgExceptions {
		for _, res := range exc.Resources {
			idx[exc.Rule] = append(idx[exc.Rule], ExceptionEntry{
				Kind: res.Kind, Name: res.Name, Namespace: res.Namespace,
			})
		}
	}
	return idx
}

// matchesException returns true when the manifest matches at least one exception entry.
// Empty fields act as wildcards (match any value).
func matchesException(entries []ExceptionEntry, m manifest.Manifest) bool {
	for _, e := range entries {
		if e.Kind != "" && e.Kind != m.Kind {
			continue
		}
		if e.Name != "" && !matchesAnyGlob(m.Name, []string{e.Name}) {
			continue
		}
		if e.Namespace != "" && !matchesAnyGlob(m.Namespace, []string{e.Namespace}) {
			continue
		}
		return true
	}
	return false
}

func matchesLabels(m manifest.Manifest, required map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	meta, ok := m.Object["metadata"].(map[string]any)
	if !ok {
		return false
	}
	labels, _ := meta["labels"].(map[string]any)
	for k, v := range required {
		if lv, ok := labels[k].(string); !ok || lv != v {
			return false
		}
	}
	return true
}
