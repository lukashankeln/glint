package render

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

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
	stdout := &cappedBuffer{limit: maxStreamBytes}
	stderr := &truncatingWriter{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = subprocessWaitDelay

	runErr := cmd.Run()
	// Check the cap first, whatever Run returned: on a writer error os/exec
	// closes the pipe, the child dies of SIGPIPE and Run reports "signal:
	// broken pipe"; Wait only returns the copy error when the child exited 0.
	// A child that ignored SIGPIPE would keep running until ctx is cancelled;
	// helm, a Go program, dies on SIGPIPE on stdout.
	if stdout.exceeded {
		return nil, fmt.Errorf("helm template output for %q exceeds the %d-byte limit: %w",
			app.Name, stdout.limit, errOutputLimit)
	}
	if errors.Is(runErr, exec.ErrWaitDelay) && ctx.Err() == nil {
		return nil, fmt.Errorf("helm template for %q exited but a child process kept its output open: %w\nstderr: %s",
			app.Name, runErr, stderr.String())
	}
	if err := runErr; err != nil {
		return nil, fmt.Errorf("helm template subprocess failed for %q: %w\nstderr: %s",
			app.Name, err, stderr.String())
	}

	return SplitYAML(stdout.Bytes(), app.Name, "<rendered>", true)
}
