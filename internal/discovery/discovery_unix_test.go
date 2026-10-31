//go:build !windows

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscover_FIFOSkipped(t *testing.T) {
	root := writeArgoRepo(t)
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "pipe.yaml"), 0o644))

	type result struct {
		apps  []DiscoveredApp
		perrs []ParseError
		err   error
	}
	done := make(chan result, 1)
	go func() {
		apps, perrs, err := Discover(context.Background(), []string{root}, defaultCfg())
		done <- result{apps, perrs, err}
	}()
	select {
	case res := <-done:
		require.NoError(t, res.err)
		assert.Empty(t, res.perrs)
		assert.NotEmpty(t, filterByFramework(res.apps, FrameworkArgoCD))
	case <-time.After(10 * time.Second):
		t.Fatal("Discover blocked on a FIFO")
	}
}

func TestParseArgoCDApplication_SymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "inlink")))
	src := filepath.Join(root, "app.yaml")

	_, err := parseArgoCDApplication(argoAppDoc("link"), newRepoRoot(root), src, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the repository root")

	_, err = parseArgoCDApplication(argoAppDoc("link/sub"), newRepoRoot(root), src, nil)
	assert.Error(t, err)

	// A dangling symlink is rejected, not skipped as nonexistent.
	_, err = parseArgoCDApplication(argoAppDoc("dangling"), newRepoRoot(root), src, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving symlinks")

	// A symlink that stays inside the repo is accepted and the lexical path is kept.
	app, err := parseArgoCDApplication(argoAppDoc("inlink"), newRepoRoot(root), src, nil)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "inlink"), app.RootPath)
}

func TestParseGitOps_SymlinkEscapeRejectedAllSites(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "charts", "x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "charts", "x", "Chart.yaml"), []byte("apiVersion: v2\nname: x\nversion: 0.1.0\n"), 0o644))
	src := filepath.Join(root, "app.yaml")

	_, err := parseFluxKustomization([]byte(fluxKustomizationDoc("apps", "link", "GitRepository", "flux-system")), newRepoRoot(root), src, fluxResolver{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the repository root")

	_, err = parseFluxHelmRelease(fluxHelmReleaseChartDoc("./link"), newRepoRoot(root), src, fluxResolver{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the repository root")

	_, err = parseArgoCDApplication(argoAppDoc("charts/x", "../../link/v.yaml"), newRepoRoot(root), src, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the repository root")
}

func TestJoinInRepo_ErrorHasNoRawTargetBytes(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Symlink("\x1b[2J\nevil", filepath.Join(root, "bad")))

	_, err := joinInRepo(newRepoRoot(root), root, "bad")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\x1b")
	assert.NotContains(t, err.Error(), "\n")
}
