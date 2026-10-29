package render

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime"
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
		wantIs  error
	}{
		{"sequence contains itself", "m: &a [*a]\n", "value contains itself", nil},
		{"mapping merges itself", "m: &a {k: 1, <<: *a}\n", "value contains itself", nil},
		{"scalar merge", "m: {<<: 5}\n", "", errMergeValue},
		{"null merge", "m: {<<: null}\n", "", errMergeValue},
		{"alias to sequence", "s: &s [1, 2]\nm: {<<: *s}\n", "", errMergeValue},
		{"mapping key", "? {a: 1}\n: v\n", "invalid map key", nil},
		{"sequence key", "? [1, 2]\n: v\n", "invalid map key", nil},
		{"alias to mapping as key", "a: &a {p: 1}\n? *a\n: v\n", "invalid map key", nil},
		{"non-finite float value", "v: .inf\n", "", errNonFiniteFloat},
		{"non-finite float document root", ".inf\n", "", errNonFiniteFloat},
		{"invalid binary", "v: !!binary \"###\"\n", "", errInvalidBinary},
		{"non-null !!null", "v: !!null yes\n", "", errInvalidNull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tc.doc), &n))
			_, err := nodeToValue(&n)
			require.Error(t, err)
			if tc.wantIs != nil {
				require.ErrorIs(t, err, tc.wantIs)
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

func TestNodeToValue_AnchorDefinedEarlierInMergeSequence(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want map[string]any
	}{
		{"alias of earlier source", "m: {<<: [&a {x: 1}, *a]}\n",
			map[string]any{"m": map[string]any{"x": float64(1)}}},
		{"nested anchor reached from later source", "m: {<<: [&a {x: &b {p: 1}}, {q: *b}]}\n",
			map[string]any{"m": map[string]any{
				"x": map[string]any{"p": float64(1)},
				"q": map[string]any{"p": float64(1)},
			}}},
		{"earlier source used as later value", "m: {<<: [&a {x: 1}, {k: *a}]}\n",
			map[string]any{"m": map[string]any{
				"x": float64(1),
				"k": map[string]any{"x": float64(1)},
			}}},
		{"earlier source wins, explicit key after wins", "m: {<<: [&a {x: 1, w: 1}, {x: 2, w: 2, z: 3}], w: 9}\n",
			map[string]any{"m": map[string]any{
				"x": float64(1), "w": float64(9), "z": float64(3),
			}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var node yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tc.src), &node))
			got, err := nodeToValue(&node)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			var viaSigs map[string]any
			require.NoError(t, sigsyaml.Unmarshal([]byte(tc.src), &viaSigs))
			assert.Equal(t, viaSigs, got)
		})
	}
}

