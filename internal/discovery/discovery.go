package discovery

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/fsutil"
)

// maxYAMLFileBytes caps how much of each YAML file discovery reads. It matches
// render's 64 MiB stream limit. It is a var so tests can lower it; such tests
// must not be parallel.
var maxYAMLFileBytes = 64 << 20

// fileResult holds everything extracted from a single YAML file during the
// parallel processing phase.
type fileResult struct {
	apps        []DiscoveredApp
	fluxSources map[string]fluxSource // see fluxSourceKey
	pendingFlux []pendingFlux
	rawDir      string
	parseErrors []ParseError
}

// pendingFlux holds a raw Flux HelmRelease or Kustomization document whose
// references cannot be resolved until the source objects across all files are
// known.
type pendingFlux struct {
	kind       string // lowercase
	raw        []byte
	sourceFile string
}

// Discover walks the given paths and returns all discoverable apps.
//
// It finds:
//   - Helm charts (dirs with Chart.yaml)
//   - Kustomize overlays (dirs with kustomization.yaml)
//   - ArgoCD Application CRDs
//   - Flux HelmRelease / Kustomization CRDs
//   - Raw YAML directories (directories with .yaml files that match none of the above)
func Discover(ctx context.Context, paths []string, cfg *config.Config) ([]DiscoveredApp, []ParseError, error) {
	var apps []DiscoveredApp
	var parseErrors []ParseError
	// Keys are RootPaths claimed by an app, plus gitopsKey values.
	seen := map[string]bool{}
	var filesScanned int

	for _, root := range paths {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, nil, err
		}

		repoRoot := findRepoRoot(abs)
		overrideMap := buildOverrideMap(cfg, repoRoot)
		repoURLs := loadRepoURLs(repoRoot, cfg.Discovery.RepoAliases)

		// Phase 1: Walk the directory tree.
		// Directory-level detection (Helm charts, Kustomize overlays) must remain
		// synchronous so we can return filepath.SkipDir at the right moments.
		// YAML file paths are just collected here for parallel processing below.
		var yamlFiles []string
		skipDirs := map[string]bool{}
		// Added after GitOps apps so a GitOps app claiming the same directory
		// takes precedence and keeps its values, namespace and source.
		var plainApps []DiscoveredApp

		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				slog.Warn("skipping unreadable path", "path", path, "err", err)
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}

			for skip := range skipDirs {
				if strings.HasPrefix(path, skip+string(filepath.Separator)) {
					return nil
				}
			}

			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && path != abs {
					return filepath.SkipDir
				}
				if isExcluded(path, cfg.Discovery.Exclude) {
					return filepath.SkipDir
				}
				if isHelmChart(path) {
					plainApps = append(plainApps, buildHelmApp(path, overrideMap))
					skipDirs[path] = true
					return filepath.SkipDir
				}
				if isKustomizeOverlay(path) {
					plainApps = append(plainApps, buildKustomizeApp(path, overrideMap))
				}
				return nil
			}

			if !isYAMLFile(d.Name()) {
				return nil
			}
			if isExcluded(path, cfg.Discovery.Exclude) {
				return nil
			}
			filesScanned++
			yamlFiles = append(yamlFiles, path)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}

		// Phase 2: Parse all YAML files concurrently.
		fileResults := parseFilesParallel(ctx, yamlFiles, repoRoot, repoURLs)

		// Phase 3: Index the Flux source objects from all parsed files.
		res := fluxResolver{sources: map[string]fluxSource{}, repoURLs: repoURLs}
		for i := range fileResults {
			maps.Copy(res.sources, fileResults[i].fluxSources)
		}

		addGitOps := func(app DiscoveredApp) {
			key := gitopsKey(app)
			if seen[key] {
				return
			}
			seen[key] = true
			if app.RootPath != "" {
				seen[app.RootPath] = true
			}
			apps = append(apps, app)
		}

		// Phase 4: Merge GitOps apps, resolving Flux references now that all
		// source objects are known.
		for i := range fileResults {
			fr := &fileResults[i]

			parseErrors = append(parseErrors, fr.parseErrors...)

			for _, app := range fr.apps {
				addGitOps(app)
			}

			for _, pf := range fr.pendingFlux {
				var app *DiscoveredApp
				var err error
				label := "Flux HelmRelease"
				if pf.kind == "kustomization" {
					label = "Flux Kustomization"
					app, err = parseFluxKustomization(pf.raw, repoRoot, pf.sourceFile, res)
				} else {
					app, err = parseFluxHelmRelease(pf.raw, repoRoot, pf.sourceFile, res)
				}
				if err != nil {
					slog.Error("failed to parse "+label, "file", pf.sourceFile, "err", err)
					parseErrors = append(parseErrors, ParseError{FilePath: pf.sourceFile, Message: err.Error()})
					continue
				}
				if app != nil {
					addGitOps(*app)
				}
			}
		}

		// Plain Helm charts and Kustomize overlays not claimed by a GitOps app.
		for _, app := range plainApps {
			if !seen[app.RootPath] {
				seen[app.RootPath] = true
				apps = append(apps, app)
			}
		}

		// Phase 5: Add raw YAML apps for directories that had plain manifests
		// and weren't already claimed as a Helm chart or Kustomize overlay.
		// Directories below a recursive app are already rendered by it.
		var recursiveRoots []string
		for _, app := range apps {
			if app.Recurse {
				recursiveRoots = append(recursiveRoots, app.RootPath)
			}
		}
		for i := range fileResults {
			dir := fileResults[i].rawDir
			if dir == "" || seen[dir] || isBelowAny(dir, recursiveRoots) {
				continue
			}
			seen[dir] = true
			apps = append(apps, buildRawApp(dir, overrideMap))
		}
	}

	slog.Info("discovery complete", "files", filesScanned, "apps", len(apps), "parseErrors", len(parseErrors))
	return apps, parseErrors, nil
}

