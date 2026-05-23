package discovery

import (
	"bytes"
	"context"
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
)

// minimalDoc is decoded first to determine the kind and apiVersion of a
// YAML document before doing heavier parsing.
type minimalDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// fileResult holds everything extracted from a single YAML file during the
// parallel processing phase.
type fileResult struct {
	apps            []DiscoveredApp
	helmRepos       map[string]string // namespace/name -> url
	pendingReleases []pendingRelease
	rawDir          string
}

// pendingRelease holds a raw Flux HelmRelease document whose sourceRef cannot
// be resolved until all HelmRepository objects across all files are known.
type pendingRelease struct {
	raw        []byte
	repoRoot   string
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
func Discover(ctx context.Context, paths []string, cfg *config.Config) ([]DiscoveredApp, error) {
	var apps []DiscoveredApp
	seen := map[string]bool{} // deduplicate by RootPath
	var filesScanned int

	for _, root := range paths {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}

		repoRoot := findRepoRoot(abs)
		overrideMap := buildOverrideMap(cfg, repoRoot)

		// Phase 1: Walk the directory tree.
		// Directory-level detection (Helm charts, Kustomize overlays) must remain
		// synchronous so we can return filepath.SkipDir at the right moments.
		// YAML file paths are just collected here for parallel processing below.
		var yamlFiles []string
		skipDirs := map[string]bool{}

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
					app := buildHelmApp(path, overrideMap)
					if !seen[app.RootPath] {
						seen[app.RootPath] = true
						apps = append(apps, app)
					}
					skipDirs[path] = true
					return filepath.SkipDir
				}
				if isKustomizeOverlay(path) {
					app := buildKustomizeApp(path, overrideMap)
					if !seen[app.RootPath] {
						seen[app.RootPath] = true
						apps = append(apps, app)
					}
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
			return nil, err
		}

		// Phase 2: Parse all YAML files concurrently.
		fileResults := parseFilesParallel(ctx, yamlFiles, repoRoot)

		// Phase 3: Build the complete HelmRepository map from all parsed files.
		helmRepos := make(map[string]string)
		for i := range fileResults {
			maps.Copy(helmRepos, fileResults[i].helmRepos)
		}

		// Phase 4: Merge results into the app list.
		for i := range fileResults {
			fr := &fileResults[i]

			for _, app := range fr.apps {
				dedupKey := app.RootPath
				if dedupKey == "" {
					dedupKey = app.RepoURL + "/" + app.ChartName + "@" + app.Name
				}
				if !seen[dedupKey] {
					seen[dedupKey] = true
					apps = append(apps, app)
				}
			}

			// Resolve Flux HelmReleases now that the full helmRepos map is available.
			for _, pr := range fr.pendingReleases {
				app, err := parseFluxHelmRelease(pr.raw, pr.repoRoot, pr.sourceFile, helmRepos)
				if err != nil {
					slog.Warn("failed to parse Flux HelmRelease", "file", pr.sourceFile, "err", err)
					continue
				}
				if app == nil {
					slog.Debug("skipping Flux HelmRelease with unresolvable chart", "file", pr.sourceFile)
					continue
				}
				dedupKey := app.RootPath
				if dedupKey == "" {
					dedupKey = app.RepoURL + "/" + app.ChartName + "@" + app.Name
				}
				if !seen[dedupKey] {
					seen[dedupKey] = true
					apps = append(apps, *app)
				}
			}
		}

		// Phase 5: Add raw YAML apps for directories that had plain manifests
		// and weren't already claimed as a Helm chart or Kustomize overlay.
		for i := range fileResults {
			if dir := fileResults[i].rawDir; dir != "" && !seen[dir] {
				seen[dir] = true
				apps = append(apps, buildRawApp(dir, overrideMap))
			}
		}
	}

	slog.Info("discovery complete", "files", filesScanned, "apps", len(apps))
	return apps, nil
}

// parseFilesParallel reads and parses all YAML files concurrently using a
// worker pool and returns results in file order.
func parseFilesParallel(ctx context.Context, files []string, repoRoot string) []fileResult {
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
				results[j.idx] = parseFile(ctx, j.path, repoRoot)
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
func parseFile(ctx context.Context, path, repoRoot string) fileResult {
	if ctx.Err() != nil {
		return fileResult{}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("skipping file with parse errors", "file", path, "err", err)
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

		docBytes, err := yaml.Marshal(&node)
		if err != nil {
			continue
		}

		var md minimalDoc
		if err := yaml.Unmarshal(docBytes, &md); err != nil {
			continue
		}

		framework := detectFramework(md.APIVersion, md.Kind)
		if framework == FrameworkPlain {
			continue
		}

		switch framework {
		case FrameworkArgoCD:
			if strings.ToLower(md.Kind) == "applicationset" {
				continue
			}
			app, err := parseArgoCDApplication(docBytes, repoRoot, path)
			if err != nil {
				slog.Warn("failed to parse ArgoCD Application", "file", path, "err", err)
				continue
			}
			if app == nil {
				slog.Debug("skipping ArgoCD Application with remote repoURL", "file", path)
				continue
			}
			result.apps = append(result.apps, *app)

		case FrameworkFlux:
			kind := strings.ToLower(md.Kind)
			switch kind {
			case "helmrepository":
				key, url, err := parseFluxHelmRepository(docBytes)
				if err != nil || key == "" {
					continue
				}
				if result.helmRepos == nil {
					result.helmRepos = make(map[string]string)
				}
				result.helmRepos[key] = url

			case "helmrelease":
				result.pendingReleases = append(result.pendingReleases, pendingRelease{
					raw:        docBytes,
					repoRoot:   repoRoot,
					sourceFile: path,
				})

			case "kustomization":
				app, err := parseFluxKustomization(docBytes, repoRoot, path)
				if err != nil {
					slog.Warn("failed to parse Flux Kustomization", "file", path, "err", err)
					continue
				}
				if app == nil {
					continue
				}
				result.apps = append(result.apps, *app)
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

// isYAMLFile reports whether filename has a .yaml or .yml extension.
func isYAMLFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
