package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestSplitYAML_MultiDoc(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: default
---
apiVersion: v1
kind: Service
metadata:
  name: svc1
  namespace: default
`)
	manifests, err := SplitYAML(data, "test-app", "test.yaml", false)
	require.NoError(t, err)
	assert.Len(t, manifests, 2)
	assert.Equal(t, "ConfigMap", manifests[0].Kind)
	assert.Equal(t, "cm1", manifests[0].Name)
	assert.Equal(t, "default", manifests[0].Namespace)
	assert.Equal(t, "Service", manifests[1].Kind)
	assert.Equal(t, "svc1", manifests[1].Name)
	assert.Equal(t, "test-app", manifests[0].SourceApp)
	assert.Equal(t, "test.yaml", manifests[0].SourcePath)
	assert.False(t, manifests[0].Rendered)
}

func TestSplitYAML_RenderedFlag(t *testing.T) {
	data := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: deploy1
`)
	manifests, err := SplitYAML(data, "app", "<rendered>", true)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.True(t, manifests[0].Rendered)
	assert.Equal(t, "<rendered>", manifests[0].SourcePath)
}

func TestSplitYAML_EmptyDocs(t *testing.T) {
	data := []byte(`
---
---
apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
---
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	assert.Len(t, manifests, 1)
	assert.Equal(t, "Namespace", manifests[0].Kind)
}

func TestSplitYAML_CommentOnly(t *testing.T) {
	data := []byte(`
# This is just a comment
# No actual YAML here
---
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	// Comment-only documents should be skipped
	assert.Len(t, manifests, 1)
	assert.Equal(t, "Pod", manifests[0].Kind)
}

func TestSplitYAML_MissingKind(t *testing.T) {
	data := []byte(`
apiVersion: v1
metadata:
  name: no-kind
---
apiVersion: v1
kind: Secret
metadata:
  name: real-secret
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	// The doc without kind should be skipped
	assert.Len(t, manifests, 1)
	assert.Equal(t, "Secret", manifests[0].Kind)
}

func TestSplitYAML_MissingAPIVersion(t *testing.T) {
	data := []byte(`
kind: Deployment
metadata:
  name: no-apiversion
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: valid-deploy
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	assert.Len(t, manifests, 1)
	assert.Equal(t, "valid-deploy", manifests[0].Name)
}

func TestSplitYAML_ObjectPopulated(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-cm
  namespace: prod
data:
  key: value
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.NotNil(t, manifests[0].Object)
	assert.Equal(t, "v1", manifests[0].Object["apiVersion"])
	assert.Equal(t, "ConfigMap", manifests[0].Object["kind"])
}

func TestSplitYAML_RawBytes(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-cm
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.NotEmpty(t, manifests[0].Raw)
}

func TestMergeValues(t *testing.T) {
	dst := map[string]any{
		"a": "orig",
		"b": map[string]any{"x": 1},
	}
	src := map[string]any{
		"a": "new",
		"b": map[string]any{"y": 2},
	}
	result := mergeValues(dst, src)
	assert.Equal(t, "new", result["a"])
	nested := result["b"].(map[string]any)
	assert.Equal(t, 1, nested["x"])
	assert.Equal(t, 2, nested["y"])
}

func TestHelmSetToValues(t *testing.T) {
	set := map[string]string{
		"image.tag":    "v1.0",
		"replicaCount": "3",
		"service.port": "8080",
	}
	vals := helmSetToValues(set)
	imgMap, ok := vals["image"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v1.0", imgMap["tag"])
	assert.Equal(t, "3", vals["replicaCount"])
	svcMap, ok := vals["service"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "8080", svcMap["port"])
}

func TestSplitYAML_TypeFidelity(t *testing.T) {
	// Locks in JSON-compatible typing (matching sigs.k8s.io/yaml's behavior,
	// which the hand-rolled node-to-value conversion replaced): numbers
	// normalize to float64, and date-like scalars stay strings rather than
	// being parsed into time.Time (a generic go-yaml decode quirk that would
	// silently corrupt label/annotation values).
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  annotations:
    date: 2024-01-01
    big: "9223372036854775807"
data:
  replicas: 3
  ratio: 1.5
  flag: true
  empty: null
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)

	meta := manifests[0].Object["metadata"].(map[string]any)
	ann := meta["annotations"].(map[string]any)
	assert.IsType(t, "", ann["date"])
	assert.Equal(t, "2024-01-01", ann["date"])

	d := manifests[0].Object["data"].(map[string]any)
	assert.IsType(t, float64(0), d["replicas"])
	assert.Equal(t, float64(3), d["replicas"])
	assert.Equal(t, 1.5, d["ratio"])
	assert.Equal(t, true, d["flag"])
	assert.Nil(t, d["empty"])
}

func TestSplitYAML_AnchorsAndAliases(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
data:
  base: &base
    x: 1
  derived: *base
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)

	d := manifests[0].Object["data"].(map[string]any)
	base := d["base"].(map[string]any)
	derived := d["derived"].(map[string]any)
	assert.Equal(t, float64(1), base["x"])
	assert.Equal(t, float64(1), derived["x"])
}

func TestSplitYAML_MalformedDocumentReturnsError(t *testing.T) {
	data := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
---
foo: [unclosed
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
`)
	manifests, err := SplitYAML(data, "app", "bad.yaml", false)
	require.Error(t, err)
	assert.Nil(t, manifests)
}

const mergeDocHeader = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm1\n"

func TestSplitYAML_MergeKeysMatchSigsYAML(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"merge before explicit keys", `
  a: &a {x: 1, yy: 2}
  m:
    <<: *a
    yy: 3
    z: 4
`},
		{"merge after explicit keys", `
  a: &a {x: 1, yy: 2}
  m:
    yy: 3
    z: 4
    <<: *a
`},
		{"sequence precedence", `
  a: &a {x: 1, yy: a}
  b: &b {x: 2, yy: b, z: b}
  m:
    <<: [*a, *b]
`},
		{"two merge keys", `
  a: &a {x: 1, yy: a}
  b: &b {x: 2, z: b}
  m:
    <<: *a
    <<: *b
`},
		{"nested merge inside merge source", `
  a: &a {x: 1, yy: 1}
  b: &b {<<: *a, yy: 2, z: 2}
  m:
    <<: *b
`},
		{"chained anchor", `
  a: &a {x: 1}
  b: &b {yy: 2, <<: *a}
  c: &c {<<: *b}
  m:
    <<: *c
    w: 3
`},
		{"empty mapping merge", `
  m:
    <<: {}
    k: v
`},
		{"empty sequence merge", `
  m:
    <<: []
    k: v
`},
		{"quoted merge key is literal", `
  a: &a {x: 1}
  m:
    '<<': *a
`},
		{"merge tag on other scalar is literal", `
  a: &a {x: 1}
  m:
    !!merge foo: *a
`},
		{"bang tag merge key", `
  a: &a {x: 1, yy: 2}
  m:
    ! <<: *a
    yy: 5
`},
		{"merge inside mappings in a sequence", `
  a: &a {x: 1}
  list:
    - {<<: *a, name: one}
    - name: two
      <<: *a
`},
		{"inline sequence of inline maps", `
  m:
    <<: [{x: 1}, {x: 2, z: 2}]
`},
		{"explicit merge tag merges", `
  a: &a {x: 1, yy: 2}
  m:
    !!merge <<: *a
    z: 3
`},
		{"str tag on merge key is literal", `
  m:
    !!str <<: {x: 1}
`},
		{"shallow nested override", `
  a: &a
    nest: {p: 1, q: 2}
    k: 1
  m:
    <<: *a
    nest: {p: 9}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(mergeDocHeader + "data:" + tc.data)
			var want map[string]any
			require.NoError(t, sigsyaml.Unmarshal(data, &want))

			manifests, err := SplitYAML(data, "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 1)
			assert.Equal(t, want, manifests[0].Object)
		})
	}
}