// parseFilesParallel reads and parses all YAML files concurrently using a
// worker pool and returns results in file order.
func parseFilesParallel(ctx context.Context, files []string, repoRoot string, repoURLs map[string]bool) []fileResult {
	if len(files) == 0 {
		return nil
	}

	results := make([]fileResult, len(files))
	workers := min(runtime.NumCPU(), len(files))

	type job struct {
		idx  int
		path string
	}

	jobs := make(chan job, len(files))
	var wg sync.WaitGroup

	for range workers {
		wg.Go(func() {
			for j := range jobs {
				results[j.idx] = parseFile(ctx, j.path, repoRoot, repoURLs)
			}
		})
	}

	for i, f := range files {
		jobs <- job{idx: i, path: f}
	}
	close(jobs)
	wg.Wait()

	return results
}

// parseFile reads a single YAML file and extracts all GitOps CRDs from it.
// HelmRelease documents are returned as pendingReleases because their sourceRef
// resolution requires the complete HelmRepository map, which is only available
// after all files have been processed.
func parseFile(ctx context.Context, path, repoRoot string, repoURLs map[string]bool) fileResult {
	if ctx.Err() != nil {
		return fileResult{}
	}

	data, err := fsutil.ReadFileCapped(path, maxYAMLFileBytes)
	if err != nil {
		switch {
		case errors.Is(err, fsutil.ErrNotRegular):
			slog.Warn("skipping non-regular file", "file", path, "err", err)
		case errors.Is(err, fsutil.ErrTooLarge):
			slog.Warn("skipping file over the size limit", "file", path, "err", err)
		default:
			slog.Warn("skipping file with parse errors", "file", path, "err", err)
		}
		return fileResult{}
	}

	var result fileResult
	// Always mark the directory as a raw candidate so CRD files themselves get
	// linted, not just the charts they reference.
	result.rawDir = filepath.Dir(path)

	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			break
		}
		if node.Kind == 0 {
			continue
		}

		apiVersion := nodeField(&node, "apiVersion")
		kind := nodeField(&node, "kind")
		framework := detectFramework(apiVersion, kind)
		if framework == FrameworkPlain {
			continue
		}

		// Only serialize to bytes now that we know this document is a CRD we handle.
		docBytes, err := yaml.Marshal(&node)
		if err != nil {
			continue
		}

		switch framework {
		case FrameworkArgoCD:
			if strings.ToLower(kind) == "applicationset" {
				continue
			}
			app, err := parseArgoCDApplication(docBytes, repoRoot, path, repoURLs)
			if err != nil {
				slog.Error("failed to parse ArgoCD Application", "file", path, "err", err)
				result.parseErrors = append(result.parseErrors, ParseError{FilePath: path, Message: err.Error()})
				continue
			}
			if app == nil {
				continue
			}
			result.apps = append(result.apps, *app)

		case FrameworkFlux:
			switch k := strings.ToLower(kind); k {
			case "helmrepository", "gitrepository", "ocirepository", "helmchart":
				key, src, err := parseFluxSource(docBytes)
				if err != nil || key == "" {
					continue
				}
				if result.fluxSources == nil {
					result.fluxSources = make(map[string]fluxSource)
				}
				result.fluxSources[key] = src

			case "helmrelease", "kustomization":
				result.pendingFlux = append(result.pendingFlux, pendingFlux{
					kind:       k,
					raw:        docBytes,
					sourceFile: path,
				})
			}
		}
	}

	return result
}

