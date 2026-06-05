package render

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"helm.sh/helm/v4/pkg/action"
	chartutil "helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	helmenv "helm.sh/helm/v4/pkg/cli"
	ri "helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/registry"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// HelmRenderer renders Helm charts using the Helm SDK.
type HelmRenderer struct {
	cfg *config.Config
}

// newHelmRenderer creates a HelmRenderer.
func newHelmRenderer(cfg *config.Config) *HelmRenderer {
	return &HelmRenderer{cfg: cfg}
}

// Render renders the given app with Helm and returns manifests.
func (r *HelmRenderer) Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if r.cfg.Render.SubprocessFallback {
		return renderHelmSubprocess(ctx, app)
	}

	return r.renderSDK(ctx, app)
}

func (r *HelmRenderer) renderSDK(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	settings := helmenv.New()

	actionCfg := action.NewConfiguration()

	// Always wire up an OCI registry client so oci:// chart URLs work.
	registryClient, err := registry.NewClient()
	if err != nil {
		return nil, fmt.Errorf("creating helm registry client: %w", err)
	}
	actionCfg.RegistryClient = registryClient

	install := action.NewInstall(actionCfg)
	install.DryRunStrategy = action.DryRunClient
	install.ReleaseName = releaseName(app)
	install.Namespace = namespace(app)
	install.IncludeCRDs = r.cfg.Render.Helm.IncludeCRDs
	install.UseReleaseName = true

	if r.cfg.Render.Helm.KubernetesVersion != "" {
		kv, err := chartutil.ParseKubeVersion(r.cfg.Render.Helm.KubernetesVersion)
		if err == nil {
			install.KubeVersion = kv
		}
	}

	var chartPath string
	if app.RootPath != "" {
		// Local chart
		chartPath = app.RootPath
	} else {
		// Remote chart — locate and download.
		repoURL := normalizeRepoURL(app.RepoURL)
		if app.ChartVersion != "" {
			install.ChartPathOptions.Version = app.ChartVersion
		}

		var chartRef string
		if strings.HasPrefix(repoURL, "oci://") {
			// For OCI registries, LocateChart takes the OCI path when registry.IsOCI(name)
			// is true. Passing the full ref as name (with empty RepoURL) triggers that path;
			// setting RepoURL to an OCI URL instead causes Helm to call
			// FindChartInAuthAndTLSAndPassRepoURL, which fails for OCI.
			chartRef = strings.TrimRight(repoURL, "/") + "/" + app.ChartName
		} else {
			install.ChartPathOptions.RepoURL = repoURL
			chartRef = app.ChartName
		}

		located, err := install.LocateChart(chartRef, settings)
		if err != nil {
			return nil, fmt.Errorf("locating remote chart %q from %q: %w", app.ChartName, app.RepoURL, err)
		}
		chartPath = located
	}

	ch, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("loading chart at %q: %w", chartPath, err)
	}

	vals, err := r.buildValues(app)
	if err != nil {
		return nil, fmt.Errorf("building values for %q: %w", app.Name, err)
	}

	relResult, err := install.RunWithContext(ctx, ch, vals)
	if err != nil {
		return nil, fmt.Errorf("helm template %q: %w", app.Name, err)
	}

	relAccessor, err := ri.NewAccessor(relResult)
	if err != nil {
		return nil, fmt.Errorf("accessing helm release: %w", err)
	}
	rendered := relAccessor.Manifest()
	if rendered == "" {
		return nil, nil
	}

	return SplitYAML([]byte(rendered), app.Name, "<rendered>", true)
}

// buildValues merges all values sources: chart defaults -> values files -> inline -> --set.
func (r *HelmRenderer) buildValues(app discovery.DiscoveredApp) (map[string]any, error) {
	base := make(map[string]any)

	// 1. Load each values file.
	for _, vf := range app.ValuesFiles {
		data, err := os.ReadFile(vf)
		if err != nil {
			slog.Warn("failed to read values file, skipping", "file", vf, "err", err)
			continue
		}
		vals, err := chartutil.ReadValues(data)
		if err != nil {
			slog.Warn("failed to parse values file, skipping", "file", vf, "err", err)
			continue
		}
		base = mergeValues(base, vals)
	}

	// 2. Inline values (spec.values in HelmRelease).
	if len(app.InlineValues) > 0 {
		base = mergeValues(base, app.InlineValues)
	}

	// 3. --set overrides.
	if len(app.HelmSet) > 0 {
		base = mergeValues(base, helmSetToValues(app.HelmSet))
	}

	return base, nil
}

func releaseName(app discovery.DiscoveredApp) string {
	if app.ReleaseName != "" {
		return app.ReleaseName
	}
	return app.Name
}

func namespace(app discovery.DiscoveredApp) string {
	if app.Namespace != "" {
		return app.Namespace
	}
	return "default"
}

// normalizeRepoURL ensures OCI registry URLs have the oci:// scheme prefix.
// ArgoCD and Flux often store bare hostnames (e.g. "ghcr.io/org/repo") while
// Helm's LocateChart requires the explicit oci:// scheme.
func normalizeRepoURL(repoURL string) string {
	for _, scheme := range []string{"oci://", "https://", "http://", "file://"} {
		if strings.HasPrefix(repoURL, scheme) {
			return repoURL
		}
	}
	// Bare hostname — treat as OCI registry.
	return "oci://" + repoURL
}
