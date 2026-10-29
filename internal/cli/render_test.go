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

func writeRenderTestApps(t *testing.T, names ...string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	for _, name := range names {
		appDir := filepath.Join(dir, "apps", name)
		require.NoError(t, os.MkdirAll(appDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "cm.yaml"),
			[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glint.yaml"), []byte("fail_on: [error]\n"), 0o644))
	return dir
}

func testConfigMapManifest(name string) manifest.Manifest {
	return manifest.Manifest{
		APIVersion: "v1", Kind: "ConfigMap", Name: name,
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": name},
		},
	}
}

// TestRender_OutputWriteFailureFailsRun checks that an app whose output file
// cannot be written fails the run while the other apps are still written.
func TestRender_OutputWriteFailureFailsRun(t *testing.T) {
	dir := writeRenderTestApps(t, "a", "b")
	outDir := filepath.Join(t.TempDir(), "out")

	oldCfg, oldRender := cfgFile, renderApps
	t.Cleanup(func() { cfgFile, renderApps = oldCfg, oldRender })
	cfgFile = filepath.Join(dir, "glint.yaml")

	var goodName, badName string
	renderApps = func(_ context.Context, apps []discovery.DiscoveredApp, _ *config.Config) []renderResult {
		require.GreaterOrEqual(t, len(apps), 2)
		badName, goodName = apps[0].Name, apps[1].Name
		// The output dir already exists; a directory at the bad app's file
		// path makes os.Create fail.
		require.NoError(t, os.MkdirAll(filepath.Join(outDir, badName+".yaml"), 0o755))
		results := make([]renderResult, len(apps))
		for i, a := range apps {
			results[i] = renderResult{app: a, manifests: []manifest.Manifest{testConfigMapManifest("good-config")}}
		}
		return results
	}

	cmd := newRenderCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{dir, "--output", outDir})
	err := cmd.Execute()

	require.Error(t, err)
	assert.EqualError(t, err, "writing output failed for 1 app(s): "+badName)
	data, readErr := os.ReadFile(filepath.Join(outDir, goodName+".yaml"))
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "good-config")
	info, statErr := os.Stat(filepath.Join(outDir, badName+".yaml"))
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

// TestRender_RenderAndWriteFailuresReportedTogether checks that a render
// failure and a write failure in the same run are both reported.
func TestRender_RenderAndWriteFailuresReportedTogether(t *testing.T) {
	dir := writeRenderTestApps(t, "a", "b", "c")
	outDir := filepath.Join(t.TempDir(), "out")

	oldCfg, oldRender := cfgFile, renderApps
	t.Cleanup(func() { cfgFile, renderApps = oldCfg, oldRender })
	cfgFile = filepath.Join(dir, "glint.yaml")

	var renderBad, writeBad string
	renderApps = func(_ context.Context, apps []discovery.DiscoveredApp, _ *config.Config) []renderResult {
		require.GreaterOrEqual(t, len(apps), 3)
		renderBad, writeBad = apps[0].Name, apps[1].Name
		require.NoError(t, os.MkdirAll(filepath.Join(outDir, writeBad+".yaml"), 0o755))
		return []renderResult{
			{app: apps[0], err: errors.New("helm template failed")},
			{app: apps[1], manifests: []manifest.Manifest{testConfigMapManifest("x")}},
			{app: apps[2], manifests: []manifest.Manifest{testConfigMapManifest("y")}},
		}
	}

	cmd := newRenderCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{dir, "--output", outDir})
	err := cmd.Execute()

	require.Error(t, err)
	assert.EqualError(t, err,
		"rendering failed for 1 app(s): "+renderBad+"\nwriting output failed for 1 app(s): "+writeBad)
}

// TestWriteManifestsToFile_WriteErrorRemovesPartialFile checks that a failed
// write does not leave a partial output file behind.
func TestWriteManifestsToFile_WriteErrorRemovesPartialFile(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full not available")
	}
	dir := t.TempDir()
	app := discovery.DiscoveredApp{Name: "full"}
	path := filepath.Join(dir, "full.yaml")
	require.NoError(t, os.Symlink("/dev/full", path))

	err := writeManifestsToFile(dir, app, []manifest.Manifest{testConfigMapManifest("x")}, "yaml")

	require.Error(t, err)
	_, statErr := os.Lstat(path)
	assert.True(t, os.IsNotExist(statErr), "partial file should be removed, got %v", statErr)
}

type failingWriter struct{ calls *int }

func (w failingWriter) Write([]byte) (int, error) {
	*w.calls++
	return 0, errors.New("disk on fire")
}

// TestRender_StdoutWriteFailureFailsRun checks that a failing stdout is not
// ignored, and that the run stops instead of writing later apps after a
// truncated document.
func TestRender_StdoutWriteFailureFailsRun(t *testing.T) {
	dir := writeRenderTestApps(t, "a", "b")

	oldCfg, oldRender := cfgFile, renderApps
	t.Cleanup(func() { cfgFile, renderApps = oldCfg, oldRender })
	cfgFile = filepath.Join(dir, "glint.yaml")
	var firstName string
	renderApps = func(_ context.Context, apps []discovery.DiscoveredApp, _ *config.Config) []renderResult {
		require.GreaterOrEqual(t, len(apps), 2)
		firstName = apps[0].Name
		return []renderResult{
			{app: apps[0], manifests: []manifest.Manifest{testConfigMapManifest("x")}},
			{app: apps[1], manifests: []manifest.Manifest{testConfigMapManifest("y")}},
		}
	}

	calls := 0
	cmd := newRenderCmd()
	cmd.SetOut(failingWriter{calls: &calls})
	// Keep cobra's usage text off the failing writer; root sets these too.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{dir})

	assert.EqualError(t, cmd.Execute(), "writing output for app "+firstName+": disk on fire")
	assert.Equal(t, 1, calls, "render must stop at the first failed stdout write")
}
