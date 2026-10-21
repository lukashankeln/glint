package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const brokenList = `rules:
  - id: broken
    match:
      kinds: Deployment
    expression: "true"
  - id: other
    expression: "true"
`

func writeRuleFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

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

func TestLoadRuleFiles_ValidList(t *testing.T) {
	path := writeRuleFile(t, "rules:\n  - id: a\n    expression: \"true\"\n  - id: b\n    expression: \"true\"\n")
	defs, err := LoadRuleFiles([]string{path})
	require.NoError(t, err)
	require.Len(t, defs, 2)
	require.Equal(t, "a", defs[0].ID)
	require.Equal(t, "b", defs[1].ID)
}

func TestLoadRuleFiles_EmptyRulesListFallsBackToSingleRule(t *testing.T) {
	path := writeRuleFile(t, "rules: []\nid: x\nexpression: \"true\"\n")
	defs, err := LoadRuleFiles([]string{path})
	require.NoError(t, err)
	require.Len(t, defs, 1)
	require.Equal(t, "x", defs[0].ID)
}
