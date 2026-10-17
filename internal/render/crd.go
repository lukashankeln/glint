package render

import (
	"sort"
	"strings"

	"github.com/lukashankeln/glint/internal/manifest"
)

// CRDAPIVersions returns the API versions served by the CustomResourceDefinitions
// in manifests, in both forms Helm's .Capabilities.APIVersions.Has accepts:
// "group/version" and "group/version/Kind". The result is sorted and unique.
func CRDAPIVersions(manifests []manifest.Manifest) []string {
	set := map[string]bool{}
	for _, m := range manifests {
		if m.Kind != "CustomResourceDefinition" || !strings.HasPrefix(m.APIVersion, "apiextensions.k8s.io/") {
			continue
		}
		spec, _ := m.Object["spec"].(map[string]any)
		group, _ := spec["group"].(string)
		if group == "" {
			continue
		}
		names, _ := spec["names"].(map[string]any)
		kind, _ := names["kind"].(string)

		add := func(version string) {
			if version == "" {
				return
			}
			set[group+"/"+version] = true
			if kind != "" {
				set[group+"/"+version+"/"+kind] = true
			}
		}

		versions, _ := spec["versions"].([]any)
		for _, v := range versions {
			vm, _ := v.(map[string]any)
			if served, ok := vm["served"].(bool); ok && !served {
				continue
			}
			name, _ := vm["name"].(string)
			add(name)
		}
		// apiextensions.k8s.io/v1beta1 single-version form.
		version, _ := spec["version"].(string)
		add(version)
	}

	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
