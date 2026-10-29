package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// TestRender_FailedAppFailsRun checks that glint render still writes the apps
// that rendered, but returns an error naming the app that did not.
func TestRender_FailedAppFailsRun(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		appDir := filepath.Join(dir, "apps", name)
		require.NoError(t, os.MkdirAll(appDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "cm.yaml"),
			[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), 0o644))
	}
	cfgPath := filepath.Join(dir, "glint.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("fail_on: [error]\n"), 0o644))

	oldCfg, oldRender := cfgFile, renderApps
	t.Cleanup(func() { cfgFile, renderApps = oldCfg, oldRender })
	cfgFile = cfgPath

	var goodName, badName string
	renderApps = func(_ context.Context, apps []discovery.DiscoveredApp, _ *config.Config) []renderResult {
		require.GreaterOrEqual(t, len(apps), 2)
		// The failing app comes first, so a render that stopped at the first
		// failure would never print the good app.
		badName, goodName = apps[0].Name, apps[1].Name
		results := make([]renderResult, len(apps))
		for i, a := range apps {
			results[i] = renderResult{app: a}
		}
		results[0] = renderResult{app: apps[0], err: errors.New("helm template failed")}
		results[1] = renderResult{app: apps[1], manifests: []manifest.Manifest{{
			APIVersion: "v1", Kind: "ConfigMap", Name: "good-config",
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]any{"name": "good-config"},
			},
		}}}
		return results
	}

	var buf bytes.Buffer
	cmd := newRenderCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir})
	err := cmd.Execute()

	require.Error(t, err)
	assert.EqualError(t, err, "rendering failed for 1 app(s): "+badName)
	out := buf.String()
	assert.Contains(t, out, "# --- app: "+goodName+" ---")
	assert.Contains(t, out, "good-config")
	assert.NotContains(t, out, "# --- app: "+badName+" ---")
}
