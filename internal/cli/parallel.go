package cli

import (
	"context"
	"runtime"
	"sync"

	"github.com/rs/zerolog/log"

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

// renderAppsParallel renders all apps concurrently using a worker pool and
// returns results in the original app order.
func renderAppsParallel(ctx context.Context, apps []discovery.DiscoveredApp, cfg *config.Config) []renderResult {
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
				log.Debug().Str("app", app.Name).Str("renderer", string(app.Renderer)).Msg("rendering app")
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
