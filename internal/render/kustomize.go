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
}

// newKustomizeRenderer creates a KustomizeRenderer.
func newKustomizeRenderer(cfg *config.Config) *KustomizeRenderer {
	return &KustomizeRenderer{cfg: cfg}
}

// Render renders the given Kustomize app and returns manifests.
func (r *KustomizeRenderer) Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if r.cfg.Render.SubprocessFallback {
		return renderKustomizeSubprocess(ctx, app)
	}

	return r.renderSDK(ctx, app)
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
