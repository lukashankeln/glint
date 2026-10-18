package discovery

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// fluxObjectRef is a Flux cross-object reference (sourceRef, chartRef).
type fluxObjectRef struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// fluxChartSpec is the chart template shared by HelmRelease.spec.chart.spec
// and the HelmChart source.
type fluxChartSpec struct {
	Chart     string        `json:"chart"`   // local path or chart name
	Version   string        `json:"version"` // chart version (for remote)
	SourceRef fluxObjectRef `json:"sourceRef"`
}

// fluxSource is a minimal representation of the Flux source kinds glint
// resolves: HelmRepository, GitRepository, OCIRepository and HelmChart.
type fluxSource struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		URL string `json:"url"`
		Ref struct {
			Tag    string `json:"tag"`
			SemVer string `json:"semver"`
		} `json:"ref"`
		fluxChartSpec
	} `json:"spec"`
}

// fluxHelmRelease is a minimal representation of a Flux HelmRelease CRD.
type fluxHelmRelease struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Chart struct {
			Spec fluxChartSpec `json:"spec"`
		} `json:"chart"`
		ChartRef        fluxObjectRef  `json:"chartRef"`
		TargetNamespace string         `json:"targetNamespace"`
		Values          map[string]any `json:"values"`
	} `json:"spec"`
}

// fluxKustomization is a minimal representation of a Flux Kustomization CRD.
type fluxKustomization struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Path            string        `json:"path"`
		TargetNamespace string        `json:"targetNamespace"`
		SourceRef       fluxObjectRef `json:"sourceRef"`
	} `json:"spec"`
}

// fluxResolver resolves Flux references against the source objects found in
// the scanned tree.
type fluxResolver struct {
	sources  map[string]fluxSource // see fluxSourceKey
	repoURLs map[string]bool       // see loadRepoURLs
}

// fluxSourceKey identifies a source object by lowercase kind, namespace and name.
func fluxSourceKey(kind, namespace, name string) string {
	if namespace == "" {
		namespace = "default"
	}
	return strings.ToLower(kind) + "/" + namespace + "/" + name
}

// parseFluxSource decodes a source.toolkit.fluxcd.io document and returns its
// key. Returns "" for documents without a name.
func parseFluxSource(raw []byte) (string, fluxSource, error) {
	var src fluxSource
	if err := yaml.Unmarshal(raw, &src); err != nil {
		return "", src, fmt.Errorf("parsing Flux %s: %w", src.Kind, err)
	}
	if src.Metadata.Name == "" {
		return "", src, nil
	}
	return fluxSourceKey(src.Kind, src.Metadata.Namespace, src.Metadata.Name), src, nil
}

// lookup finds the object ref points to. defaultNS is the namespace of the
// referring object, which Flux uses when ref.Namespace is empty.
func (r fluxResolver) lookup(ref fluxObjectRef, defaultNS string) (fluxSource, bool) {
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNS
	}
	src, ok := r.sources[fluxSourceKey(ref.Kind, ns, ref.Name)]
	return src, ok
}

// isLocalGitSource reports whether a GitRepository ref points at the scanned
// repository. GitRepositories created outside git (e.g. by `flux install`
// plus a separately applied object) cannot be resolved and are assumed local.
// flux-system/flux-system is the self-reference `flux bootstrap` creates, and
// its URL often differs from the local remote (internal host, other scheme).
func (r fluxResolver) isLocalGitSource(ref fluxObjectRef, defaultNS string) (bool, string) {
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNS
	}
	if ns == "flux-system" && ref.Name == "flux-system" {
		return true, ""
	}
	src, ok := r.lookup(ref, defaultNS)
	if !ok {
		slog.Debug("GitRepository not found in scanned paths, assuming this repository", "name", ref.Name, "namespace", ns)
		return true, ""
	}
	if r.repoURLs[normalizeGitURL(src.Spec.URL)] {
		return true, ""
	}
	return false, src.Spec.URL
}

