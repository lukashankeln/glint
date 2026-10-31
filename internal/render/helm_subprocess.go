package render

import (
	"context"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// renderHelmSubprocess renders a Helm chart using the `helm template` CLI command.
func renderHelmSubprocess(ctx context.Context, app discovery.DiscoveredApp, apiVersions []string) ([]manifest.Manifest, error) {
	ns := namespace(app)
	release := releaseName(app)

	if app.RootPath != "" {
		if err := checkLocalChart(app.RootPath); err != nil {
			return nil, err
		}
	}

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

	for _, v := range apiVersions {
		args = append(args, "--api-versions", v)
	}
	for _, vf := range app.ValuesFiles {
		args = append(args, "--values", vf)
	}
	for k, v := range app.HelmSet {
		args = append(args, "--set", k+"="+v)
	}

	cmd := execCommandContext(ctx, "helm", args...)
	stdout, err := runCappedSubprocess(ctx, cmd, "helm template", app.Name)
	if err != nil {
		return nil, err
	}

	return SplitYAML(stdout, app.Name, "<rendered>", true)
}
