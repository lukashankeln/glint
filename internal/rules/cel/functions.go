package cel

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"

	"github.com/Masterminds/semver/v3"
)

// ImageTagOption registers imageTag(string) string.
// Extracts the tag from a container image reference.
// "nginx" → "latest", "nginx:1.0" → "1.0", "nginx@sha256:..." → "".
func ImageTagOption() cel.EnvOption {
	return cel.Function("imageTag",
		cel.Overload("imageTag_string",
			[]*cel.Type{cel.StringType},
			cel.StringType,
			cel.UnaryBinding(func(val ref.Val) ref.Val {
				return types.String(extractImageTag(string(val.(types.String))))
			}),
		),
	)
}

// InListOption registers inList(string, list<dyn>) bool.
func InListOption() cel.EnvOption {
	return cel.Function("inList",
		cel.Overload("inList_string_list",
			[]*cel.Type{cel.StringType, cel.ListType(cel.DynType)},
			cel.BoolType,
			cel.BinaryBinding(func(lhs, rhs ref.Val) ref.Val {
				val := string(lhs.(types.String))
				list, ok := rhs.(traits.Lister)
				if !ok {
					return types.False
				}
				it := list.Iterator()
				for it.HasNext() == types.True {
					if s, ok := it.Next().(types.String); ok && string(s) == val {
						return types.True
					}
				}
				return types.False
			}),
		),
	)
}

// MatchesGlobOption registers matchesGlob(string, string) bool.
func MatchesGlobOption() cel.EnvOption {
	return cel.Function("matchesGlob",
		cel.Overload("matchesGlob_string_string",
			[]*cel.Type{cel.StringType, cel.StringType},
			cel.BoolType,
			cel.BinaryBinding(func(lhs, rhs ref.Val) ref.Val {
				str := string(lhs.(types.String))
				pattern := string(rhs.(types.String))
				matched, err := doublestar.Match(pattern, str)
				if err != nil {
					return types.False
				}
				return types.Bool(matched)
			}),
		),
	)
}

// SemverLTOption registers semverLT(string, string) bool.
func SemverLTOption() cel.EnvOption {
	return cel.Function("semverLT",
		cel.Overload("semverLT_string_string",
			[]*cel.Type{cel.StringType, cel.StringType},
			cel.BoolType,
			cel.BinaryBinding(func(lhs, rhs ref.Val) ref.Val {
				a := string(lhs.(types.String))
				b := string(rhs.(types.String))
				va, err := semver.NewVersion(a)
				if err != nil {
					return types.False
				}
				vb, err := semver.NewVersion(b)
				if err != nil {
					return types.False
				}
				return types.Bool(va.LessThan(vb))
			}),
		),
	)
}

// PodSpecOption registers podSpec(dyn) map<string, dyn>.
// Returns the pod spec of a workload: spec.jobTemplate.spec.template.spec
// (CronJob), else spec.template.spec (Deployment etc.), else spec when it has
// a containers key (Pod), else an empty map. Null, absent or wrong-typed
// intermediate values fall through; it never errors.
func PodSpecOption() cel.EnvOption {
	return cel.Function("podSpec",
		cel.Overload("podSpec_dyn",
			[]*cel.Type{cel.DynType},
			cel.MapType(cel.StringType, cel.DynType),
			cel.UnaryBinding(func(val ref.Val) ref.Val {
				return podSpecOf(val)
			}),
		),
	)
}

// PodContainersOption registers podContainers(dyn) list<dyn>.
// Concatenates the containers, initContainers and ephemeralContainers lists of
// podSpec(resource), in that order, skipping absent, null or non-list entries.
func PodContainersOption() cel.EnvOption {
	return cel.Function("podContainers",
		cel.Overload("podContainers_dyn",
			[]*cel.Type{cel.DynType},
			cel.ListType(cel.DynType),
			cel.UnaryBinding(func(val ref.Val) ref.Val {
				spec, _ := podSpecOf(val).(traits.Mapper)
				var elems []ref.Val
				for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
					lst, ok := mapGet(spec, key).(traits.Lister)
					if !ok {
						continue
					}
					for it := lst.Iterator(); it.HasNext() == types.True; {
						elems = append(elems, it.Next())
					}
				}
				return types.NewRefValList(types.DefaultTypeAdapter, elems)
			}),
		),
	)
}

// mapGet returns m[key], or nil when m is nil/not a mapper or key is absent.
func mapGet(m traits.Mapper, key string) ref.Val {
	if m == nil {
		return nil
	}
	v, ok := m.Find(types.String(key))
	if !ok {
		return nil
	}
	return v
}

// podSpecOf resolves the pod spec of a workload resource; see PodSpecOption.
func podSpecOf(resource ref.Val) ref.Val {
	root, _ := resource.(traits.Mapper)
	spec, _ := mapGet(root, "spec").(traits.Mapper)
	if spec != nil {
		if jt, _ := mapGet(spec, "jobTemplate").(traits.Mapper); jt != nil {
			if js, _ := mapGet(jt, "spec").(traits.Mapper); js != nil {
				if tpl, _ := mapGet(js, "template").(traits.Mapper); tpl != nil {
					if ps, _ := mapGet(tpl, "spec").(traits.Mapper); ps != nil {
						return ps
					}
				}
			}
		}
		if tpl, _ := mapGet(spec, "template").(traits.Mapper); tpl != nil {
			if ps, _ := mapGet(tpl, "spec").(traits.Mapper); ps != nil {
				return ps
			}
		}
		if mapGet(spec, "containers") != nil {
			return spec
		}
	}
	return types.NewStringInterfaceMap(types.DefaultTypeAdapter, map[string]any{})
}

// extractImageTag parses a container image reference and returns its tag.
// Returns "" for digest references, "latest" for references without a tag.
func extractImageTag(image string) string {
	// Digest ref: return empty (not a tag)
	if strings.Contains(image, "@") {
		return ""
	}
	// Image name is the last path component
	name := image
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		name = image[idx+1:]
	}
	// Extract tag after ":"
	if idx := strings.LastIndex(name, ":"); idx >= 0 {
		return name[idx+1:]
	}
	return "latest"
}