func TestSplitYAML_AnchorDefinedEarlierInMergeSequenceEmitted(t *testing.T) {
	data := []byte(mergeDocHeader + "data:\n  m: {<<: [&a {x: 1}, *a]}\n")
	var want map[string]any
	require.NoError(t, sigsyaml.Unmarshal(data, &want))

	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.Equal(t, want, manifests[0].Object)
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

// oldPipelineObject reproduces SplitYAML's object decoding from before
// nodeToValue: yaml.v3 re-encode of the parsed node, then sigs.k8s.io/yaml.
func oldPipelineObject(t *testing.T, data []byte) map[string]any {
	t.Helper()
	obj, err := oldPipelineObjectErr(data)
	require.NoError(t, err)
	return obj
}

// oldPipelineObjectErr is oldPipelineObject returning the error, for inputs
// the old pipeline rejects.
func oldPipelineObjectErr(data []byte) (map[string]any, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	docBytes, err := yaml.Marshal(&node)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := sigsyaml.Unmarshal(docBytes, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// TestSplitYAML_NonJSONScalarsMatchOldPipeline pins .inf/.nan, !!binary and
// !!null handling to the old pipeline: where it rejected the document the
// document is skipped, otherwise the objects are equal and JSON-encodable.
func TestSplitYAML_NonJSONScalarsMatchOldPipeline(t *testing.T) {
	bodies := []string{
		// Non-finite floats: rejected as values, accepted as keys.
		"  v: .inf\n", "  v: -.inf\n", "  v: .NaN\n", "  v: .nan\n",
		"  v: !!float .inf\n", "  v: [.inf, 1]\n", "  v: {a: .inf}\n",
		"  .inf: 1\n", "  -.inf: 1\n", "  .nan: 1\n",
		"  a: &a .inf\n  b: [*a]\n",
		"  &a .inf : 1\n  b: {*a : 2}\n",
		"  m: &m {x: .inf}\n  q: {<<: *m}\n",
		// !!binary values and keys.
		"  v: !!binary eWVz\n", "  v: !!binary \"eWVz\"\n", "  v: !<tag:yaml.org,2002:binary> eWVz\n",
		"  v: !!binary |\n    eWVz\n    eWVz\n",
		"  v: !!binary >\n    eWVz\n    eWVz\n",
		"  v: !!binary \"eW Vz\"\n",
		"  v: !!binary \"###\"\n", "  v: !!binary \"eWV\"\n", "  v: !!binary 123\n",
		"  v: !!binary /w==\n", "  v: !!binary //8=\n",
		"  !!binary eWVz: 1\n",
		"  a: &a !!binary eWVz\n  b: [*a, *a]\n",
		// !!null: only the YAML 1.1 null spellings.
		"  v: !!null yes\n", "  v: !!null Nul\n", "  v: !!null nULL\n",
		"  v: !!null\n", "  v: ~\n", "  v: null\n", "  v: NULL\n", "  v: \"\"\n",
		"  v: 'null'\n", "  v: ''\n", "  v: !!null ~\n", "  v: !!null 'null'\n", "  v: !!null \"\"\n",
		// Regression rows that already matched.
		"  v: !!float 1e400\n", "  !!float 1e400: 1\n", "  v: !!int .inf\n",
		"  v: " + strings.Repeat("9", 400) + "\n",
		"  v: !!str .inf\n", "  v: !!int 0x10\n", "  v: !!timestamp 2001-12-14\n",
	}
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			doc := mergeDocHeader + "data:\n" + body
			want, oldErr := oldPipelineObjectErr([]byte(doc))
			if oldErr != nil {
				manifests, err := SplitYAML([]byte(good+"---\n"+doc), "app", "file.yaml", false)
				require.NoError(t, err)
				require.Len(t, manifests, 1)
				assert.Equal(t, "good", manifests[0].Name)
				return
			}
			manifests, err := SplitYAML([]byte(doc), "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 1)
			assert.Equal(t, want, manifests[0].Object)
			_, err = json.Marshal(manifests[0].Object)
			assert.NoError(t, err)
		})
	}
}

// TestSplitYAML_BinaryKeyCollisionKeepsLast pins a known difference from the
// old pipeline: two !!binary keys that decode to the same string after
// invalid bytes become U+FFFD keep the later one in document order (the old
// pipeline kept the one whose raw key bytes sorted last).
func TestSplitYAML_BinaryKeyCollisionKeepsLast(t *testing.T) {
	doc := mergeDocHeader + "data:\n  !!binary //8=: 1\n  !!binary /v8=: 2\n"
	manifests, err := SplitYAML([]byte(doc), "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.Equal(t, map[string]any{"\uFFFD\uFFFD": float64(2)}, manifests[0].Object["data"])
}

func TestSplitYAML_YAML11BoolsMatchOldPipeline(t *testing.T) {
	literals := []string{
		"y", "Y", "yes", "Yes", "YES", "on", "On", "ON", "true", "True", "TRUE",
		"n", "N", "no", "No", "NO", "off", "Off", "OFF", "false", "False", "FALSE",
	}
	var cases []string
	for _, l := range literals {
		cases = append(cases,
			"  v: "+l+"\n",
			"  "+l+": 1\n",
			"  dq: \""+l+"\"\n",
			"  sq: '"+l+"'\n",
			"  t: !!str "+l+"\n",
			"  \""+l+"\": 1\n",
		)
	}
	cases = append(cases,
		"  lit: |\n    yes\n",
		"  fold: >\n    no\n",
		"  mixed: yEs\n  oN: 1\n",
		"  seq: [y, n, on, off, \"yes\"]\n",
		"  bang: ! yes\n",
		"  tb: !!bool yes\n",
		"  tbq: !!bool \"off\"\n",
	)
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			data := []byte(mergeDocHeader + "data:\n" + c)
			want := oldPipelineObject(t, data)

			manifests, err := SplitYAML(data, "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 1)
			assert.Equal(t, want, manifests[0].Object)
		})
	}
}

