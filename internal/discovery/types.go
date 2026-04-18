package discovery

// Framework identifies which GitOps tool manages an application.
type Framework string

const (
	FrameworkArgoCD Framework = "argocd"
	FrameworkFlux   Framework = "flux"
	FrameworkPlain  Framework = "plain"
)

// RendererType identifies how manifests should be produced for an app.
type RendererType string

const (
	RendererHelm      RendererType = "helm"
	RendererKustomize RendererType = "kustomize"
	RendererRaw       RendererType = "raw"
)

// DiscoveredApp is a single renderable unit found by the discovery engine.
type DiscoveredApp struct {
	// Name is derived from the directory name or CRD metadata.name.
	Name string `json:"name"`

	// Framework is the detected GitOps tool.
	Framework Framework `json:"framework"`

	// Renderer determines how manifests are produced.
	Renderer RendererType `json:"renderer"`

	// RootPath is the absolute path to the chart root, overlay directory, or YAML directory.
	RootPath string `json:"root_path"`

	// ValuesFiles lists Helm values files to merge (in priority order, last wins).
	ValuesFiles []string `json:"values_files,omitempty"`

	// HelmSet holds --set overrides from config.
	HelmSet map[string]string `json:"helm_set,omitempty"`

	// ReleaseName is the Helm release name (defaults to Name).
	ReleaseName string `json:"release_name,omitempty"`

	// Namespace is the target namespace for rendering.
	Namespace string `json:"namespace,omitempty"`

	// Source records which file (and CRD name) triggered this discovery entry.
	// Nil for apps discovered purely from filesystem (Chart.yaml / kustomization.yaml).
	Source *SourceRef `json:"source,omitempty"`

	// Remote Helm chart (mutually exclusive with RootPath for Helm renderer)
	RepoURL      string `json:"repo_url,omitempty"`      // Helm repo URL (https:// or oci://)
	ChartName    string `json:"chart_name,omitempty"`    // Chart name for remote repos
	ChartVersion string `json:"chart_version,omitempty"` // Version constraint (e.g. "6.x", ">=1.0.0")

	// InlineValues holds spec.values from a Flux HelmRelease or similar inline values.
	// Merged after ValuesFiles but before HelmSet overrides.
	InlineValues map[string]any `json:"inline_values,omitempty"`
}

// SourceRef records the origin of a discovered app.
type SourceRef struct {
	// Path is the file that contained the ArgoCD Application / Flux CRD.
	Path string `json:"path,omitempty"`
	// Name is the metadata.name of the originating CRD.
	Name string `json:"name,omitempty"`
}

