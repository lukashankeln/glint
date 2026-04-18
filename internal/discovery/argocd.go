package discovery

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// argoApplication is a minimal representation of an ArgoCD Application CRD.
type argoApplication struct {
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Source struct {
			RepoURL        string `yaml:"repoURL"`
			Path           string `yaml:"path"`
			Chart          string `yaml:"chart"`
			TargetRevision string `yaml:"targetRevision"`
			Helm           struct {
				ReleaseName  string         `yaml:"releaseName"`
				ValueFiles   []string       `yaml:"valueFiles"`
				Values       string         `yaml:"values"`       // inline YAML string
				ValuesObject map[string]any `yaml:"valuesObject"` // inline YAML object (takes precedence over Values)
			} `yaml:"helm"`
		} `yaml:"source"`
		Destination struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"destination"`
	} `yaml:"spec"`
}

// parseArgoCDApplication converts an ArgoCD Application document (already
// decoded from YAML) into a DiscoveredApp.
//
// repoRoot is the root of the local repository being scanned. sourceFile is
// the .yaml file that contained the Application manifest.
//
// Returns nil if the Application references a remote repo that cannot be
// resolved locally.
func parseArgoCDApplication(raw []byte, repoRoot, sourceFile string) (*DiscoveredApp, error) {
	var app argoApplication
	if err := yaml.Unmarshal(raw, &app); err != nil {
		return nil, fmt.Errorf("parsing ArgoCD Application: %w", err)
	}

	name := app.Metadata.Name
	if name == "" {
		return nil, nil
	}

	repoURL := app.Spec.Source.RepoURL
	isRemote := repoURL != "" && !isLocalRepoURL(repoURL)

	// Remote Helm chart (spec.source.chart is set and repoURL is remote)
	if app.Spec.Source.Chart != "" && isRemote {
		namespace := app.Spec.Destination.Namespace
		releaseName := app.Spec.Source.Helm.ReleaseName
		if releaseName == "" {
			releaseName = name
		}
		return &DiscoveredApp{
			Name:         name,
			Framework:    FrameworkArgoCD,
			Renderer:     RendererHelm,
			RootPath:     "",
			RepoURL:      repoURL,
			ChartName:    app.Spec.Source.Chart,
			ChartVersion: app.Spec.Source.TargetRevision,
			Namespace:    namespace,
			ReleaseName:  releaseName,
			InlineValues: mergeArgoCDHelmValues(app.Spec.Source.Helm.Values, app.Spec.Source.Helm.ValuesObject),
			Source: &SourceRef{
				Path: sourceFile,
				Name: name,
			},
		}, nil
	}

	// Skip Applications pointing to remote repos that aren't Helm charts.
	if isRemote {
		return nil, nil // caller should log this as a skip
	}

	// Resolve the source path relative to the repo root.
	sourcePath := app.Spec.Source.Path
	var rootPath string
	switch {
	case sourcePath != "":
		rootPath = filepath.Clean(filepath.Join(repoRoot, sourcePath))
	case repoRoot != "":
		rootPath = repoRoot
	default:
		rootPath = filepath.Dir(sourceFile)
	}

	renderer := resolveArgoCDRenderer(app, rootPath)
	namespace := app.Spec.Destination.Namespace

	discovered := &DiscoveredApp{
		Name:      name,
		Framework: FrameworkArgoCD,
		Renderer:  renderer,
		RootPath:  rootPath,
		Namespace: namespace,
		Source: &SourceRef{
			Path: sourceFile,
			Name: name,
		},
	}

	if renderer == RendererHelm {
		discovered.ReleaseName = app.Spec.Source.Helm.ReleaseName
		if discovered.ReleaseName == "" {
			discovered.ReleaseName = name
		}
		// Explicit valueFiles from the Application spec
		for _, vf := range app.Spec.Source.Helm.ValueFiles {
			abs := filepath.Clean(filepath.Join(rootPath, vf))
			discovered.ValuesFiles = append(discovered.ValuesFiles, abs)
		}
		// Fall back to discovering values files on disk if none specified
		if len(discovered.ValuesFiles) == 0 {
			discovered.ValuesFiles = discoverValuesFiles(rootPath)
		}
		discovered.InlineValues = mergeArgoCDHelmValues(app.Spec.Source.Helm.Values, app.Spec.Source.Helm.ValuesObject)
	}

	return discovered, nil
}

// resolveArgoCDRenderer determines the renderer for an ArgoCD Application
// by inspecting the spec and the files on disk.
func resolveArgoCDRenderer(app argoApplication, rootPath string) RendererType {
	// Explicit Helm chart reference in spec
	if app.Spec.Source.Chart != "" {
		return RendererHelm
	}
	// Check what's at the resolved path
	if isHelmChart(rootPath) {
		return RendererHelm
	}
	if isKustomizeOverlay(rootPath) {
		return RendererKustomize
	}
	return RendererRaw
}

// mergeArgoCDHelmValues merges spec.source.helm.values (YAML string) and
// spec.source.helm.valuesObject (map) into a single InlineValues map.
// valuesObject takes precedence over values for duplicate keys.
// Returns nil when both inputs are empty.
func mergeArgoCDHelmValues(valuesStr string, valuesObject map[string]any) map[string]any {
	var base map[string]any

	// Parse the inline YAML string first (lower priority).
	if valuesStr != "" {
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(valuesStr), &parsed); err == nil && parsed != nil {
			base = parsed
		}
	}

	// Merge valuesObject on top (higher priority).
	if len(valuesObject) > 0 {
		if base == nil {
			base = make(map[string]any)
		}
		maps.Copy(base, valuesObject)
	}

	return base
}

// isLocalRepoURL returns true when repoURL refers to the local filesystem
// rather than a remote git server.
func isLocalRepoURL(repoURL string) bool {
	if repoURL == "" || repoURL == "." {
		return true
	}
	lower := strings.ToLower(repoURL)
	// Remote indicators
	for _, prefix := range []string{"https://", "http://", "git@", "ssh://", "git://"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	// Looks like a local path
	if strings.HasPrefix(repoURL, "/") || strings.HasPrefix(repoURL, "./") || strings.HasPrefix(repoURL, "../") {
		return true
	}
	// Relative paths without explicit prefix (e.g. "charts/frontend")
	if _, err := os.Stat(repoURL); err == nil {
		return true
	}
	return false
}