func TestSplitYAML_YAML11BoolIssueExample(t *testing.T) {
	data := []byte(mergeDocHeader + "data:\n  y: 1\n  \"y\": 2\n  on: off\n  q: \"yes\"\n")
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	d := manifests[0].Object["data"].(map[string]any)
	assert.Equal(t, map[string]any{"true": false, "y": float64(2), "q": "yes"}, d)
}

// parseNode parses a single YAML document into a Node tree.
func parseNode(t *testing.T, doc string) *yaml.Node {
	t.Helper()
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(doc), &node))
	return &node
}

// aliasedScalarDoc builds a ConfigMap with one anchored string of strLen bytes
// and a list of n aliases to it.
func aliasedScalarDoc(name string, strLen, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\ndata:\n  s: &s %s\n  l:\n", name, strings.Repeat("a", strLen))
	b.WriteString(strings.Repeat("  - *s\n", n))
	return b.String()
}

func TestNodeToValue_AliasedScalarAmplificationRejected(t *testing.T) {
	// The alias ratio is ~0.5, so only the byte budget catches this: 2000
	// copies of 16 KiB would be ~32 MB.
	node := parseNode(t, aliasedScalarDoc("amp", 16<<10, 2000))

	_, err := nodeToValue(node)
	require.ErrorIs(t, err, errExcessiveAliasExpansion)
}

func TestNodeToValue_AliasedMappingAmplificationRejected(t *testing.T) {
	var b strings.Builder
	fmt.Fprintf(&b, "%sdata:\n  m: &m {k: %s}\n  l:\n", mergeDocHeader, strings.Repeat("a", 16<<10))
	b.WriteString(strings.Repeat("  - *m\n", 200))

	_, err := nodeToValue(parseNode(t, b.String()))
	require.ErrorIs(t, err, errExcessiveAliasExpansion)
}

func TestSplitYAML_AliasAmplificationSkipsDocument(t *testing.T) {
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"
	data := []byte(aliasedScalarDoc("amp", 16<<10, 2000) + "---\n" + good)

	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.Equal(t, "good", manifests[0].Name)
}

func TestSplitYAML_AliasBudgetSharedAcrossStream(t *testing.T) {
	// Each document alone expands 40 * (16 KiB + aliasedScalarCost) bytes, within the budget;
	// both together are not.
	const strLen, n = 16 << 10, 40
	cost := n * (strLen + aliasedScalarCost)
	data := []byte(aliasedScalarDoc("first", strLen, n) + "---\n" + aliasedScalarDoc("second", strLen, n))
	budget := aliasByteBudget(len(data))
	require.Less(t, cost, budget, "fixture: one document must fit")
	require.Greater(t, 2*cost, budget, "fixture: two documents must not fit")

	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.Equal(t, "first", manifests[0].Name)
}

func TestNodeToValueBudget_Edge(t *testing.T) {
	// The anchored scalar is free; the alias, one level deep, charges
	// len("abc") + aliasedScalarCost + aliasedIndentCost*1 bytes.
	const doc = "[&s abc, *s]"
	const cost = len("abc") + aliasedScalarCost + aliasedIndentCost

	indent := indentByteBudget(0)
	budget := cost
	v, err := nodeToValueBudget(parseNode(t, doc), &budget, &indent)
	require.NoError(t, err)
	assert.Equal(t, []any{"abc", "abc"}, v)
	assert.Equal(t, 0, budget)

	budget = cost - 1
	_, err = nodeToValueBudget(parseNode(t, doc), &budget, &indent)
	require.ErrorIs(t, err, errExcessiveAliasExpansion)
}

