package render

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/lukashankeln/glint/internal/fsutil"
	"github.com/lukashankeln/glint/internal/subproc"
)

// execCommandContext is exec.CommandContext, replaceable in tests.
var execCommandContext = exec.CommandContext

// subprocessWaitDelay bounds how long the output pipes of a helm/kustomize
// subprocess are drained after the child exits or the context is done, so a
// grandchild that inherited the pipes cannot hold cmd.Run open. It is a
// package var so tests can lower it.
var subprocessWaitDelay = subproc.DefaultWaitDelay

// readFileCapped reads path through fsutil.ReadFileCapped. A file over limit
// returns an error wrapping both errStreamTooLarge and fsutil.ErrTooLarge; a
// non-regular file (e.g. a symlink to /dev/zero or a FIFO) wraps
// fsutil.ErrNotRegular and is never opened.
func readFileCapped(path string, limit int) ([]byte, error) {
	data, err := fsutil.ReadFileCapped(path, limit)
	if errors.Is(err, fsutil.ErrTooLarge) {
		return nil, fmt.Errorf("%w: %w", errStreamTooLarge, err)
	}
	return data, err
}

// checkFileCapped is readFileCapped without the read: it Stats path and
// applies the same regular-file and size checks, never opening it.
func checkFileCapped(path string, limit int) error {
	err := fsutil.CheckFileCapped(path, limit)
	if errors.Is(err, fsutil.ErrTooLarge) {
		return fmt.Errorf("%w: %w", errStreamTooLarge, err)
	}
	return err
}

// runCappedSubprocess runs cmd with stdout capped at maxStreamBytes through
// subproc.Run and returns its stdout. what names the command ("helm template",
// "kustomize build") and appName the app, for error messages.
func runCappedSubprocess(ctx context.Context, cmd *exec.Cmd, what, appName string) ([]byte, error) {
	stdout, stderr, err := subproc.Run(cmd, maxStreamBytes, subprocessWaitDelay)
	if errors.Is(err, subproc.ErrOutputLimit) {
		return nil, fmt.Errorf("%s output for %q exceeds the %d-byte limit: %w",
			what, appName, maxStreamBytes, subproc.ErrOutputLimit)
	}
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
		return nil, fmt.Errorf("%s for %q exited but a child process kept its output open: %w\nstderr: %s",
			what, appName, err, stderr)
	}
	if err != nil {
		return nil, fmt.Errorf("%s subprocess failed for %q: %w\nstderr: %s",
			what, appName, err, stderr)
	}
	return stdout, nil
}
