//go:build !windows

package render

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/fsutil"
)

// withDeadline runs fn and fails the test if it does not return in time.
func withDeadline(t *testing.T, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("call blocked on a FIFO")
		return nil
	}
}

func TestBuildValues_FIFOValuesFile(t *testing.T) {
	vf := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, syscall.Mkfifo(vf, 0o644))

	r := newHelmRenderer(defaultCfg())
	err := withDeadline(t, func() error {
		_, err := r.buildValues(discovery.DiscoveredApp{Name: "x", ValuesFiles: []string{vf}})
		return err
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

func TestRawRenderer_FIFOFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "pipe.yaml"), 0o644))

	app := discovery.DiscoveredApp{Name: "raw", Renderer: discovery.RendererRaw, RootPath: root}
	err := withDeadline(t, func() error {
		_, err := New(app, defaultCfg()).Render(context.Background(), app)
		return err
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

const minimalChartYAML = "apiVersion: v2\nname: c\nversion: 0.1.0\n"

func renderChartDir(t *testing.T, dir string) error {
	t.Helper()
	r := newHelmRenderer(defaultCfg())
	return withDeadline(t, func() error {
		_, err := r.renderSDK(context.Background(), discovery.DiscoveredApp{Name: "c", RootPath: dir})
		return err
	})
}

func TestHelmRenderer_ChartYAMLFIFO(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "Chart.yaml"), 0o644))
	err := renderChartDir(t, dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

func TestHelmRenderer_ChartYAMLSymlinkToFIFO(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe")
	require.NoError(t, syscall.Mkfifo(pipe, 0o644))
	require.NoError(t, os.Symlink(pipe, filepath.Join(dir, "Chart.yaml")))
	err := renderChartDir(t, dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

func TestHelmRenderer_HelmignoreFIFO(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(minimalChartYAML), 0o644))
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, ".helmignore"), 0o644))
	err := renderChartDir(t, dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

func TestHelmSubprocess_ChartYAMLFIFO(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "Chart.yaml"), 0o644))
	err := withDeadline(t, func() error {
		_, err := renderHelmSubprocess(context.Background(), discovery.DiscoveredApp{Name: "c", RootPath: dir}, nil)
		return err
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

func kustApp(t *testing.T, kustomization string) (string, discovery.DiscoveredApp) {
	t.Helper()
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("/dev/zero not available")
	}
	dir := t.TempDir()
	if kustomization != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(kustomization), 0o644))
	}
	return dir, discovery.DiscoveredApp{Name: "k", Renderer: discovery.RendererKustomize, RootPath: dir}
}

func renderKust(t *testing.T, app discovery.DiscoveredApp) error {
	t.Helper()
	return renderKustWith(t, defaultCfg(), app)
}

// renderKustUnrestricted renders with load_restrictor none, so tests of the
// read cap reach the file read instead of stopping at the load restrictor.
func renderKustUnrestricted(t *testing.T, app discovery.DiscoveredApp) error {
	t.Helper()
	cfg := defaultCfg()
	cfg.Render.Kustomize.LoadRestrictor = config.LoadRestrictorNone
	return renderKustWith(t, cfg, app)
}

func renderKustWith(t *testing.T, cfg *config.Config, app discovery.DiscoveredApp) error {
	t.Helper()
	return withDeadline(t, func() error {
		_, err := newKustomizeRenderer(cfg).renderSDK(context.Background(), app)
		return err
	})
}

func TestKustomizeSDK_SymlinkToDevZeroResource(t *testing.T) {
	dir, app := kustApp(t, "resources:\n- z.yaml\n")
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(dir, "z.yaml")))
	err := renderKustUnrestricted(t, app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestKustomizeSDK_AbsoluteDevZeroResource(t *testing.T) {
	_, app := kustApp(t, "resources:\n- /dev/zero\n")
	err := renderKustUnrestricted(t, app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestKustomizeSDK_AbsoluteDevZeroResourceRootOnly(t *testing.T) {
	_, app := kustApp(t, "resources:\n- /dev/zero\n")
	err := renderKust(t, app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not in or below")
}

func TestKustomizeSDK_FIFOResource(t *testing.T) {
	dir, app := kustApp(t, "resources:\n- p.yaml\n")
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "p.yaml"), 0o644))
	err := renderKust(t, app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestKustomizeSDK_KustomizationSymlinkToDevZero(t *testing.T) {
	dir, app := kustApp(t, "")
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(dir, "kustomization.yaml")))
	// kustomize drops the load error and reports the file as missing.
	err := renderKust(t, app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to find one of")
}

func TestCappedFS_ReadFileSymlinkToDevZero(t *testing.T) {
	dir, _ := kustApp(t, "")
	link := filepath.Join(dir, "zero")
	require.NoError(t, os.Symlink("/dev/zero", link))
	_, err := cappedFS{filesys.MakeFsOnDisk()}.ReadFile(link)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

// withRenderBound runs fn and fails the test if it does not return in time.
func withRenderBound(t *testing.T, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("render blocked past the timeout and pipe wait delay")
		return nil
	}
}

// orphanSetup lowers subprocessWaitDelay, points the helper at a pidfile and
// kills the leftover grandchild on cleanup.
func orphanSetup(t *testing.T, mode string) string {
	t.Helper()
	orig := subprocessWaitDelay
	subprocessWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { subprocessWaitDelay = orig })

	pidfile := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Setenv("GLINT_TEST_PIDFILE", pidfile)
	fakeExec(t, mode)
	t.Cleanup(func() {
		b, err := os.ReadFile(pidfile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return pidfile
}

func orphanApps(t *testing.T) map[string]discovery.DiscoveredApp {
	t.Helper()
	return map[string]discovery.DiscoveredApp{
		"helm":      {Name: "x", Renderer: discovery.RendererHelm, RootPath: t.TempDir()},
		"kustomize": {Name: "x", Renderer: discovery.RendererKustomize, RootPath: t.TempDir()},
	}
}

func orphanCfg(name, timeout string) *config.Config {
	cfg := defaultCfg()
	cfg.Render.SubprocessFallback = true
	if name == "helm" {
		cfg.Render.Helm.Timeout = timeout
	} else {
		cfg.Render.Kustomize.Timeout = timeout
	}
	return cfg
}

func TestSubprocessRenderers_TimeoutWithOrphanedChild(t *testing.T) {
	for name, app := range orphanApps(t) {
		t.Run(name, func(t *testing.T) {
			pidfile := orphanSetup(t, "orphan-hang")
			r := New(app, orphanCfg(name, "2s"))
			err := withRenderBound(t, func() error {
				_, err := r.Render(context.Background(), app)
				return err
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Contains(t, err.Error(), "timed out")
			assert.FileExists(t, pidfile, "grandchild must have held the pipe")
		})
	}
}

func TestSubprocessRenderers_CleanExitWithOrphanedChild(t *testing.T) {
	for name, app := range orphanApps(t) {
		t.Run(name, func(t *testing.T) {
			pidfile := orphanSetup(t, "orphan-exit")
			r := New(app, orphanCfg(name, ""))
			err := withRenderBound(t, func() error {
				_, err := r.Render(context.Background(), app)
				return err
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, exec.ErrWaitDelay)
			assert.Contains(t, err.Error(), "kept its output open")
			assert.FileExists(t, pidfile)
		})
	}
}
