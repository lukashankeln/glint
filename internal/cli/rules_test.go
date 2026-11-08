package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRulesList_ColumnsAlignWithLongID(t *testing.T) {
	longID := "cis-9.9.9-" + strings.Repeat("x", 40)
	cfgPath := filepath.Join(t.TempDir(), "glint.yaml")
	yml := "rules:\n  custom:\n    - id: " + longID + "\n      expression: \"true\"\n      message: m\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(yml), 0o644))

	oldCfg := cfgFile
	t.Cleanup(func() { cfgFile = oldCfg })
	cfgFile = cfgPath

	cmd := newRulesListCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 3)
	col := strings.Index(lines[0], "SEVERITY")
	assert.Equal(t, len(longID)+2, col)
	for _, l := range lines[2:] {
		assert.Equal(t, col, strings.Index(l[col:], l[col:])+col)
		assert.True(t, len(l) > col)
		assert.Equal(t, byte(' '), l[col-1])
	}
}