func TestNodeToValue_DeepAliasedMappingRejected(t *testing.T) {
	// A mapping nested 800 levels, aliased 60 times. Flat per-node charges
	// would total ~100 KB, but the output indents every line by its depth
	// (~2 bytes per level), ~78 MB in all.
	deep := strings.Repeat("{a: ", 800) + "x" + strings.Repeat("}", 800)
	var b strings.Builder
	fmt.Fprintf(&b, "d: &d %s\nl:\n", deep)
	b.WriteString(strings.Repeat("  - *d\n", 60))

	// The aliased copies also charge the indentation budget, which nodeToValue
	// would trip first; give it ample room so this pins the alias budget's
	// indentation charge on its own.
	budget, indent := aliasByteBudget(0), 1<<40
	_, err := nodeToValueBudget(parseNode(t, b.String()), &budget, &indent)
	require.ErrorIs(t, err, errExcessiveAliasExpansion)

	_, err = nodeToValue(parseNode(t, b.String()))
	require.ErrorIs(t, err, errExcessiveNesting)
}

func TestNodeToValueBudget_NodeChargesOnly(t *testing.T) {
	// Aliased empty mappings and sequences carry no scalars: each alias, one
	// level deep, charges its base cost plus aliasedIndentCost.
	const n = 30
	indent := indentByteBudget(0)
	for anchored, base := range map[string]int{"{}": aliasedMapCost, "[]": aliasedSeqCost} {
		doc := "[&e " + anchored + strings.Repeat(", *e", n) + "]"
		total := n * (base + aliasedIndentCost)

		budget := total
		_, err := nodeToValueBudget(parseNode(t, doc), &budget, &indent)
		require.NoError(t, err, anchored)
		assert.Equal(t, 0, budget, anchored)

		budget = total - 1
		_, err = nodeToValueBudget(parseNode(t, doc), &budget, &indent)
		require.ErrorIs(t, err, errExcessiveAliasExpansion, anchored)
	}
}

func TestSplitYAML_AliasBudgetScalesWithInput(t *testing.T) {
	// ~150 KB of plain padding raises the stream budget above the 1 MiB floor;
	// the aliased content costs more than the floor but less than the scaled
	// budget, so the document is accepted only because the budget scales.
	const strLen, n = 16 << 10, 80
	doc := aliasedScalarDoc("scaled", strLen, n) + "  pad: " + strings.Repeat("p", 150<<10) + "\n"
	data := []byte(doc)
	cost := n * (strLen + aliasedScalarCost)
	require.Greater(t, cost, aliasByteBudget(0), "fixture: must exceed the floor")
	require.Less(t, cost+n*3*aliasedIndentCost, aliasByteBudget(len(data)), "fixture: must fit the scaled budget")

	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.Equal(t, "scaled", manifests[0].Name)
}

func TestSplitYAML_RejectedDocumentRefundsBudget(t *testing.T) {
	// The bomb drains the budget before it is rejected; the refund leaves the
	// later anchored document its share.
	const strLen, n = 16 << 10, 40
	data := []byte(aliasedScalarDoc("bomb", 16<<10, 2000) + "---\n" +
		aliasedScalarDoc("modest", strLen, n) + "---\n" +
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n")
	require.Less(t, n*(strLen+aliasedScalarCost+6), aliasByteBudget(len(data)), "fixture: modest must fit alone")

	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 2)
	assert.Equal(t, "modest", manifests[0].Name)
	assert.Equal(t, "good", manifests[1].Name)
}

