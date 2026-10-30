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
		manifests, err = renderKustomizeSubprocess(tctx, r.cfg, app)
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

// cappedFS is the on-disk filesystem with ReadFile bounded by maxStreamBytes
// and restricted to regular files, so a kustomization file, resource, patch or
// generator source that is a device, FIFO or huge file fails instead of being
// read whole. Open is deliberately not wrapped: no kustomize build-path code
// calls FileSystem.Open (besides ReadFile it only stats paths). The cap is
// per file; there is no total budget across files.
type cappedFS struct{ filesys.FileSystem }

func (c cappedFS) ReadFile(path string) ([]byte, error) {
	return readFileCapped(path, maxStreamBytes)
}

// kustomizeOptions maps render.kustomize settings onto krusty options. The
// helm plugin config is built by hand instead of types.EnabledPluginConfig so
// that only builtin kustomize plugins load (no exec or Go plugins) while
// helmCharts work. This does not sandbox helm itself: an overlay controls
// helm's config/data home and chart home, so enable_helm is only safe for
// trusted repositories (see docs/configuration.md).
func kustomizeOptions(cfg *config.Config) *krusty.Options {
	opts := krusty.MakeDefaultOptions()
	opts.LoadRestrictions = types.LoadRestrictionsRootOnly
	if cfg.Render.Kustomize.LoadRestrictor == config.LoadRestrictorNone {
		opts.LoadRestrictions = types.LoadRestrictionsNone
	}
	if cfg.Render.Kustomize.EnableHelm {
		opts.PluginConfig = types.MakePluginConfig(
			types.PluginRestrictionsBuiltinsOnly, types.BploUseStaticallyLinked)
		opts.PluginConfig.HelmConfig.Enabled = true
		opts.PluginConfig.HelmConfig.Command = "helm"
	}
	return opts
}

func (r *KustomizeRenderer) renderSDK(_ context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	fSys := cappedFS{filesys.MakeFsOnDisk()}

	k := krusty.MakeKustomizer(kustomizeOptions(r.cfg))
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