func TestSplitYAML_MergeKeyIssueExample(t *testing.T) {
	data := []byte(mergeDocHeader + `data:
  base: &base
    image: nginx:latest
    securityContext:
      runAsNonRoot: true
  containers:
    - <<: *base
      name: c
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)

	d := manifests[0].Object["data"].(map[string]any)
	cs := d["containers"].([]any)
	require.Len(t, cs, 1)
	c := cs[0].(map[string]any)
	assert.Equal(t, "nginx:latest", c["image"])
	assert.Equal(t, map[string]any{"runAsNonRoot": true}, c["securityContext"])
	assert.Equal(t, "c", c["name"])
	assert.NotContains(t, c, "<<")
}

func TestSplitYAML_TopLevelMergeSuppliesKind(t *testing.T) {
	data := []byte(`<<: {apiVersion: v1, kind: ConfigMap}
metadata:
  name: cm1
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.Equal(t, "ConfigMap", manifests[0].Kind)
	assert.Equal(t, "v1", manifests[0].APIVersion)
	assert.Equal(t, "cm1", manifests[0].Name)
}

func TestNodeToValue_Errors(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		wantErr string
		isMerge bool
	}{
		{"sequence contains itself", "m: &a [*a]\n", "value contains itself", false},
		{"mapping merges itself", "m: &a {k: 1, <<: *a}\n", "value contains itself", false},
		{"scalar merge", "m: {<<: 5}\n", "", true},
		{"null merge", "m: {<<: null}\n", "", true},
		{"alias to sequence", "s: &s [1, 2]\nm: {<<: *s}\n", "", true},
		{"mapping key", "? {a: 1}\n: v\n", "invalid map key", false},
		{"sequence key", "? [1, 2]\n: v\n", "invalid map key", false},
		{"alias to mapping as key", "a: &a {p: 1}\n? *a\n: v\n", "invalid map key", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tc.doc), &n))
			_, err := nodeToValue(&n)
			require.Error(t, err)
			if tc.isMerge {
				require.ErrorIs(t, err, errMergeValue)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

func TestSplitYAML_InvalidMergeValueSkipsDocument(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"scalar", "  m:\n    <<: 5\n"},
		{"null", "  m:\n    <<: null\n"},
		{"bare", "  m:\n    <<:\n"},
		{"sequence with scalar", "  m:\n    <<: [{x: 1}, 5]\n"},
		{"alias to sequence", "  s: &s [1, 2]\n  m:\n    <<: *s\n"},
		{"sequence of alias to sequence", "  s: &s [1, 2]\n  m:\n    <<: [*s]\n"},
	}
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"
	good2 := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good2\n"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := mergeDocHeader + "data:\n" + tc.body

			var tmp map[string]any
			assert.Error(t, sigsyaml.Unmarshal([]byte(bad), &tmp))

			data := []byte(good + "---\n" + bad + "---\n" + good2)
			manifests, err := SplitYAML(data, "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 2)
			assert.Equal(t, "good", manifests[0].Name)
			assert.Equal(t, "good2", manifests[1].Name)
		})
	}
}

