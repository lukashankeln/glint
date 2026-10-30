//go:build !windows

package plugins

import (
	"context"
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

	"github.com/lukashankeln/glint/internal/config"
)

// withPluginBound runs fn and fails the test if it does not return in time.
func withPluginBound(t *testing.T, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("plugin run blocked past the timeout and pipe wait delay")
		return nil
	}
}

// orphanSetup lowers pluginWaitDelay, points the helper at a pidfile and kills
// the leftover grandchild on cleanup.
func orphanSetup(t *testing.T) string {
	t.Helper()
	orig := pluginWaitDelay
	pluginWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { pluginWaitDelay = orig })

	pidfile := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Setenv("GLINT_TEST_PIDFILE", pidfile)
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

func TestRunner_TimeoutWithOrphanedChild(t *testing.T) {
	pidfile := orphanSetup(t)
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Timeout: "2s",
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "orphan-hang"},
	})
	require.NoError(t, err)

	err = withPluginBound(t, func() error {
		_, err := r.Run(context.Background(), testManifests)
		return err
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.FileExists(t, pidfile, "grandchild must have held the pipe")
}

func TestRunner_CleanExitWithOrphanedChild(t *testing.T) {
	for name, allow := range map[string]bool{"strict": false, "allow_nonzero_exit": true} {
		t.Run(name, func(t *testing.T) {
			pidfile := orphanSetup(t)
			r, err := New(config.PluginConfig{
				Name:             "test",
				Command:          selfExe(t),
				AllowNonZeroExit: allow,
				Env:              map[string]string{"GLINT_PLUGIN_TEST_MODE": "orphan-exit"},
			})
			require.NoError(t, err)

			err = withPluginBound(t, func() error {
				_, err := r.Run(context.Background(), testManifests)
				return err
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, exec.ErrWaitDelay)
			assert.Contains(t, err.Error(), "kept its output open")
			assert.Contains(t, err.Error(), "orphan-exit stderr marker")
			assert.FileExists(t, pidfile)
		})
	}
}
