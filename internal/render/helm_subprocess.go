package render

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// renderHelmSubprocess renders a Helm chart using the `helm template` CLI command.
func renderHelmSubprocess(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	ns := namespace(app)
	release := releaseName(app)

	chartRef := app.RootPath
	if chartRef == "" {
		chartRef = app.ChartName
	}

	args := []string{
		"template",
		release,
		chartRef,
		"--namespace", ns,
		"--include-crds",
	}

	if app.RepoURL != "" {
		args = append(args, "--repo", app.RepoURL)
	}
	if app.ChartVersion != "" {
		args = append(args, "--version", app.ChartVersion)
	}

	for _, vf := range app.ValuesFiles {
		args = append(args, "--values", vf)
	}
	for k, v := range app.HelmSet {
		args = append(args, "--set", k+"="+v)
	}

	cmd := exec.CommandContext(ctx, "helm", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("helm template subprocess failed for %q: %w\nstderr: %s",
			app.Name, err, stderr.String())
	}

	return SplitYAML(stdout.Bytes(), app.Name, "<rendered>", true)
}
