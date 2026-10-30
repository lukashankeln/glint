package discovery

import (
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// argoApplication is a minimal representation of an ArgoCD Application CRD.
type argoApplication struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Source struct {
			RepoURL        string `json:"repoURL"`
			Path           string `json:"path"`
			Chart          string `json:"chart"`
			TargetRevision string `json:"targetRevision"`
			Helm           struct {
				ReleaseName  string         `json:"releaseName"`
				ValueFiles   []string       `json:"valueFiles"`
				Values       string         `json:"values"`
				ValuesObject map[string]any `json:"valuesObject"`
			} `json:"helm"`
			Directory struct {
				Recurse bool `json:"recurse"`
			} `json:"directory"`
		} `json:"source"`
		Destination struct {
			Namespace string `json:"namespace"`
		} `json:"destination"`
	} `json:"spec"`
}

// parseArgoCDApplication converts an ArgoCD Application document (already
// decoded from YAML) into a DiscoveredApp.
//
// repoRoot is the root of the local repository being scanned. sourceFile is
// the .yaml file that contained the Application manifest. repoURLs holds the
// normalized URLs of the local repository (see loadRepoURLs).
//
// Returns nil if the Application references a remote repo that cannot be
// resolved locally.
func parseArgoCDApplication(raw []byte, repoRoot, sourceFile string, repoURLs map[string]bool) (*DiscoveredApp, error) {
	var app argoApplication
	if err := yaml.Unmarshal(raw, &app); err != nil {
		return nil, fmt.Errorf("parsing ArgoCD Application: %w", err)
	}

	name := app.Metadata.Name
	if name == "" {
		return nil, nil
	}

	repoURL := app.Spec.Source.RepoURL
	isRemote := repoURL != "" && !isLocalRepoURL(repoURL) && !repoURLs[normalizeGitURL(repoURL)]

	chart := app.Spec.Source.Chart
	if chart == "" {
		repoURL, chart = splitOCIChartURL(repoURL, app.Spec.Source.Path)
	}

	// Remote Helm chart (spec.source.chart is set and repoURL is remote)
	if chart != "" && isRemote {
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
			ChartName:    chart,
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
		slog.Info("skipping ArgoCD Application with remote source", "app", name, "repoURL", repoURL, "file", sourceFile)
		return nil, nil
	}

	// Resolve the source path relative to the repo root.
	sourcePath := app.Spec.Source.Path
	var rootPath string
	switch {
	case sourcePath != "":
		p, err := joinInRepo(repoRoot, repoRoot, sourcePath)
		if err != nil {
			return nil, fmt.Errorf("ArgoCD Application %q: spec.source.path %q: %w", name, sourcePath, err)
		}
		rootPath = p
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
		Recurse:   renderer == RendererRaw && app.Spec.Source.Directory.Recurse,
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
			abs, err := joinInRepo(repoRoot, rootPath, vf)
			if err != nil {
				return nil, fmt.Errorf("ArgoCD Application %q: spec.source.helm.valueFiles %q: %w", name, vf, err)
			}
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

// splitOCIChartURL splits an ArgoCD OCI source, where repoURL addresses the
// chart artifact itself (oci://registry/org/chart), into registry path and
// chart name. Sources with a path inside the artifact are left unsplit because
// they are not rendered as a chart root.
func splitOCIChartURL(repoURL, path string) (string, string) {
	if !strings.HasPrefix(strings.ToLower(repoURL), "oci://") || (path != "" && path != ".") {
		return repoURL, ""
	}
	trimmed := strings.TrimRight(repoURL, "/")
	i := strings.LastIndex(trimmed, "/")
	if i < len("oci://") {
		return repoURL, ""
	}
	return trimmed[:i], trimmed[i+1:]
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