func TestSplitYAML_ManySmallAliasedMapsRejected(t *testing.T) {
	// Each document aliases a one-pair map `uses` times: about 1 KiB of input
	// that costs a fraction of the budget, so one document fits alone but the
	// ten together do not. The heap-shaped charges must not accept the stream.
	const uses = 300
	var doc strings.Builder
	doc.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  m: &a {k0: v}\n  l: [")
	doc.WriteString(strings.TrimSuffix(strings.Repeat("*a, ", uses), ", "))
	doc.WriteString("]\n")
	const docs = 10
	data := []byte(strings.Repeat("---\n"+doc.String(), docs))
	one := uses * (aliasedMapCost + aliasedEntryCost + 2*(len("k0")+aliasedScalarCost))
	budget := aliasByteBudget(len(data))
	require.Less(t, one, budget, "fixture: one document must fit")
	require.Greater(t, docs*one, budget, "fixture: all documents must not fit")

	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	assert.Less(t, len(manifests), docs)
	assert.NotEmpty(t, manifests)
}

func TestAliasByteBudget_FloorAndCeiling(t *testing.T) {
	assert.Equal(t, aliasedBytesFloor, aliasByteBudget(0))
	assert.Equal(t, aliasedBytesFloor, aliasByteBudget(1))
	assert.Equal(t, 10*(1<<20), aliasByteBudget(1<<20))
	assert.Equal(t, aliasedBytesCeiling, aliasByteBudget(aliasedBytesCeiling))
	assert.Equal(t, aliasedBytesCeiling, aliasByteBudget(1<<40))
}

// TestAliasCharges_CoverHeapAllocation checks that what nodeToValue charges for
// aliased content is at least what the conversion allocates (runtime.MemStats
// TotalAlloc, which counts garbage as well as retained bytes). Each case is a
// list holding the anchor once and then n uses of it; the cost of one use is
// the difference between 2n and n uses, which cancels fixed costs and the
// unaliased scaffolding. The leading plain scalars keep the alias ratio under
// yaml.v3's excessive-aliasing limit. The charge must not fall below the
// allocation; the constants carry a margin on top of that.
func TestAliasCharges_CoverHeapAllocation(t *testing.T) {
	const n = 100
	pairs := func(k int) string {
		p := make([]string, k)
		for i := range p {
			p[i] = fmt.Sprintf("k%d: v", i)
		}
		return strings.Join(p, ", ")
	}
	type tc struct{ name, anchor, use string }
	var cases []tc
	for _, k := range []int{1, 8, 9, 57} {
		cases = append(cases,
			tc{fmt.Sprintf("map-%d", k), "&a {" + pairs(k) + "}", "*a"},
			tc{fmt.Sprintf("seq-%d", k), "&a [" + strings.Repeat("v, ", k) + "]", "*a"},
		)
	}
	// The unaliased wrapper map {<<: ...} is input scaffolding that no alias
	// charge can cover, so a merge of a single entry (no growth of the wrapper
	// beyond its presize) is left out.
	for _, k := range []int{8, 9, 57} {
		cases = append(cases,
			tc{fmt.Sprintf("merge-%d", k), "&a {" + pairs(k) + "}", "{<<: *a}"},
			tc{fmt.Sprintf("merge-seq-%d", k), "&a {" + pairs(k) + "}", "{<<: [*a, *a]}"},
		)
	}
	typed := func(k int, key bool) string {
		p := make([]string, k)
		for i := range p {
			if key {
				p[i] = fmt.Sprintf("%d: v", i+1000)
			} else {
				p[i] = fmt.Sprintf("k%d: %d", i, i+1000)
			}
		}
		return "&a {" + strings.Join(p, ", ") + "}"
	}
	cases = append(cases,
		tc{"int", "&a 12345", "*a"},
		tc{"float", "&a 1.5", "*a"},
		tc{"bool", "&a true", "*a"},
		tc{"float-long", "&a 1." + strings.Repeat("0", 1000), "*a"},
		tc{"float-tagged-long", "&a !!float " + strings.Repeat("1", 300), "*a"},
		tc{"map-float-key-long", "&a {1." + strings.Repeat("0", 1000) + ": v}", "*a"},
		tc{"map-int-key-long", "&a {" + strings.Repeat("9", 300) + ": v}", "*a"},
		tc{"tagged-bool", "&a !!bool yes", "*a"},
		tc{"map-int-values-1", typed(1, false), "*a"},
		tc{"map-int-values-8", typed(8, false), "*a"},
		tc{"map-int-keys-1", typed(1, true), "*a"},
		tc{"map-int-keys-8", typed(8, true), "*a"},
		tc{"binary-long", "&a !!binary " + strings.Repeat("eWVz", 250), "*a"},
		tc{"binary-invalid-utf8", "&a !!binary " + strings.Repeat("////", 250), "*a"},
		tc{"binary-short", "&a !!binary eWVz", "*a"},
		tc{"scalar-empty", "&a \"\"", "*a"},
		tc{"scalar-5", "&a hello", "*a"},
		tc{"scalar-100", "&a " + strings.Repeat("x", 100), "*a"},
	)

	measure := func(node *yaml.Node) (alloc uint64, charged int) {
		budget, indent := 1<<40, 1<<40
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		v, err := nodeToValueBudget(node, &budget, &indent)
		runtime.ReadMemStats(&after)
		require.NoError(t, err)
		runtime.KeepAlive(v)
		return after.TotalAlloc - before.TotalAlloc, 1<<40 - budget
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := func(uses int) *yaml.Node {
				return parseNode(t, "["+strings.Repeat("0, ", 3000)+c.anchor+strings.Repeat(", "+c.use, uses)+"]")
			}
			// TotalAlloc is process-wide, so stray allocations only ever add
			// to a measurement: the minimum over several repeats, taken
			// separately for the small and the large document, is the closest
			// to each one's own allocation. The difference of those minima
			// is then the per-use cost; a minimum over per-repeat differences
			// could instead hide an undercharge.
			minS, minL, charged := ^uint64(0), ^uint64(0), 0
			for range 5 {
				allocS, chargedS := measure(doc(n))
				allocL, chargedL := measure(doc(2 * n))
				minS, minL = min(minS, allocS), min(minL, allocL)
				charged = (chargedL - chargedS) / n
			}
			var alloc uint64
			if minL > minS {
				alloc = (minL - minS) / n
			}
			t.Logf("per alias: allocated %d, charged %d", alloc, charged)
			assert.LessOrEqual(t, alloc, uint64(charged))
		})
	}
}

