package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/rules"
)

func TestMixedCaseSeverity_SARIFAndCounts(t *testing.T) {
	cfg := config.RulesConfig{Custom: []config.CustomRuleDef{
		{ID: "always", Severity: "Warning", Expression: "false", Message: "bad"},
	}}
	cfg.BuiltIn.NoLatestTag.Enabled = false
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false
	cfg.BuiltIn.ResourceRequests.Enabled = false
	engine, err := rules.NewEngine(cfg)
	require.NoError(t, err)

	obj := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "x"}}
	vs := engine.Evaluate([]manifest.Manifest{{APIVersion: "v1", Kind: "ConfigMap", Name: "x", Object: obj}})
	require.Len(t, vs, 1)

	assert.Equal(t, 1, countBySeverity(vs, rules.SeverityWarning))
	assert.Equal(t, 0, countBySeverity(vs, rules.SeverityError))

	var buf bytes.Buffer
	require.NoError(t, writeSARIF(&buf, vs))
	assert.Contains(t, buf.String(), `"level": "warning"`)
}
