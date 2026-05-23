package discovery

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	"github.com/lukashankeln/glint/internal/config"
)

// minimalDoc is decoded first to determine the kind and apiVersion of a
// YAML document before doing heavier parsing.
type minimalDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
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

		// Find the repository root (used to resolve relative paths in CRDs).
		repoRoot := findRepoRoot(abs)

		// Apply config overrides keyed by path.
		overrideMap := buildOverrideMap(cfg, repoRoot)

		// Pre-scan: collect all HelmRepository objects for two-pass resolution.
		helmRepos := collectHelmRepositories(ctx, abs, cfg)

		// Track directories that contain plain .yaml files (candidates for raw apps).
		rawDirs := map[string]bool{}

		// skipDirs tracks directories we should not recurse into (e.g. Helm chart dirs).
		skipDirs := map[string]bool{}

		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("skipping unreadable path")
				return nil
			}

			if ctx.Err() != nil {
				return ctx.Err()
			}

			// Check if any parent was marked as skip.
			for skip := range skipDirs {
				if strings.HasPrefix(path, skip+string(filepath.Separator)) {
					return nil
				}
			}

			if d.IsDir() {
				// Skip hidden directories and excluded patterns.
				if strings.HasPrefix(d.Name(), ".") && path != abs {
					return filepath.SkipDir
				}
				if isExcluded(path, cfg.Discovery.Exclude) {
					return filepath.SkipDir
				}

				// Helm chart — don't recurse further (subcharts handled by Helm).
				if isHelmChart(path) {
					app := buildHelmApp(path, overrideMap)
					if !seen[app.RootPath] {
						seen[app.RootPath] = true
						apps = append(apps, app)
					}
					skipDirs[path] = true
					return filepath.SkipDir
				}

				// Kustomize overlay — add, but keep recursing for nested overlays.
				if isKustomizeOverlay(path) {
					app := buildKustomizeApp(path, overrideMap)
					if !seen[app.RootPath] {
						seen[app.RootPath] = true
						apps = append(apps, app)
					}
					// Don't return SkipDir — overlays can be nested.
				}

				return nil
			}

			// Only process .yaml / .yml files.
			if !isYAMLFile(d.Name()) {
				return nil
			}
			if isExcluded(path, cfg.Discovery.Exclude) {
				return nil
			}
			filesScanned++

			dir := filepath.Dir(path)

			// Parse the file and inspect each document.
			_, err = processYAMLFile(path, repoRoot, helmRepos, &apps, seen)
			if err != nil {
				log.Warn().Err(err).Str("file", path).Msg("skipping file with parse errors")
				return nil
			}

			// Mark the directory as a raw candidate regardless of whether it
			// contained GitOps CRDs. The seen-check below filters out dirs
			// already claimed as a Helm chart or Kustomize overlay root.
			// This ensures ArgoCD Application / Flux HelmRelease files are
			// themselves linted by CEL rules, not just the charts they reference.
			rawDirs[dir] = true

			return nil
		})
		if err != nil {
			return nil, err
		}

		// Add raw YAML apps for directories that had plain manifests and weren't
		// already claimed as a Helm chart or Kustomize overlay.
		for dir := range rawDirs {
			if !seen[dir] {
				seen[dir] = true
				apps = append(apps, buildRawApp(dir, overrideMap))
			}
		}
	}

	log.Info().Int("files", filesScanned).Int("apps", len(apps)).Msg("discovery complete")
	return apps, nil
}

// collectHelmRepositories does a pre-scan walk to collect all Flux HelmRepository
// objects, returning a map of "namespace/name" -> url.
func collectHelmRepositories(ctx context.Context, root string, cfg *config.Config) map[string]string {
	repos := make(map[string]string)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			if isExcluded(path, cfg.Discovery.Exclude) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isYAMLFile(d.Name()) || isExcluded(path, cfg.Discovery.Exclude) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
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
			if strings.ToLower(md.Kind) == "helmrepository" {
				key, url, err := parseFluxHelmRepository(docBytes)
				if err != nil || key == "" {
					continue
				}
				repos[key] = url
			}
		}
		return nil
	})
	return repos
}

// processYAMLFile parses a YAML file and extracts any ArgoCD/Flux CRDs from it.
// Returns true if at least one CRD document was found.
func processYAMLFile(path, repoRoot string, helmRepos map[string]string, apps *[]DiscoveredApp, seen map[string]bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	crdFound := false
	dec := yaml.NewDecoder(bytes.NewReader(data))

	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			break // EOF or parse error — stop iterating
		}
		if node.Kind == 0 {
			continue
		}

		// Re-encode the single document for targeted parsing.
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
				// ApplicationSets are generator controllers, not renderable apps.
				// Skip creating a DiscoveredApp; the directory will be picked up as raw YAML
				continue
			}
			crdFound = true
			app, err := parseArgoCDApplication(docBytes, repoRoot, path)
			if err != nil {
				log.Warn().Err(err).Str("file", path).Msg("failed to parse ArgoCD Application")
				continue
			}
			if app == nil {
				log.Debug().Str("file", path).Msg("skipping ArgoCD Application with remote repoURL")
				continue
			}
			// Use RepoURL+ChartName as dedup key for remote Helm charts.
			dedupKey := app.RootPath
			if dedupKey == "" {
				dedupKey = app.RepoURL + "/" + app.ChartName + "@" + app.Name
			}
			if !seen[dedupKey] {
				seen[dedupKey] = true
				*apps = append(*apps, *app)
			}

		case FrameworkFlux:
			crdFound = true
			kind := strings.ToLower(md.Kind)
			switch kind {
			case "helmrepository":
				// Already collected in pre-scan; skip here to avoid treating as raw CRD.

			case "helmrelease":
				app, err := parseFluxHelmRelease(docBytes, repoRoot, path, helmRepos)
				if err != nil {
					log.Warn().Err(err).Str("file", path).Msg("failed to parse Flux HelmRelease")
					continue
				}
				if app == nil {
					log.Debug().Str("file", path).Msg("skipping Flux HelmRelease with unresolvable chart")
					continue
				}
				// Use RepoURL as dedup key for remote charts (RootPath is empty).
				dedupKey := app.RootPath
				if dedupKey == "" {
					dedupKey = app.RepoURL + "/" + app.ChartName + "@" + app.Name
				}
				if !seen[dedupKey] {
					seen[dedupKey] = true
					*apps = append(*apps, *app)
				}

			case "kustomization":
				app, err := parseFluxKustomization(docBytes, repoRoot, path)
				if err != nil {
					log.Warn().Err(err).Str("file", path).Msg("failed to parse Flux Kustomization")
					continue
				}
				if app == nil {
					continue
				}
				if !seen[app.RootPath] {
					seen[app.RootPath] = true
					*apps = append(*apps, *app)
				}
			}
		}
	}

	return crdFound, nil
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
