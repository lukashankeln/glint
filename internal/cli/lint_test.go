package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

func TestWriteReportFile_WriteErrorRemovesPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")

	err := writeReportFile(path, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return errors.New("disk on fire")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
	assert.Contains(t, err.Error(), "disk on fire")
	_, statErr := os.Lstat(path)
	assert.True(t, os.IsNotExist(statErr), "partial file should be removed, got %v", statErr)
}

func TestWriteReportFile_CloseErrorReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")

	err := writeReportFile(path, func(w io.Writer) error {
		f, ok := w.(*os.File)
		require.True(t, ok, "writeReportFile must pass the raw *os.File")
		return f.Close()
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "closing "+fmt.Sprintf("%q", path))
	assert.Contains(t, err.Error(), "file already closed")
	_, statErr := os.Lstat(path)
	assert.True(t, os.IsNotExist(statErr), "file should be removed, got %v", statErr)
}

func TestWriteReportFile_NonRegularNotRemoved(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full not available")
	}
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.Symlink("/dev/full", path))

	err := writeReportFile(path, func(w io.Writer) error {
		_, err := w.Write([]byte("x"))
		return err
	})

	require.Error(t, err)
	_, statErr := os.Lstat(path)
	assert.NoError(t, statErr, "symlink must not be removed")
}

// lintFixture sets up one discoverable app with a config and a render that
// succeeds with no manifests (so there are no violations).
func lintFixture(t *testing.T) string {
	t.Helper()
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
	renderApps = func(_ context.Context, apps []discovery.DiscoveredApp, _ *config.Config) []renderResult {
		out := make([]renderResult, len(apps))
		for i, a := range apps {
			out[i] = renderResult{app: a}
		}
		return out
	}
	return dir
}

// TestLint_ReportFileWriteFailureFailsRun checks that a report file that
// cannot be written fails the run with a non-violations error (exit 2) and
// that a non-regular target is left in place.
func TestLint_ReportFileWriteFailureFailsRun(t *testing.T) {
	dir := lintFixture(t)

	t.Run("failure", func(t *testing.T) {
		if _, err := os.Stat("/dev/full"); err != nil {
			t.Skip("/dev/full not available")
		}
		for flag, prefix := range map[string]string{
			"--output":       "writing output: ",
			"--output-sarif": "writing SARIF output: ",
			"--output-junit": "writing JUnit output: ",
			"--output-json":  "writing json summary: ",
		} {
			t.Run(flag, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "report")
				require.NoError(t, os.Symlink("/dev/full", path))

				cmd := newLintCmd()
				cmd.SilenceUsage, cmd.SilenceErrors = true, true
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})
				cmd.SetArgs([]string{"--format", "json", flag, path, dir})
				err := cmd.Execute()

				require.Error(t, err)
				var vf *ViolationsFoundError
				assert.False(t, errors.As(err, &vf), "must not be a violations error")
				assert.True(t, strings.HasPrefix(err.Error(), prefix), "got %q", err)
				_, statErr := os.Lstat(path)
				assert.NoError(t, statErr, "non-regular target must not be removed")
			})
		}
	})

	t.Run("success", func(t *testing.T) {
		out := t.TempDir()
		paths := map[string]string{
			"--output":       filepath.Join(out, "out.json"),
			"--output-sarif": filepath.Join(out, "out.sarif"),
			"--output-junit": filepath.Join(out, "out.xml"),
			"--output-json":  filepath.Join(out, "summary.json"),
		}
		args := []string{"--format", "json"}
		for flag, p := range paths {
			args = append(args, flag, p)
		}
		cmd := newLintCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append(args, dir))

		require.NoError(t, cmd.Execute())
		for _, p := range paths {
			fi, err := os.Stat(p)
			require.NoError(t, err)
			assert.NotZero(t, fi.Size(), p)
		}
	})
}

// TestLint_SummaryWriteFailureFailsRun checks that a failing stdout is not
// ignored for the text summary or the "No apps discovered." message.
func TestLint_SummaryWriteFailureFailsRun(t *testing.T) {
	run := func(t *testing.T, path string) (int, error) {
		calls := 0
		cmd := newLintCmd()
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetOut(failingWriter{calls: &calls})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"--format", "text", path})
		return calls, cmd.Execute()
	}

	t.Run("summary", func(t *testing.T) {
		calls, err := run(t, lintFixture(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "writing summary")
		assert.Contains(t, err.Error(), "disk on fire")
		assert.Equal(t, 1, calls)
	})

	t.Run("no apps", func(t *testing.T) {
		oldCfg := cfgFile
		t.Cleanup(func() { cfgFile = oldCfg })
		cfgFile = ""
		_, err := run(t, t.TempDir())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "writing output")
		assert.Contains(t, err.Error(), "disk on fire")
	})
}

// TestLint_SummaryLineConfig checks that output.summary gates the text
// summary line and defaults to on.
func TestLint_SummaryLineConfig(t *testing.T) {
	run := func(t *testing.T, cfgYAML string) string {
		dir := lintFixture(t)
		cfgPath := filepath.Join(dir, "glint.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgYAML), 0o644))
		var buf bytes.Buffer
		cmd := newLintCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"--format", "text", dir})
		require.NoError(t, cmd.Execute())
		return buf.String()
	}

	assert.Contains(t, run(t, "fail_on: [error]\n"), "0 errors, 0 warnings across")
	assert.Contains(t, run(t, "output:\n  summary: true\n"), "0 errors, 0 warnings across")
	assert.NotContains(t, run(t, "output:\n  summary: false\n"), "errors,")
}
