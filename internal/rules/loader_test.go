package rules

import (
	"os"
	"path/filepath"
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
