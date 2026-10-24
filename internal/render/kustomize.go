package render

import (
	"context"
	"fmt"

	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// KustomizeRenderer renders Kustomize overlays using the krusty SDK.
type KustomizeRenderer struct {
	cfg *config.Config
	// sdk is the in-process render; a field so tests can substitute a blocking one.
	sdk func(context.Context, discovery.DiscoveredApp) ([]manifest.Manifest, error)
}

// newKustomizeRenderer creates a KustomizeRenderer.
func newKustomizeRenderer(cfg *config.Config) *KustomizeRenderer {
	r := &KustomizeRenderer{cfg: cfg}
	r.sdk = r.renderSDK
	return r
}

// Render renders the given Kustomize app and returns manifests.
func (r *KustomizeRenderer) Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	tctx, cancel, d, err := withRenderTimeout(ctx, r.cfg.Render.Kustomize.Timeout)
	if err != nil {
		return nil, fmt.Errorf("render.kustomize.timeout: %w", err)
	}
	defer cancel()

	if tctx.Err() != nil {
		return nil, wrapRenderTimeout(ctx, tctx, "kustomize", app.Name, d, tctx.Err())
	}

	var manifests []manifest.Manifest
	if r.cfg.Render.SubprocessFallback {
		manifests, err = renderKustomizeSubprocess(tctx, app)
	} else {
		manifests, err = runWithContext(tctx, func() ([]manifest.Manifest, error) {
			return r.sdk(tctx, app)
		})
	}
	if err != nil {
		return nil, wrapRenderTimeout(ctx, tctx, "kustomize", app.Name, d, err)
	}
	return manifests, nil
}

func (r *KustomizeRenderer) renderSDK(_ context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	fSys := filesys.MakeFsOnDisk()

	opts := krusty.MakeDefaultOptions()
	opts.LoadRestrictions = types.LoadRestrictionsNone

	k := krusty.MakeKustomizer(opts)
	resMap, err := k.Run(fSys, app.RootPath)
	if err != nil {
		return nil, fmt.Errorf("kustomize build %q: %w", app.RootPath, err)
	}

	yamlBytes, err := resMap.AsYaml()
	if err != nil {
		return nil, fmt.Errorf("kustomize yaml output for %q: %w", app.RootPath, err)
	}

	return SplitYAML(yamlBytes, app.Name, "<rendered>", true)
}
