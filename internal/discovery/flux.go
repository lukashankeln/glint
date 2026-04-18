package discovery

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// fluxHelmRepository is a minimal representation of a Flux HelmRepository CRD.
type fluxHelmRepository struct {
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		URL string `yaml:"url"`
	} `yaml:"spec"`
}

// fluxHelmRelease is a minimal representation of a Flux HelmRelease CRD.
type fluxHelmRelease struct {
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Chart struct {
			Spec struct {
				Chart     string `yaml:"chart"`   // local path or chart name
				Version   string `yaml:"version"` // chart version (for remote)
				SourceRef struct {
					Kind      string `yaml:"kind"`
					Name      string `yaml:"name"`
					Namespace string `yaml:"namespace"`
				} `yaml:"sourceRef"`
			} `yaml:"spec"`
		} `yaml:"chart"`
		TargetNamespace string         `yaml:"targetNamespace"`
		Values          map[string]any `yaml:"values"`
	} `yaml:"spec"`
}

// fluxKustomization is a minimal representation of a Flux Kustomization CRD.
type fluxKustomization struct {
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Path            string `yaml:"path"`
		TargetNamespace string `yaml:"targetNamespace"`
	} `yaml:"spec"`
}

// parseFluxHelmRepository extracts the key ("namespace/name") and URL from a
// Flux HelmRepository document. Returns ("", "", nil) if not a valid HelmRepository.
func parseFluxHelmRepository(raw []byte) (key string, url string, err error) {
	var repo fluxHelmRepository
	if err := yaml.Unmarshal(raw, &repo); err != nil {
		return "", "", fmt.Errorf("parsing Flux HelmRepository: %w", err)
	}
	if repo.Metadata.Name == "" || repo.Spec.URL == "" {
		return "", "", nil
	}
	ns := repo.Metadata.Namespace
	if ns == "" {
		ns = "default"
	}
	return ns + "/" + repo.Metadata.Name, repo.Spec.URL, nil
}

// parseFluxHelmRelease converts a Flux HelmRelease document into a DiscoveredApp.
//
// repoRoot is the local repository root. sourceFile is the .yaml file that
// contained the HelmRelease manifest.
// helmRepos maps "namespace/name" -> repo URL for HelmRepository resolution.
func parseFluxHelmRelease(raw []byte, repoRoot, sourceFile string, helmRepos map[string]string) (*DiscoveredApp, error) {
	var hr fluxHelmRelease
	if err := yaml.Unmarshal(raw, &hr); err != nil {
		return nil, fmt.Errorf("parsing Flux HelmRelease: %w", err)
	}

	name := hr.Metadata.Name
	if name == "" {
		return nil, nil
	}

	chartRef := hr.Spec.Chart.Spec.Chart
	if chartRef == "" {
		return nil, nil
	}

	namespace := hr.Spec.TargetNamespace
	if namespace == "" {
		namespace = hr.Metadata.Namespace
	}

	var inlineValues map[string]any
	if len(hr.Spec.Values) > 0 {
		inlineValues = hr.Spec.Values
	}

	// Local chart path
	if isLocalChartPath(chartRef) {
		rootPath := filepath.Clean(filepath.Join(repoRoot, chartRef))
		app := &DiscoveredApp{
			Name:         name,
			Framework:    FrameworkFlux,
			Renderer:     RendererHelm,
			RootPath:     rootPath,
			Namespace:    namespace,
			ReleaseName:  name,
			InlineValues: inlineValues,
			Source: &SourceRef{
				Path: sourceFile,
				Name: name,
			},
		}
		app.ValuesFiles = discoverValuesFiles(rootPath)
		return app, nil
	}

	// Remote chart — try to resolve via HelmRepository sourceRef
	sourceRef := hr.Spec.Chart.Spec.SourceRef
	if strings.ToLower(sourceRef.Kind) != "helmrepository" || sourceRef.Name == "" {
		return nil, nil // can't resolve
	}

	// Determine the namespace to look up the HelmRepository
	repoNS := sourceRef.Namespace
	if repoNS == "" {
		repoNS = hr.Metadata.Namespace
	}
	if repoNS == "" {
		repoNS = "default"
	}
	repoKey := repoNS + "/" + sourceRef.Name

	repoURL, ok := helmRepos[repoKey]
	if !ok {
		// HelmRepository not found in pre-scan — skip
		return nil, nil
	}

	return &DiscoveredApp{
		Name:         name,
		Framework:    FrameworkFlux,
		Renderer:     RendererHelm,
		RootPath:     "",
		RepoURL:      repoURL,
		ChartName:    chartRef,
		ChartVersion: hr.Spec.Chart.Spec.Version,
		Namespace:    namespace,
		ReleaseName:  name,
		InlineValues: inlineValues,
		Source: &SourceRef{
			Path: sourceFile,
			Name: name,
		},
	}, nil
}

// parseFluxKustomization converts a Flux Kustomization document into a DiscoveredApp.
func parseFluxKustomization(raw []byte, repoRoot, sourceFile string) (*DiscoveredApp, error) {
	var ks fluxKustomization
	if err := yaml.Unmarshal(raw, &ks); err != nil {
		return nil, fmt.Errorf("parsing Flux Kustomization: %w", err)
	}

	name := ks.Metadata.Name
	if name == "" {
		return nil, nil
	}

	path := ks.Spec.Path
	if path == "" {
		path = "."
	}

	rootPath := filepath.Clean(filepath.Join(repoRoot, path))
	namespace := ks.Spec.TargetNamespace
	if namespace == "" {
		namespace = ks.Metadata.Namespace
	}

	return &DiscoveredApp{
		Name:      name,
		Framework: FrameworkFlux,
		Renderer:  RendererKustomize,
		RootPath:  rootPath,
		Namespace: namespace,
		Source: &SourceRef{
			Path: sourceFile,
			Name: name,
		},
	}, nil
}

// isLocalChartPath reports whether ref looks like a local filesystem path
// rather than a chart name from a Helm repository.
func isLocalChartPath(ref string) bool {
	return strings.HasPrefix(ref, "./") ||
		strings.HasPrefix(ref, "../") ||
		strings.HasPrefix(ref, "/")
}
