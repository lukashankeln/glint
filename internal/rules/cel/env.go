// Package cel provides the CEL environment and custom functions for glint rules.
package cel

import (
	"github.com/google/cel-go/cel"
)

// NewEnv constructs the shared CEL environment with all variable declarations
// and custom function registrations. The returned environment is immutable and
// safe for concurrent use across all rule evaluations.
func NewEnv() (*cel.Env, error) {
	return cel.NewEnv(
		// Variable declarations
		cel.Variable("resource", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("name", cel.StringType),
		cel.Variable("namespace", cel.StringType),
		cel.Variable("labels", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("annotations", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("kind", cel.StringType),
		cel.Variable("apiVersion", cel.StringType),

		// Custom functions
		ImageTagOption(),
		InListOption(),
		MatchesGlobOption(),
		SemverLTOption(),
	)
}