// deepFlow builds a ConfigMap whose data nests depth levels of flow mappings
// (seq false) or flow sequences (seq true).
func deepFlow(name string, depth int, seq bool) string {
	open, closeS, leaf := "{a: ", "}", "x"
	if seq {
		open, closeS = "[", "]"
	}
	head := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\ndata: ", name)
	return head + strings.Repeat(open, depth) + leaf + strings.Repeat(closeS, depth) + "\n"
}

func TestNodeToValue_DeepNestingRejected(t *testing.T) {
	for name, seq := range map[string]bool{"mapping": false, "sequence": true} {
		t.Run(name, func(t *testing.T) {
			_, err := nodeToValue(parseNode(t, deepFlow("deep", 5000, seq)))
			require.ErrorIs(t, err, errExcessiveNesting)
		})
	}
}

func TestSplitYAML_DeepNestingSkipsDocument(t *testing.T) {
	good := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: good\n"
	for name, seq := range map[string]bool{"mapping": false, "sequence": true} {
		t.Run(name, func(t *testing.T) {
			data := []byte(deepFlow("deep", 5000, seq) + "---\n" + good)
			manifests, err := SplitYAML(data, "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 1)
			assert.Equal(t, "good", manifests[0].Name)
		})
	}
}

// deepStringDoc nests a ConfigMap value depth levels of {a: ...} around the scalar
// (already formatted for flow context, or a block scalar when block is set).
func deepStringDoc(depth int, scalar string) string {
	return "apiVersion: v1\nkind: ConfigMap\ndata: " + strings.Repeat("{a: ", depth) + scalar + strings.Repeat("}", depth) + "\n"
}

func TestNodeToValue_DeepStringsRejected(t *testing.T) {
	const depth = 200
	words := strings.TrimSpace(strings.Repeat("word ", 4000))
	lines := strings.Repeat("line\\n", 4000) // escaped newlines in a double-quoted scalar
	cases := map[string]string{
		"newlines":        deepStringDoc(depth, `"`+lines+`"`),
		"plain-spaces":    deepStringDoc(depth, words),
		"quoted-spaces":   deepStringDoc(depth, `"`+words+`"`),
		"binary-newlines": deepStringDoc(depth, "!!binary "+b64(strings.Repeat("\nx", 4000))),
		"binary-spaces":   deepStringDoc(depth, "!!binary "+b64(words)),
		"nel-literal":     deepStringDoc(depth, `"a\n`+strings.Repeat(`x\N`, 4000)+`"`),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := nodeToValue(parseNode(t, doc))
			require.ErrorIs(t, err, errExcessiveNesting)
		})
	}
}

