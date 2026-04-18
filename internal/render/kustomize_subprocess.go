package render

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// renderKustomizeSubprocess renders a Kustomize overlay using the `kustomize build` CLI command.
func renderKustomizeSubprocess(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	cmd := exec.CommandContext(ctx, "kustomize", "build", app.RootPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("kustomize build subprocess failed for %q: %w\nstderr: %s",
			app.Name, err, stderr.String())
	}

	return SplitYAML(stdout.Bytes(), app.Name, "<rendered>", true)
}
