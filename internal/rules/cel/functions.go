package cel

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"

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
