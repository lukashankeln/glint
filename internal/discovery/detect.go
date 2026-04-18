package discovery

import (
	"os"
	"path/filepath"
	"strings"
)

// isHelmChart reports whether dir contains a Chart.yaml file.
func isHelmChart(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "Chart.yaml"))
	return err == nil
}

// isKustomizeOverlay reports whether dir contains a kustomization.yaml file
// (case-insensitive filename, as the Kustomize spec allows both cases).
func isKustomizeOverlay(dir string) bool {
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// detectFramework inspects a parsed YAML document's apiVersion and kind to
// determine whether it belongs to a known GitOps framework.
func detectFramework(apiVersion, kind string) Framework {
	av := strings.ToLower(apiVersion)
	k := strings.ToLower(kind)

	switch {
	case strings.HasPrefix(av, "argoproj.io/") && k == "application":
		return FrameworkArgoCD
	case strings.HasPrefix(av, "argoproj.io/") && k == "applicationset":
		return FrameworkArgoCD
	case strings.HasPrefix(av, "helm.toolkit.fluxcd.io/") && k == "helmrelease":
		return FrameworkFlux
	case strings.HasPrefix(av, "kustomize.toolkit.fluxcd.io/") && k == "kustomization":
		return FrameworkFlux
	case strings.HasPrefix(av, "source.toolkit.fluxcd.io/"):
		return FrameworkFlux
	default:
		return FrameworkPlain
	}
}

// discoverValuesFiles returns Helm values files present in dir, starting with
// values.yaml (if it exists) and any values-*.yaml variants.
func discoverValuesFiles(dir string) []string {
	var files []string

	base := filepath.Join(dir, "values.yaml")
	if _, err := os.Stat(base); err == nil {
		files = append(files, base)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "values.yaml" {
			continue // already added
		}
		if strings.HasPrefix(name, "values-") &&
			(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			files = append(files, filepath.Join(dir, name))
		}
	}
	return files
}
