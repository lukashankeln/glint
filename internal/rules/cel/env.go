// Package cel provides the CEL environment and custom functions for glint rules.
package cel

import (
	"cel.dev/cel-go/cel"
)

// NewEnv constructs the shared CEL environment with all variable declarations
// and custom function registrations. The returned environment is immutable and
// safe for concurrent use across all rule evaluations.
func NewEnv() (*cel.Env, error) {
	return cel.NewEnv(
		// Variable declarations
		cel.Variable("resource", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("name", cel.StringType),
		// metadata.namespace is exposed as "ns": "namespace" is a reserved word
		// in CEL and cannot be referenced in an expression.
		cel.Variable("ns", cel.StringType),
		cel.Variable("labels", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("annotations", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("kind", cel.StringType),
		cel.Variable("apiVersion", cel.StringType),

		// Custom functions
		ImageTagOption(),
		InListOption(),
		MatchesGlobOption(),
		PodContainersOption(),
		PodSpecOption(),
		SemverLTOption(),
	)
}
