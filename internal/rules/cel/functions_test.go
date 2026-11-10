package cel

import (
	"testing"

	"cel.dev/cel-go/common/types/ref"
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
		"ns":          "",
		"labels":      map[string]string{},
		"annotations": map[string]string{},
		"kind":        "",
		"apiVersion":  "",
	}
}

func TestPodSpecAndContainers(t *testing.T) {
	env, err := NewEnv()
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	eval := func(t *testing.T, expr string, res map[string]any) any {
		t.Helper()
		ast, iss := env.Compile(expr)
		if iss != nil && iss.Err() != nil {
			t.Fatalf("compile %q: %v", expr, iss.Err())
		}
		prog, err := env.Program(ast)
		if err != nil {
			t.Fatalf("program: %v", err)
		}
		vars := baseVars()
		vars["resource"] = res
		out, _, err := prog.Eval(vars)
		if err != nil {
			t.Fatalf("eval %q: %v", expr, err)
		}
		return out.Value()
	}
	ctr := func(n string) map[string]any { return map[string]any{"name": n} }
	names := `podContainers(resource).map(c, c.name)`
	nameList := func(ns ...any) []any { return ns }

	pod := map[string]any{"kind": "Pod", "spec": map[string]any{
		"hostPID": true, "containers": []any{ctr("a")}}}
	deploy := map[string]any{"spec": map[string]any{"template": map[string]any{
		"spec": map[string]any{"containers": []any{ctr("d")}}}}}
	cron := map[string]any{"spec": map[string]any{"jobTemplate": map[string]any{
		"spec": map[string]any{"template": map[string]any{
			"spec": map[string]any{"containers": []any{ctr("c")}}}}}}}

	tests := []struct {
		name string
		expr string
		res  map[string]any
		want any
	}{
		{"pod", names, pod, nameList("a")},
		{"pod hostPID", `has(podSpec(resource).hostPID) && podSpec(resource).hostPID == true`, pod, true},
		{"deployment", names, deploy, nameList("d")},
		{"cronjob", names, cron, nameList("c")},
		{"deployment no hostPID", `has(podSpec(resource).hostPID)`, deploy, false},
		{"configmap no spec", `size(podSpec(resource)) == 0 && size(podContainers(resource)) == 0`, map[string]any{"data": map[string]any{"a": "b"}}, true},
		{"spec without containers", `size(podSpec(resource)) == 0 && size(podContainers(resource)) == 0`, map[string]any{"spec": map[string]any{"replicas": 1.0}}, true},
		{"spec null", `size(podSpec(resource)) == 0 && size(podContainers(resource)) == 0`, map[string]any{"spec": nil}, true},
		{"template null", `size(podContainers(resource)) == 0`, map[string]any{"spec": map[string]any{"template": nil}}, true},
		{"template wrong type", `size(podContainers(resource)) == 0`, map[string]any{"spec": map[string]any{"template": "x"}}, true},
		{"jobTemplate null falls to template", names, map[string]any{"spec": map[string]any{
			"jobTemplate": nil, "template": map[string]any{"spec": map[string]any{"containers": []any{ctr("t")}}}}}, nameList("t")},
		{"containers null", names, map[string]any{"spec": map[string]any{
			"containers": nil, "initContainers": []any{ctr("i")}}}, nameList("i")},
		{"containers wrong type", names, map[string]any{"spec": map[string]any{
			"containers": "x", "initContainers": []any{ctr("i")}}}, nameList("i")},
		{"template spec null falls to spec.containers", names, map[string]any{"spec": map[string]any{
			"template": map[string]any{"spec": nil}, "containers": []any{ctr("s")}}}, nameList("s")},
		{"jobTemplate non-map spec falls to template", names, map[string]any{"spec": map[string]any{
			"jobTemplate": map[string]any{"spec": "x"},
			"template":    map[string]any{"spec": map[string]any{"containers": []any{ctr("t")}}}}}, nameList("t")},
		{"cronjob all lists in order", names, map[string]any{"spec": map[string]any{"jobTemplate": map[string]any{
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"ephemeralContainers": []any{ctr("e")},
				"initContainers":      []any{ctr("i")},
				"containers":          []any{ctr("a")}}}}}}}, nameList("a", "i", "e")},
		{"all lists in order", names, map[string]any{"spec": map[string]any{
			"ephemeralContainers": []any{ctr("e")},
			"initContainers":      []any{ctr("i")},
			"containers":          []any{ctr("a"), ctr("b")}}}, nameList("a", "b", "i", "e")},
		{"map literal containers", `podContainers({"spec": {"containers": [{"name": "a"}]}}).map(c, c.name)`, map[string]any{}, nameList("a")},
		{"map literal empty", `has(podSpec({}).hostPID)`, map[string]any{}, false},
		{"container predicate", `podContainers(resource).all(c, !has(c.securityContext) || c.securityContext.privileged != true)`,
			map[string]any{"spec": map[string]any{"containers": []any{
				map[string]any{"name": "a", "securityContext": map[string]any{"privileged": true}}}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := eval(t, tc.expr, tc.res)
			if l, ok := got.([]ref.Val); ok {
				conv := make([]any, len(l))
				for i, e := range l {
					conv[i] = e.Value()
				}
				got = conv
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
