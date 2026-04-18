package cel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractImageTag(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"nginx", "latest"},
		{"nginx:latest", "latest"},
		{"nginx:1.21", "1.21"},
		{"nginx:1.21.0-alpine", "1.21.0-alpine"},
		{"nginx@sha256:abcdef123", ""},
		{"registry.io:5000/nginx", "latest"},
		{"registry.io:5000/nginx:1.0", "1.0"},
		{"registry.io:5000/org/nginx:2.0", "2.0"},
		{"gcr.io/myproject/app@sha256:abc", ""},
		{"gcr.io/myproject/app:v1.2.3", "v1.2.3"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			assert.Equal(t, tt.want, extractImageTag(tt.image))
		})
	}
}

func TestCELFunctions(t *testing.T) {
	env, err := NewEnv()
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}

	run := func(expr string, vars map[string]any) (any, error) {
		ast, issues := env.Compile(expr)
		if issues != nil && issues.Err() != nil {
			return nil, issues.Err()
		}
		prog, err := env.Program(ast)
		if err != nil {
			return nil, err
		}
		out, _, err := prog.Eval(vars)
		if err != nil {
			return nil, err
		}
		return out.Value(), nil
	}

	t.Run("imageTag_no_tag", func(t *testing.T) {
		v, err := run(`imageTag("nginx")`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, "latest", v)
	})

	t.Run("imageTag_with_tag", func(t *testing.T) {
		v, err := run(`imageTag("nginx:1.21")`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, "1.21", v)
	})

	t.Run("imageTag_digest", func(t *testing.T) {
		v, err := run(`imageTag("nginx@sha256:abc")`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, "", v)
	})

	t.Run("inList_found", func(t *testing.T) {
		v, err := run(`inList("apps/v1beta1", ["apps/v1", "apps/v1beta1"])`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, true, v)
	})

	t.Run("inList_not_found", func(t *testing.T) {
		v, err := run(`inList("apps/v1", ["apps/v1beta1", "apps/v1beta2"])`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, false, v)
	})

	t.Run("matchesGlob_match", func(t *testing.T) {
		v, err := run(`matchesGlob("gcr.io/proj/app", "gcr.io/proj/*")`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, true, v)
	})

	t.Run("matchesGlob_no_match", func(t *testing.T) {
		v, err := run(`matchesGlob("gcr.io/other/app", "gcr.io/proj/*")`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, false, v)
	})

	t.Run("semverLT_true", func(t *testing.T) {
		v, err := run(`semverLT("1.2.3", "1.3.0")`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, true, v)
	})

	t.Run("semverLT_false", func(t *testing.T) {
		v, err := run(`semverLT("2.0.0", "1.9.9")`, baseVars())
		assert.NoError(t, err)
		assert.Equal(t, false, v)
	})
}

// baseVars provides the minimal CEL activation required by the env declarations.
func baseVars() map[string]any {
	return map[string]any{
		"resource":    map[string]any{},
		"name":        "",
		"namespace":   "",
		"labels":      map[string]string{},
		"annotations": map[string]string{},
		"kind":        "",
		"apiVersion":  "",
	}
}