func TestSplitYAML_SelfReferentialAnchorSkipsDocument(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"mapping merges itself", "  m: &a {k: 1, <<: *a}\n"},
		{"sequence contains itself", "  m: &a [*a]\n"},
	}
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := mergeDocHeader + "data:\n" + tc.body
			data := []byte(bad + "---\n" + good)
			manifests, err := SplitYAML(data, "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 1)
			assert.Equal(t, "good", manifests[0].Name)
		})
	}
}

// billionLaughsData builds a ConfigMap whose data nests levels a..e, each a
// 10-element list of aliases to the previous level: about 10^5 leaves, far past
// the ~5.4k decodes at which the guard trips, yet small enough that a guard
// regression fails an assertion instead of exhausting memory.
func billionLaughsData() string {
	var b strings.Builder
	b.WriteString(mergeDocHeader + "data:\n  a: &a [x, x, x, x, x, x, x, x, x, x]\n")
	prev := "a"
	for _, l := range "bcde" {
		refs := strings.TrimSuffix(strings.Repeat("*"+prev+", ", 10), ", ")
		fmt.Fprintf(&b, "  %c: &%c [%s]\n", l, l, refs)
		prev = string(l)
	}
	return b.String()
}

func TestNodeToValue_BillionLaughsRejected(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(billionLaughsData()), &node))

	_, err := nodeToValue(&node)
	require.ErrorIs(t, err, errExcessiveAliasing)
}

