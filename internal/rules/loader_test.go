package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
)

func writeRule(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rule.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestLoadRuleFiles_UnknownSeverity(t *testing.T) {
	single := writeRule(t, "id: r1\nseverity: Critical\nexpression: \"true\"\nmessage: m\n")
	list := writeRule(t, "rules:\n  - id: r1\n    severity: Critical\n    expression: \"true\"\n    message: m\n")
	for _, p := range []string{single, list} {
		_, err := LoadRuleFiles([]string{p})
		require.Error(t, err, p)
		assert.Contains(t, err.Error(), "Critical")
	}
}

func TestLoadRuleFiles_SeverityCaseInsensitive(t *testing.T) {
	single := writeRule(t, "id: r1\nseverity: WARNING\nexpression: \"true\"\nmessage: m\n")
	list := writeRule(t, "rules:\n  - id: r1\n    severity: WARNING\n    expression: \"true\"\n    message: m\n")
	for _, p := range []string{single, list} {
		defs, err := LoadRuleFiles([]string{p})
		require.NoError(t, err)
		require.Len(t, defs, 1)
		assert.Equal(t, SeverityWarning, defs[0].Severity)
	}
}

func TestLoadCustomInline_Severity(t *testing.T) {
	defs, err := LoadCustomInline(config.RulesConfig{Custom: []config.CustomRuleDef{
		{ID: "a", Severity: "Info", Expression: "true", Message: "m"},
	}})
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, SeverityInfo, defs[0].Severity)

	cfg := defaultRulesCfg()
	cfg.Custom = []config.CustomRuleDef{{ID: "a", Severity: "nope", Expression: "true", Message: "m"}}
	_, err = NewEngine(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

func TestApplyBuiltInOverride_NormalizesSeverity(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.NoLatestTag.Severity = "Warning"
	def := applyBuiltInOverride(RuleDef{ID: "no-latest-tag", Severity: SeverityError}, cfg)
	assert.Equal(t, SeverityWarning, def.Severity)
}

func writeRuleFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRuleFiles_MalformedListIsError(t *testing.T) {
	cases := map[string]string{
		"type error in rule": "rules:\n  - id: a\n    expression: \"true\"\n    match: {kinds: Deployment}\n",
		"rules is mapping":   "rules:\n  id: a\n  expression: \"true\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeRuleFile(t, content)
			_, err := LoadRuleFiles([]string{p})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), p) {
				t.Errorf("error %q does not mention path %q", err, p)
			}
		})
	}
}

func TestLoadRuleFiles_ValidFiles(t *testing.T) {
	list := writeRuleFile(t, "rules:\n  - id: a\n    expression: \"true\"\n  - id: b\n    expression: \"true\"\n")
	got, err := LoadRuleFiles([]string{list})
	if err != nil || len(got) != 2 {
		t.Fatalf("list: got %d rules, err %v", len(got), err)
	}
	single := writeRuleFile(t, "id: solo\nexpression: \"true\"\n")
	got, err = LoadRuleFiles([]string{single})
	if err != nil || len(got) != 1 || got[0].ID != "solo" {
		t.Fatalf("single: got %+v, err %v", got, err)
	}
}

func TestLoadRuleFiles_EmptyFile(t *testing.T) {
	p := writeRuleFile(t, "")
	got, err := LoadRuleFiles([]string{p})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d rules, err %v", len(got), err)
	}
}

func TestNewEngine_MalformedRuleFileIsError(t *testing.T) {
	p := writeRuleFile(t, "rules:\n  - id: a\n    expression: \"true\"\n    match: {kinds: Deployment}\n")
	if _, err := NewEngine(config.RulesConfig{RuleFiles: []string{p}}); err == nil {
		t.Fatal("expected error")
	}
}

const brokenList = `rules:
  - id: broken
    match:
      kinds: Deployment
    expression: "true"
  - id: other
    expression: "true"
`

func TestLoadRuleFiles_MalformedRulesError(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"list with scalar kinds", brokenList},
		{"rules is scalar", "rules: foo\n"},
		{"top-level rule plus broken list", "id: x\nexpression: \"true\"\n" + brokenList},
		{"single rule with scalar kinds", "id: x\nexpression: \"true\"\nmatch:\n  kinds: Deployment\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeRuleFile(t, tt.content)
			defs, err := LoadRuleFiles([]string{path})
			require.Error(t, err)
			require.Nil(t, defs)
			require.Contains(t, err.Error(), path)
		})
	}
}

func TestLoadRuleFiles_EmptyRulesListFallsBackToSingleRule(t *testing.T) {
	path := writeRuleFile(t, "rules: []\nid: x\nexpression: \"true\"\n")
	defs, err := LoadRuleFiles([]string{path})
	require.NoError(t, err)
	require.Len(t, defs, 1)
	require.Equal(t, "x", defs[0].ID)
}
