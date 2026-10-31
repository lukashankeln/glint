package cli

import (
	"context"
	"runtime"
	"sync"

	"log/slog"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/render"
)

type renderResult struct {
	app       discovery.DiscoveredApp
	manifests []manifest.Manifest
	err       error
}

// renderAppsParallel renders all apps concurrently and returns results in the
// original app order.
//
// With render.helm.discover_api_versions, Helm charts are rendered a second
// time with the API versions of all CRDs from the first pass, because charts
// gate resources on .Capabilities.APIVersions and an offline render only
// knows the built-in APIs.
func renderAppsParallel(ctx context.Context, apps []discovery.DiscoveredApp, cfg *config.Config) []renderResult {
	defer render.CleanupHelmRepoCache()

	results := renderAppsOnce(ctx, apps, cfg)
	if !cfg.Render.Helm.DiscoverAPIVersions || ctx.Err() != nil {
		return results
	}

	var all []manifest.Manifest
	for _, r := range results {
		all = append(all, r.manifests...)
	}
	known := map[string]bool{}
	for _, v := range cfg.Render.Helm.APIVersions {
		known[v] = true
	}
	var added []string
	for _, v := range render.CRDAPIVersions(all) {
		if !known[v] {
			added = append(added, v)
		}
	}
	if len(added) == 0 {
		return results
	}

	var helmIdx []int
	var helmApps []discovery.DiscoveredApp
	for i, app := range apps {
		if app.Renderer == discovery.RendererHelm {
			helmIdx = append(helmIdx, i)
			helmApps = append(helmApps, app)
		}
	}
	if len(helmApps) == 0 {
		return results
	}

	slog.Info("re-rendering Helm apps with API versions from discovered CRDs", "apps", len(helmApps), "apiVersions", len(added))
	slog.Debug("discovered CRD API versions", "apiVersions", added)

	extended := *cfg
	extended.Render.Helm.APIVersions = append(append([]string{}, cfg.Render.Helm.APIVersions...), added...)
	for j, r := range renderAppsOnce(ctx, helmApps, &extended) {
		results[helmIdx[j]] = r
	}
	return results
}

// renderAppsOnce renders all apps concurrently using a worker pool and
// returns results in the original app order.
func renderAppsOnce(ctx context.Context, apps []discovery.DiscoveredApp, cfg *config.Config) []renderResult {
	results := make([]renderResult, len(apps))
	if len(apps) == 0 {
		return results
	}

	workers := min(runtime.NumCPU(), len(apps))

	type workResult struct {
		idx       int
		manifests []manifest.Manifest
		err       error
	}

	jobs := make(chan int, len(apps))
	ch := make(chan workResult, len(apps))

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for idx := range jobs {
				app := apps[idx]
				slog.Debug("rendering app", "app", app.Name, "renderer", app.Renderer)
				r := render.New(app, cfg)
				manifests, err := r.Render(ctx, app)
				ch <- workResult{idx: idx, manifests: manifests, err: err}
			}
		})
	}

	for i := range apps {
		jobs <- i
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(ch)
	}()

	for r := range ch {
		results[r.idx] = renderResult{
			app:       apps[r.idx],
			manifests: r.manifests,
			err:       r.err,
		}
	}
	return results
}