func TestSplitYAML_BillionLaughsSkipsDocument(t *testing.T) {
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"
	data := []byte(billionLaughsData() + "---\n" + good)

	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.Equal(t, "good", manifests[0].Name)
}

func TestNodeToValue_ManyModerateAliasesAccepted(t *testing.T) {
	// Over 1000 decodes and over 100 alias decodes, so the ratio check runs,
	// but aliases stay well under the 99% allowed ratio.
	var b strings.Builder
	b.WriteString(mergeDocHeader + "data:\n  base: &base {a: 1, b: 2, c: 3}\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "  p%d: v%d\n", i, i)
	}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "  k%d: *base\n", i)
	}
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(b.String()), &node))

	v, err := nodeToValue(&node)
	require.NoError(t, err)
	data := v.(map[string]any)["data"].(map[string]any)
	assert.Len(t, data, 3201)
	assert.Equal(t, map[string]any{"a": float64(1), "b": float64(2), "c": float64(3)}, data["k199"])
}

func TestNodeToValue_AnchorInsideMergeSequence(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("data:\n  <<: [&x {a: 1}]\n  other: *x\n"), &node))

	v, err := nodeToValue(&node)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"data": map[string]any{
			"a":     float64(1),
			"other": map[string]any{"a": float64(1)},
		},
	}, v)
}

func TestNodeToValue_AnchorNestedInAnchoredNode(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("a: &a {b: &b 1}\nc: *b\n"), &node))

	v, err := nodeToValue(&node)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"a": map[string]any{"b": float64(1)},
		"c": float64(1),
	}, v)
}

func TestSplitYAML_CrossDocumentAliasSkipsDocument(t *testing.T) {
	def := mergeDocHeader + "data:\n  x: &a {p: 1}\n"
	ref := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ref\ndata:\n  y: *a\n"
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"

	var tmp map[string]any
	assert.Error(t, sigsyaml.Unmarshal([]byte(ref), &tmp))

	manifests, err := SplitYAML([]byte(def+"---\n"+ref+"---\n"+good), "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 2)
	assert.Equal(t, "cm1", manifests[0].Name)
	assert.Equal(t, "good", manifests[1].Name)
}

func TestNodeToValue_UnknownAnchor(t *testing.T) {
	dec := yaml.NewDecoder(strings.NewReader("x: &a {p: 1}\n---\ny: *a\n"))
	var d1, d2 yaml.Node
	require.NoError(t, dec.Decode(&d1))
	require.NoError(t, dec.Decode(&d2))
	_, err := nodeToValue(&d1)
	require.NoError(t, err)
	_, err = nodeToValue(&d2)
	require.ErrorContains(t, err, `unknown anchor "a"`)
}

func TestSplitYAML_ComplexKeySkipsDocument(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"mapping key", "  ? {a: 1}\n  : v\n"},
		{"sequence key", "  ? [1, 2]\n  : v\n"},
	}
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := mergeDocHeader + "data:\n" + tc.body

			var tmp map[string]any
			assert.Error(t, sigsyaml.Unmarshal([]byte(bad), &tmp))

			manifests, err := SplitYAML([]byte(good+"---\n"+bad), "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 1)
			assert.Equal(t, "good", manifests[0].Name)
		})
	}
}
