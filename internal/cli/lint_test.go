package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/render"
	"github.com/lukashankeln/glint/internal/rules"
)

func TestRenderErrorViolation_AmbiguousMergeKey(t *testing.T) {
	app := discovery.DiscoveredApp{Name: "frontend", RootPath: "/repo/apps/web"}
	err := fmt.Errorf("splitting YAML in %q: %w", "/repo/apps/web/a.yaml",
		fmt.Errorf("resolving merge keys: %w", render.ErrAmbiguousMergeKey))

	v, ok := renderErrorViolation(renderResult{app: app, err: err})
	require.True(t, ok)
	assert.Equal(t, "glint/parse-error", v.RuleID)
	assert.Equal(t, rules.SeverityError, v.Severity)
	assert.Equal(t, "/repo/apps/web", v.FilePath)
	assert.Equal(t, "render", v.Source)
	assert.Contains(t, v.Message, `app "frontend"`)
	assert.Contains(t, v.Message, "resolving merge keys")
	assert.True(t, failsThreshold([]rules.Violation{v}, []string{"error"}))
}

func TestRenderErrorViolation_OtherErrorsStaySkips(t *testing.T) {
	app := discovery.DiscoveredApp{Name: "web"}
	for _, err := range []error{nil, errors.New("helm template failed")} {
		_, ok := renderErrorViolation(renderResult{app: app, err: err})
		assert.False(t, ok, "err=%v", err)
	}
}

// TestLint_AmbiguousMergeKeyFailsRun runs the lint command end to end with a
// render that fails with ErrAmbiguousMergeKey: the run must fail with a
// glint/parse-error violation, while any other render error stays a skip.
func TestLint_AmbiguousMergeKeyFailsRun(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "web")
	require.NoError(t, os.MkdirAll(appDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "cm.yaml"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), 0o644))
	cfgPath := filepath.Join(dir, "glint.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("fail_on: [error]\n"), 0o644))

	oldCfg, oldRender := cfgFile, renderApps
	t.Cleanup(func() { cfgFile, renderApps = oldCfg, oldRender })
	cfgFile = cfgPath

	run := func(renderErr error) (string, error) {
		renderApps = func(_ context.Context, apps []discovery.DiscoveredApp, _ *config.Config) []renderResult {
			out := make([]renderResult, len(apps))
			for i, a := range apps {
				out[i] = renderResult{app: a, err: renderErr}
			}
			return out
		}
		var buf bytes.Buffer
		cmd := newLintCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"--format", "text", dir})
		err := cmd.Execute()
		return buf.String(), err
	}

	out, err := run(fmt.Errorf("splitting YAML: %w", render.ErrAmbiguousMergeKey))
	require.NotContains(t, out, "No apps discovered")
	var vf *ViolationsFoundError
	require.ErrorAs(t, err, &vf)
	assert.Contains(t, out, "glint/parse-error")

	out, err = run(errors.New("helm template failed"))
	require.NotContains(t, out, "No apps discovered")
	require.NoError(t, err)
	assert.NotContains(t, out, "glint/parse-error")
}