// buildHelmApp constructs a DiscoveredApp for a Helm chart directory.
func buildHelmApp(dir string, overrides map[string]config.DiscoveryOverride) DiscoveredApp {
	app := DiscoveredApp{
		Name:      filepath.Base(dir),
		Framework: FrameworkPlain,
		Renderer:  RendererHelm,
		RootPath:  dir,
	}
	app.ReleaseName = app.Name
	app.ValuesFiles = discoverValuesFiles(dir)

	if o, ok := overrides[dir]; ok {
		applyOverride(&app, o)
	}
	return app
}

// buildKustomizeApp constructs a DiscoveredApp for a Kustomize overlay directory.
func buildKustomizeApp(dir string, overrides map[string]config.DiscoveryOverride) DiscoveredApp {
	app := DiscoveredApp{
		Name:      filepath.Base(dir),
		Framework: FrameworkPlain,
		Renderer:  RendererKustomize,
		RootPath:  dir,
	}
	if o, ok := overrides[dir]; ok {
		applyOverride(&app, o)
	}
	return app
}

// buildRawApp constructs a DiscoveredApp for a plain YAML directory.
func buildRawApp(dir string, overrides map[string]config.DiscoveryOverride) DiscoveredApp {
	app := DiscoveredApp{
		Name:      filepath.Base(dir),
		Framework: FrameworkPlain,
		Renderer:  RendererRaw,
		RootPath:  dir,
	}
	if o, ok := overrides[dir]; ok {
		applyOverride(&app, o)
	}
	return app
}

// applyOverride merges config override settings into an app.
func applyOverride(app *DiscoveredApp, o config.DiscoveryOverride) {
	if o.Renderer != "" {
		app.Renderer = RendererType(o.Renderer)
	}
	if o.Helm.ReleaseName != "" {
		app.ReleaseName = o.Helm.ReleaseName
	}
	if o.Helm.Namespace != "" {
		app.Namespace = o.Helm.Namespace
	}
	if len(o.Helm.ValuesFiles) > 0 {
		app.ValuesFiles = o.Helm.ValuesFiles
	}
	if len(o.Helm.Set) > 0 {
		app.HelmSet = o.Helm.Set
	}
}

// buildOverrideMap indexes config overrides by their absolute path.
func buildOverrideMap(cfg *config.Config, repoRoot string) map[string]config.DiscoveryOverride {
	m := make(map[string]config.DiscoveryOverride, len(cfg.Discovery.Overrides))
	for _, o := range cfg.Discovery.Overrides {
		abs := filepath.Clean(filepath.Join(repoRoot, o.Path))
		m[abs] = o
	}
	return m
}

// gitopsKey identifies a GitOps app by the document that defined it, so that
// several apps rendering the same directory with different settings are kept.
func gitopsKey(app DiscoveredApp) string {
	if app.Source == nil {
		return app.RootPath
	}
	return "gitops:" + app.Source.Path + "#" + app.Name + "#" + app.RootPath + "#" + app.ChartName
}

// isBelowAny reports whether dir is strictly inside one of roots.
func isBelowAny(dir string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(dir, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// findRepoRoot walks upward from dir looking for a .git directory.
// Falls back to dir itself if no git root is found.
func findRepoRoot(dir string) string {
	current := dir
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir // filesystem root reached
		}
		current = parent
	}
}

// nodeField extracts a top-level string field from a yaml.Node without
// re-serializing the document. This avoids a Marshal+Unmarshal roundtrip
// when all we need is a couple of scalar values (apiVersion, kind).
func nodeField(n *yaml.Node, key string) string {
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) > 0 {
			return nodeField(n.Content[0], key)
		}
		return ""
	}
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1].Value
		}
	}
	return ""
}

// isYAMLFile reports whether filename has a .yaml or .yml extension.
func isYAMLFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
