package cel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/manifest"
)

func evalWithManifest(t *testing.T, expr string, m manifest.Manifest) any {
	t.Helper()
	env, err := NewEnv()
	require.NoError(t, err)
	ast, iss := env.Compile(expr)
	require.NoError(t, iss.Err(), "compile %q", expr)
	prg, err := env.Program(ast)
	require.NoError(t, err)
	out, _, err := prg.Eval(ManifestToVars(m))
	require.NoError(t, err, "eval %q", expr)
	return out.Value()
}

func nsManifest(namespace string) manifest.Manifest {
	meta := map[string]any{"name": "app"}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	return manifest.Manifest{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Name:       "app",
		Namespace:  namespace,
		Object:     map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta},
	}
}

// Every declared variable must be referenceable on its own and bound by
// ManifestToVars; a reserved identifier (such as "namespace") fails here.
func TestNewEnv_EveryVariableIsUsable(t *testing.T) {
	m := nsManifest("prod")
	vars := ManifestToVars(m)
	for _, name := range []string{"resource", "name", "ns", "labels", "annotations", "kind", "apiVersion"} {
		t.Run(name, func(t *testing.T) {
			_, bound := vars[name]
			assert.True(t, bound, "ManifestToVars does not bind %q", name)
			evalWithManifest(t, name, m)
		})
	}
}

func TestNamespaceVariable(t *testing.T) {
	assert.Equal(t, true, evalWithManifest(t, `matchesGlob(ns, "prod-*")`, nsManifest("prod-a")))
	assert.Equal(t, false, evalWithManifest(t, `matchesGlob(ns, "prod-*")`, nsManifest("staging")))
	assert.Equal(t, true, evalWithManifest(t, `ns == ""`, nsManifest("")))
	assert.Equal(t, false, evalWithManifest(t, `ns == ""`, nsManifest("prod-a")))
}

// "namespace" is a CEL reserved word; it must not be declared as a variable.
func TestNamespaceIsReserved(t *testing.T) {
	env, err := NewEnv()
	require.NoError(t, err)
	_, iss := env.Compile(`namespace == ""`)
	require.Error(t, iss.Err())
	assert.Contains(t, iss.Err().Error(), "reserved identifier")
}
