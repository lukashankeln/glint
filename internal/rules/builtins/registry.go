// Package builtins provides metadata and param substitution for built-in rules.
package builtins

// IDs is the set of rule IDs that are considered built-in.
var IDs = map[string]bool{
	"no-latest-tag":    true,
	"resource-requests": true,
	"deprecated-apis":  true,
}