// crdSchema builds a schema-like tree depth levels deep: properties/items
// chains with a description at each level, as block YAML or compact JSON.
func crdSchema(depth int, json bool) string {
	desc := "A description of this field that has several words in it."
	if json {
		var b strings.Builder
		for range depth {
			fmt.Fprintf(&b, `{"type":"object","description":%q,"properties":{"f":`, desc)
		}
		b.WriteString(`{"type":"string"}`)
		b.WriteString(strings.Repeat("}}", depth))
		return "apiVersion: v1\nkind: ConfigMap\nschema: " + b.String() + "\n"
	}
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: ConfigMap\nschema:\n")
	for i := range depth {
		ind := strings.Repeat("  ", 1+2*i)
		fmt.Fprintf(&b, "%stype: object\n%sdescription: %s\n%sproperties:\n%s  f:\n", ind, ind, desc, ind, ind)
	}
	b.WriteString(strings.Repeat("  ", 1+2*depth) + "type: string\n")
	return b.String()
}

func TestSplitYAML_RealisticDeepSchemaAccepted(t *testing.T) {
	for name, json := range map[string]bool{"block": false, "json-flow": true} {
		t.Run(name, func(t *testing.T) {
			manifests, err := SplitYAML([]byte(crdSchema(20, json)), "app", "file.yaml", false)
			require.NoError(t, err)
			require.Len(t, manifests, 1)
		})
	}
}

func TestSplitYAML_LargeMultilineLiteralAccepted(t *testing.T) {
	var lit strings.Builder
	for lit.Len() < 200<<10 {
		lit.WriteString("    a line of the script with some words\n\n")
	}
	data := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  script: |\n" + lit.String())
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
}

func TestNodeToValueBudget_IndentBudgetShared(t *testing.T) {
	doc := deepFlow("d", 300, false) // 2*sum(0..300) ~ 90 KB
	aliasB := aliasByteBudget(0)
	indent := indentByteBudget(0)
	_, err := nodeToValueBudget(parseNode(t, doc), &aliasB, &indent)
	require.NoError(t, err)
	used := indentByteBudget(0) - indent
	require.Positive(t, used)

	// A budget that fits one document fits it once.
	indent = used
	_, err = nodeToValueBudget(parseNode(t, doc), &aliasB, &indent)
	require.NoError(t, err)
	assert.Equal(t, 0, indent)
	_, err = nodeToValueBudget(parseNode(t, doc), &aliasB, &indent)
	require.ErrorIs(t, err, errExcessiveNesting)
}

func TestIndentByteBudget_FloorAndCeiling(t *testing.T) {
	assert.Equal(t, indentBytesFloor, indentByteBudget(0))
	assert.Equal(t, 10*(1<<20), indentByteBudget(1<<20))
	assert.Equal(t, indentBytesCeiling, indentByteBudget(1<<40))
}

