package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	chartutil "helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	helmenv "helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	ri "helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/registry"
	"helm.sh/helm/v4/pkg/repo/v1"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/fsutil"
	"github.com/lukashankeln/glint/internal/manifest"
)

// HelmRenderer renders Helm charts using the Helm SDK.
type HelmRenderer struct {
	cfg *config.Config
	// sdk is the in-process render; a field so tests can substitute a blocking one.
	sdk func(context.Context, discovery.DiscoveredApp) ([]manifest.Manifest, error)
}

// newHelmRenderer creates a HelmRenderer.
func newHelmRenderer(cfg *config.Config) *HelmRenderer {
	r := &HelmRenderer{cfg: cfg}
	r.sdk = r.renderSDK
	return r
}

// Render renders the given app with Helm and returns manifests.
func (r *HelmRenderer) Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	tctx, cancel, d, err := withRenderTimeout(ctx, r.cfg.Render.Helm.Timeout)
	if err != nil {
		return nil, fmt.Errorf("render.helm.timeout: %w", err)
	}
	defer cancel()

	if tctx.Err() != nil {
		return nil, wrapRenderTimeout(ctx, tctx, "helm", app.Name, d, tctx.Err())
	}

	var manifests []manifest.Manifest
	if r.cfg.Render.SubprocessFallback {
		manifests, err = renderHelmSubprocess(tctx, app, r.cfg.Render.Helm.APIVersions)
	} else {
		manifests, err = runWithContext(tctx, func() ([]manifest.Manifest, error) {
			return r.sdk(tctx, app)
		})
	}
	if err != nil {
		return nil, wrapRenderTimeout(ctx, tctx, "helm", app.Name, d, err)
	}
	return manifests, nil
}

