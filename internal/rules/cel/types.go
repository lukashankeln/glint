package cel

import (
	"github.com/lukashankeln/glint/internal/manifest"
)

// ManifestToVars extracts the CEL variable bindings from a manifest.
// labels and annotations default to empty maps to prevent nil-map CEL errors.
func ManifestToVars(m manifest.Manifest) map[string]any {
	labels := map[string]string{}
	annotations := map[string]string{}

	if meta, ok := m.Object["metadata"].(map[string]any); ok {
		if l, ok := meta["labels"].(map[string]any); ok {
			for k, v := range l {
				if s, ok := v.(string); ok {
					labels[k] = s
				}
			}
		}
		if a, ok := meta["annotations"].(map[string]any); ok {
			for k, v := range a {
				if s, ok := v.(string); ok {
					annotations[k] = s
				}
			}
		}
	}

	return map[string]any{
		"resource":    m.Object,
		"name":        m.Name,
		"namespace":   m.Namespace,
		"labels":      labels,
		"annotations": annotations,
		"kind":        m.Kind,
		"apiVersion":  m.APIVersion,
	}
}