func TestSplitYAML_RejectedDocumentRefundsIndentBudget(t *testing.T) {
	// Each deep document needs ~800 KB of the ~1.1 MiB budget, so two do not
	// fit together. The rejected bomb in between is refunded, so the first
	// document and the small last one are accepted; the second "fit" is
	// rejected on its own overrun.
	const depth = 900
	fit := deepFlow("fit", depth, false)
	bomb := deepFlow("bomb", 5000, false)
	data := []byte(fit + "---\n" + bomb + "---\n" + strings.Replace(fit, "fit", "fit2", 1))
	budget := indentByteBudget(len(data))
	cost := 0
	for d := 0; d < depth; d++ {
		cost += indentCost * d
	}
	require.Less(t, cost, budget, "fixture: one document must fit")
	require.Greater(t, 2*cost, budget, "fixture: two documents must not fit")
	// Without the refund the bomb would drain the budget; with it the second
	// "fit" still does not fit, but a small last document does.
	data = append(data, []byte("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: last\n")...)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	var names []string
	for _, m := range manifests {
		names = append(names, m.Name)
	}
	assert.Equal(t, []string{"fit", "last"}, names)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func countNodes(n *yaml.Node) int {
	c := 1
	for _, ch := range n.Content {
		c += countNodes(ch)
	}
	return c
}

func leadingSpaces(b []byte) int {
	total := 0
	// Split on every YAML line break (LF, CR, NEL, LS, PS). Only LF, LS and PS
	// reach the output raw (CR is escaped, NEL becomes a space), but counting
	// all five only makes the bound stricter. The set is spelled out rather
	// than reusing isBreakRune, so a break dropped there cannot also hide here.
	atStart := true
	for _, r := range string(b) {
		switch {
		case r == '\n' || r == '\r' || r == '\u0085' || r == '\u2028' || r == '\u2029':
			atStart = true
		case r == ' ' && atStart:
			total++
		default:
			atStart = false
		}
	}
	return total
}

// TestIndentCharges_BoundOutputIndentation checks that what the converter
// charges to the indentation budget is at least the indentation the writers
// produce: YAML via sigs.k8s.io/yaml (as `glint render` does) and JSON with a
// two-space indent (bounded by twice the charge plus four bytes per node).
func TestIndentCharges_BoundOutputIndentation(t *testing.T) {
	chain := func(n int, open, closeS, leaf string) string {
		return strings.Repeat(open, n) + leaf + strings.Repeat(closeS, n)
	}
	long := strings.TrimSpace(strings.Repeat("word ", 120))
	multi := strings.Repeat("line of text\\n\\n", 30)
	cases := map[string]string{
		"map-chain":         chain(60, "{a: ", "}", "x"),
		"seq-chain":         chain(60, "[", "]", "x"),
		"alternating":       chain(30, "{a: [", "]}", "x"),
		"wide-at-depth":     chain(20, "{a: ", "}", "{"+strings.TrimSuffix(strings.Repeat("k: v, ", 50), ", ")+"}"),
		"multiline":         chain(40, "{a: ", "}", `"`+multi+`"`),
		"multiline-ends-nl": chain(40, "{a: ", "}", `"first\nsecond\n"`),
		"spaced-plain":      chain(40, "{a: ", "}", long),
		"spaced-quoted":     chain(40, "{a: ", "}", `"`+long+`"`),
		"deep-keys":         chain(30, "{a: ", "}", `{"`+long+`": v}`),
		"seq-of-strings":    chain(25, "[", "]", `"`+long+`", "two words\\nx"`),
		"binary-newlines":   chain(40, "{a: ", "}", "!!binary "+b64("\nx\nx\nx\nx\nx\nx")),
		"binary-spaces":     chain(40, "{a: ", "}", "!!binary "+b64("x x x x x x x x x x")),
		"literal-nel":       chain(40, "{a: ", "}", `"a\n`+strings.Repeat(`x\N`, 50)+`"`),
		"literal-ls":        chain(40, "{a: ", "}", `"a\n`+strings.Repeat(`x\L`, 50)+`"`),
		"literal-ps":        chain(40, "{a: ", "}", `"a\n`+strings.Repeat(`x\P`, 50)+`"`),
		"long-keys":         chain(30, "{a: ", "}", `{"`+strings.Repeat("k", 135)+`": v}`),
		"long-keys-nested":  chain(30, "{"+strings.Repeat("k", 135)+": ", "}", "v"),
		"multiline-keys":    chain(30, "{a: ", "}", `{"two\nlines": v, "x\ry": w}`),
		"many-break-keys":   chain(30, "{a: ", "}", `{"a\nb": v, "c\nd": v, "e\nf": v, "g\nh": v, "i\nj": v, "k\nl": v}`),
	}
	for name, flow := range cases {
		t.Run(name, func(t *testing.T) {
			node := parseNode(t, flow)
			aliasB, indent := 1<<40, 1<<40
			v, err := nodeToValueBudget(node, &aliasB, &indent)
			require.NoError(t, err)
			charged := 1<<40 - indent

			y, err := sigsyaml.Marshal(v)
			require.NoError(t, err)
			assert.LessOrEqual(t, leadingSpaces(y), charged, "yaml")

			var jb bytes.Buffer
			enc := json.NewEncoder(&jb)
			enc.SetIndent("", "  ")
			require.NoError(t, enc.Encode(v))
			assert.LessOrEqual(t, leadingSpaces(jb.Bytes()), 2*charged+4*countNodes(node), "json")
		})
	}
}