func (r *HelmRenderer) renderSDK(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	settings := helmenv.New()

	actionCfg := action.NewConfiguration()

	// Always wire up an OCI registry client so oci:// chart URLs work.
	registryClient, err := registry.NewClient(registry.ClientOptCredentialsFile(settings.RegistryConfig))
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

	// Advertise extra API versions so charts that gate resources on
	// .Capabilities.APIVersions.Has (e.g. CRDs installed by other apps, such
	// as monitoring.coreos.com/v1 for ServiceMonitors) render without error.
	if len(r.cfg.Render.Helm.APIVersions) > 0 {
		install.APIVersions = append(install.APIVersions, r.cfg.Render.Helm.APIVersions...)
	}

	var ch chart.Charter
	if app.RootPath != "" {
		// Local chart
		if err := checkLocalChart(app.RootPath); err != nil {
			return nil, err
		}
		var err error
		ch, err = loader.Load(app.RootPath)
		if err != nil {
			return nil, fmt.Errorf("loading chart at %q: %w", app.RootPath, err)
		}
	} else {
		// Remote chart — locate and download.
		repoURL := normalizeRepoURL(app.RepoURL)
		if app.ChartVersion != "" {
			install.ChartPathOptions.Version = app.ChartVersion
		}

		if strings.HasPrefix(repoURL, "oci://") {
			// For OCI registries, LocateChart takes the OCI path when registry.IsOCI(name)
			// is true. Passing the full ref as name (with empty RepoURL) triggers that path;
			// setting RepoURL to an OCI URL instead causes Helm to call
			// FindChartInAuthAndTLSAndPassRepoURL, which fails for OCI.
			chartRef := strings.TrimRight(repoURL, "/") + "/" + app.ChartName
			located, err := install.LocateChart(chartRef, settings)
			if err != nil {
				return nil, fmt.Errorf("locating remote chart %q from %q: %w", app.ChartName, app.RepoURL, err)
			}
			ch, err = loader.Load(located)
			if err != nil {
				return nil, fmt.Errorf("loading chart at %q: %w", located, err)
			}
		} else {
			var err error
			ch, err = loadRemoteChart(ctx, repoURL, app.ChartName, app.ChartVersion, settings)
			if err != nil {
				return nil, fmt.Errorf("locating remote chart %q from %q: %w", app.ChartName, app.RepoURL, err)
			}
		}
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

// loadRemoteChart resolves and downloads a chart from a plain HTTP(S) chart
// repository, bypassing Helm's own LocateChart/ChartDownloader resolution.
//
// Helm's own path re-downloads the target repo's entire index.yaml fresh on
// every call (repo.FindChartInRepoURL, which discards the parsed result right
// after use instead of caching it) and then, purely to pick up any per-repo
// TLS/auth config, re-parses every OTHER locally cached repo index hunting for
// the resolved chart URL's owner (ChartDownloader.scanReposForURL — its own
// doc comment calls this "far from optimal"). Neither step depends on which
// app or which chart within the repo is being resolved, so doing them once
// per app instead of once per distinct repo URL turns a handful of MB of
// index parsing into hundreds of MB once more than a couple of apps share a
// repo (e.g. bitnami's ~26MB index.yaml). glint has no per-repo TLS/auth
// config of its own to apply, so skipping scanReposForURL costs nothing here.
//
// loadRepoIndexCached fetches and parses a given repo's index at most once
// per process; only the final chart content fetch below runs per app.
func loadRemoteChart(ctx context.Context, repoURL, name, version string, settings *helmenv.EnvSettings) (chart.Charter, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	getters := getter.All(settings)

	idx, err := loadRepoIndexCached(repoURL, getters)
	if err != nil {
		return nil, err
	}

	cv, err := idx.Get(name, version)
	if err != nil {
		return nil, fmt.Errorf("chart %q not found in %q index: %w", name, repoURL, err)
	}
	if len(cv.URLs) == 0 {
		return nil, fmt.Errorf("chart %q in %q has no downloadable URLs", name, repoURL)
	}

	chartURL, err := repo.ResolveReferenceURL(repoURL, cv.URLs[0])
	if err != nil {
		return nil, fmt.Errorf("resolving chart URL for %q: %w", name, err)
	}

	data, err := fetchChartArchive(chartURL, cv.Digest, getters, settings.ContentCache)
	if err != nil {
		return nil, err
	}
	return loader.LoadArchive(data)
}

// fetchChartArchive downloads a chart archive, reusing Helm's own
// content-addressed disk cache (keyed by the index entry's digest) the same
// way ChartDownloader.DownloadToCache does, so a chart already fetched by a
// plain `helm` invocation — or an earlier glint run — isn't re-downloaded.
func fetchChartArchive(chartURL, digestString string, getters getter.Providers, contentCacheRoot string) (io.Reader, error) {
	u, err := url.Parse(chartURL)
	if err != nil {
		return nil, fmt.Errorf("invalid chart URL %q: %w", chartURL, err)
	}
	g, err := getters.ByScheme(u.Scheme)
	if err != nil {
		return nil, fmt.Errorf("no getter for scheme %q: %w", u.Scheme, err)
	}

	cache := &downloader.DiskCache{Root: contentCacheRoot}

	var digest [32]byte // zero if the index had no usable digest; filled in below from the download instead
	if raw := digestString; raw != "" {
		if _, after, ok := strings.Cut(raw, ":"); ok {
			raw = after
		}
		if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
			copy(digest[:], b)
			if pth, err := cache.Get(digest, downloader.CacheChart); err == nil {
				if cached, err := os.ReadFile(pth); err == nil {
					return bytes.NewReader(cached), nil
				}
			}
		}
	}

	data, err := g.Get(chartURL, getter.WithAcceptHeader("application/gzip,application/octet-stream"))
	if err != nil {
		return nil, fmt.Errorf("downloading chart %q: %w", chartURL, err)
	}

	if digest == ([32]byte{}) {
		digest = sha256.Sum256(data.Bytes())
	}
	if _, err := cache.Put(digest, bytes.NewReader(data.Bytes()), downloader.CacheChart); err != nil {
		slog.Debug("failed to cache downloaded chart", "url", chartURL, "err", err)
	}

	return data, nil
}

// repoIndexCache memoizes parsed Helm chart-repo indexes keyed by repo URL,
// shared by every HelmRenderer in the process (each app gets its own
// HelmRenderer instance, so a renderer-scoped cache would not help). A failed
// fetch is cached too: within one run a repo URL that doesn't resolve won't
// resolve for the next app either, and retrying it per app would reintroduce
// the redundant work this cache exists to avoid. The failure cache also covers
// a failure to create the private temp dir. Each fetch writes the index into
// its own temp dir, which that fetch removes once the index is parsed into
// memory. Because only the fetching goroutine removes the dir, an abandoned
// (timed-out) render can never have its dir deleted underneath it.
var repoIndexCache sync.Map // repoURL string -> *repoIndexEntry

type repoIndexEntry struct {
	once sync.Once
	idx  *repo.IndexFile
	err  error
}

func loadRepoIndexCached(repoURL string, getters getter.Providers) (*repo.IndexFile, error) {
	v, _ := repoIndexCache.LoadOrStore(repoURL, &repoIndexEntry{})
	e := v.(*repoIndexEntry)
	e.once.Do(func() {
		dir, err := os.MkdirTemp("", "glint-helm-repo-index-")
		if err != nil {
			e.err = fmt.Errorf("preparing chart repo cache: %w", err)
			return
		}
		defer os.RemoveAll(dir)

		cr, err := repo.NewChartRepository(&repo.Entry{URL: repoURL, Name: repoCacheName(repoURL)}, getters)
		if err != nil {
			e.err = fmt.Errorf("configuring chart repository %q: %w", repoURL, err)
			return
		}
		cr.CachePath = dir

		idxPath, err := cr.DownloadIndexFile()
		if err != nil {
			e.err = fmt.Errorf("downloading index for %q: %w", repoURL, err)
			return
		}
		e.idx, e.err = repo.LoadIndexFile(idxPath)
	})
	return e.idx, e.err
}

// repoCacheName derives a stable, glint-namespaced cache file name for a repo
// URL so entries never collide with each other or with the user's own named
// Helm repos.
func repoCacheName(repoURL string) string {
	sum := sha256.Sum256([]byte(repoURL))
	return "glint-" + hex.EncodeToString(sum[:8])
}

// buildValues merges all values sources: chart defaults -> values files -> inline -> --set.
func (r *HelmRenderer) buildValues(app discovery.DiscoveredApp) (map[string]any, error) {
	base := make(map[string]any)

	// 1. Load each values file.
	for _, vf := range app.ValuesFiles {
		data, err := readFileCapped(vf, maxStreamBytes)
		if errors.Is(err, errStreamTooLarge) || errors.Is(err, fsutil.ErrNotRegular) {
			return nil, fmt.Errorf("reading values file %q: %w", vf, err)
		}
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

// checkLocalChart rejects a local chart whose Chart.yaml or .helmignore is
// non-regular or over maxStreamBytes, before Helm's loader opens them with an
// unbounded read. Other Stat errors (missing, or root not a directory) are
// ignored: root may lack a Chart.yaml or be a packaged .tgz, which the loader
// handles itself.
func checkLocalChart(root string) error {
	for _, name := range []string{"Chart.yaml", ".helmignore"} {
		err := checkFileCapped(filepath.Join(root, name), maxStreamBytes)
		if errors.Is(err, errStreamTooLarge) || errors.Is(err, fsutil.ErrNotRegular) {
			return fmt.Errorf("loading chart at %q: %s: %w", root, name, err)
		}
	}
	return nil
}