// parseFluxHelmRelease converts a Flux HelmRelease document into a DiscoveredApp.
//
// repoRoot is the local repository root. sourceFile is the .yaml file that
// contained the HelmRelease manifest.
//
// Returns nil if the chart cannot be resolved to a local path or a Helm
// repository.
func parseFluxHelmRelease(raw []byte, repoRoot, sourceFile string, res fluxResolver) (*DiscoveredApp, error) {
	var hr fluxHelmRelease
	if err := yaml.Unmarshal(raw, &hr); err != nil {
		return nil, fmt.Errorf("parsing Flux HelmRelease: %w", err)
	}

	name := hr.Metadata.Name
	if name == "" {
		return nil, nil
	}
	skip := func(reason string, args ...any) {
		slog.Info("skipping Flux HelmRelease", append([]any{"app", name, "reason", reason, "file", sourceFile}, args...)...)
	}

	namespace := hr.Spec.TargetNamespace
	if namespace == "" {
		namespace = hr.Metadata.Namespace
	}

	app := &DiscoveredApp{
		Name:        name,
		Framework:   FrameworkFlux,
		Renderer:    RendererHelm,
		Namespace:   namespace,
		ReleaseName: name,
		Source: &SourceRef{
			Path: sourceFile,
			Name: name,
		},
	}
	if len(hr.Spec.Values) > 0 {
		app.InlineValues = hr.Spec.Values
	}

	spec := hr.Spec.Chart.Spec
	// Namespace that spec.SourceRef defaults to.
	specNS := hr.Metadata.Namespace

	if ref := hr.Spec.ChartRef; ref.Kind != "" {
		src, ok := res.lookup(ref, hr.Metadata.Namespace)
		if !ok {
			skip("chartRef not found in scanned paths", "chartRef", ref.Kind+"/"+ref.Name)
			return nil, nil
		}
		switch strings.ToLower(ref.Kind) {
		case "ocirepository":
			repoURL, chart := splitOCIChartURL(src.Spec.URL, "")
			if chart == "" {
				skip("OCIRepository URL does not name a chart", "url", src.Spec.URL)
				return nil, nil
			}
			app.RepoURL = repoURL
			app.ChartName = chart
			app.ChartVersion = src.Spec.Ref.Tag
			if app.ChartVersion == "" {
				app.ChartVersion = src.Spec.Ref.SemVer
			}
			return app, nil
		case "helmchart":
			spec = src.Spec.fluxChartSpec
			specNS = src.Metadata.Namespace
		default:
			skip("unsupported chartRef kind", "chartRef", ref.Kind+"/"+ref.Name)
			return nil, nil
		}
	}

	if spec.Chart == "" {
		skip("no chart reference")
		return nil, nil
	}

	switch strings.ToLower(spec.SourceRef.Kind) {
	case "helmrepository":
		src, ok := res.lookup(spec.SourceRef, specNS)
		if !ok {
			skip("HelmRepository not found in scanned paths", "sourceRef", spec.SourceRef.Name)
			return nil, nil
		}
		app.RepoURL = src.Spec.URL
		app.ChartName = spec.Chart
		app.ChartVersion = spec.Version
		return app, nil

	case "gitrepository":
		if local, url := res.isLocalGitSource(spec.SourceRef, specNS); !local {
			skip("GitRepository is not this repository", "url", url)
			return nil, nil
		}

	case "":
		if !isLocalChartPath(spec.Chart) {
			skip("chart has no sourceRef")
			return nil, nil
		}

	default:
		skip("unsupported sourceRef kind", "sourceRef", spec.SourceRef.Kind+"/"+spec.SourceRef.Name)
		return nil, nil
	}

	app.RootPath = filepath.Clean(filepath.Join(repoRoot, spec.Chart))
	app.ValuesFiles = discoverValuesFiles(app.RootPath)
	return app, nil
}

// parseFluxKustomization converts a Flux Kustomization document into a
// DiscoveredApp. Returns nil if its source is not this repository.
func parseFluxKustomization(raw []byte, repoRoot, sourceFile string, res fluxResolver) (*DiscoveredApp, error) {
	var ks fluxKustomization
	if err := yaml.Unmarshal(raw, &ks); err != nil {
		return nil, fmt.Errorf("parsing Flux Kustomization: %w", err)
	}

	name := ks.Metadata.Name
	if name == "" {
		return nil, nil
	}

	switch ref := ks.Spec.SourceRef; strings.ToLower(ref.Kind) {
	case "", "gitrepository":
		if local, url := res.isLocalGitSource(ref, ks.Metadata.Namespace); !local {
			slog.Info("skipping Flux Kustomization", "app", name, "reason", "GitRepository is not this repository", "url", url, "file", sourceFile)
			return nil, nil
		}
	default:
		slog.Info("skipping Flux Kustomization", "app", name, "reason", "source is not a git repository", "sourceRef", ref.Kind+"/"+ref.Name, "file", sourceFile)
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

	app := &DiscoveredApp{
		Name:      name,
		Framework: FrameworkFlux,
		Renderer:  RendererKustomize,
		RootPath:  rootPath,
		Namespace: namespace,
		Source: &SourceRef{
			Path: sourceFile,
			Name: name,
		},
	}
	// kustomize-controller generates a kustomization covering all manifests
	// below the path when none exists.
	if !isKustomizeOverlay(rootPath) {
		app.Renderer = RendererRaw
		app.Recurse = true
	}
	return app, nil
}

// isLocalChartPath reports whether ref looks like a local filesystem path
// rather than a chart name from a Helm repository.
func isLocalChartPath(ref string) bool {
	return strings.HasPrefix(ref, "./") ||
		strings.HasPrefix(ref, "../") ||
		strings.HasPrefix(ref, "/")
}
