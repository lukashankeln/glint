package manifest

// Manifest is a single parsed Kubernetes resource.
type Manifest struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string

	Raw        []byte         // original YAML bytes for this document
	SourceApp  string         // DiscoveredApp.Name
	SourcePath string         // file path (raw) or "<rendered>" (helm/kustomize)
	Rendered   bool           // true if produced by rendering

	Object map[string]any // full k8s Unstructured .Object
}
