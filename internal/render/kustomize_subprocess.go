package render

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// renderKustomizeSubprocess renders a Kustomize overlay using the `kustomize build` CLI command.
func renderKustomizeSubprocess(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	cmd := execCommandContext(ctx, "kustomize", "build", app.RootPath)
	stdout := &cappedBuffer{limit: maxStreamBytes}
	stderr := &truncatingWriter{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = subprocessWaitDelay

	runErr := cmd.Run()
	// Cap check first: see renderHelmSubprocess.
	if stdout.exceeded {
		return nil, fmt.Errorf("kustomize build output for %q exceeds the %d-byte limit: %w",
			app.Name, stdout.limit, errOutputLimit)
	}
	if errors.Is(runErr, exec.ErrWaitDelay) && ctx.Err() == nil {
		return nil, fmt.Errorf("kustomize build for %q exited but a child process kept its output open: %w\nstderr: %s",
			app.Name, runErr, stderr.String())
	}
	if err := runErr; err != nil {
		return nil, fmt.Errorf("kustomize build subprocess failed for %q: %w\nstderr: %s",
			app.Name, err, stderr.String())
	}

	return SplitYAML(stdout.Bytes(), app.Name, "<rendered>", true)
}
