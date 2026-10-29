package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestRenderErrorViolation_OtherErrorsReported(t *testing.T) {
	app := discovery.DiscoveredApp{Name: "web", RootPath: "/repo/apps/web"}

	_, ok := renderErrorViolation(renderResult{app: app})
	assert.False(t, ok)

	v, ok := renderErrorViolation(renderResult{app: app, err: errors.New("helm template failed")})
	require.True(t, ok)
	assert.Equal(t, "glint/render-error", v.RuleID)
	assert.Equal(t, rules.SeverityError, v.Severity)
	assert.Equal(t, "render", v.Source)
	assert.Equal(t, "/repo/apps/web", v.FilePath)
	assert.Contains(t, v.Message, `app "web"`)
	assert.Contains(t, v.Message, "helm template failed")
	assert.True(t, failsThreshold([]rules.Violation{v}, []string{"error"}))
}

// TestLint_AmbiguousMergeKeyFailsRun runs the lint command end to end with a
// render that fails with ErrAmbiguousMergeKey: the run must fail with a
// glint/parse-error violation, while any other render error fails it with a
// glint/render-error violation.
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

	run := func(renderErr error, extraArgs ...string) (string, error) {
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
		cmd.SetArgs(append([]string{"--format", "text", dir}, extraArgs...))
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
	require.ErrorAs(t, err, &vf)
	assert.Contains(t, out, "glint/render-error")
	assert.NotContains(t, out, "glint/parse-error")

	out, err = run(errors.New("helm template failed"), "--skip-rules", "glint/render-error")
	require.NotContains(t, out, "No apps discovered")
	require.NoError(t, err)
	assert.NotContains(t, out, "glint/render-error")
}

// TestLint_RealRenderErrorFailsRun lints malformed plain YAML without
// injecting render results: the app must fail to render and be reported as a
// glint/render-error violation, not as a discovery problem.
func TestLint_RealRenderErrorFailsRun(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "web")
	require.NoError(t, os.MkdirAll(appDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "bad.yaml"), []byte("key: [unclosed\n"), 0o644))
	cfgPath := filepath.Join(dir, "glint.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("fail_on: [error]\n"), 0o644))

	oldCfg := cfgFile
	t.Cleanup(func() { cfgFile = oldCfg })
	cfgFile = cfgPath

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--format", "text", dir})
	err := cmd.Execute()

	out := buf.String()
	require.NotContains(t, out, "No apps discovered")
	var vf *ViolationsFoundError
	require.ErrorAs(t, err, &vf)

	var renderErrs, discoveryLines int
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "[ERROR] discovery ") {
			discoveryLines++
		}
		if strings.HasPrefix(line, "[ERROR] render ") && strings.HasSuffix(line, "[glint/render-error]") {
			renderErrs++
		}
	}
	assert.Equal(t, 1, renderErrs, "output:\n%s", out)
	assert.Zero(t, discoveryLines, "output:\n%s", out)
}
