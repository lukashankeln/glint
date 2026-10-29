//go:build !windows

package render

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
