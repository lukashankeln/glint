package render

import (
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

// oldPipelineObject reproduces SplitYAML's object decoding from before
// nodeToValue: yaml.v3 re-encode of the parsed node, then sigs.k8s.io/yaml.
func oldPipelineObject(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &node))
	docBytes, err := yaml.Marshal(&node)
	require.NoError(t, err)
	var obj map[string]any
	require.NoError(t, sigsyaml.Unmarshal(docBytes, &obj))
	return obj
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

	budget := cost
	v, err := nodeToValueBudget(parseNode(t, doc), &budget)
	require.NoError(t, err)
	assert.Equal(t, []any{"abc", "abc"}, v)
	assert.Equal(t, 0, budget)

	budget = cost - 1
	_, err = nodeToValueBudget(parseNode(t, doc), &budget)
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

	_, err := nodeToValue(parseNode(t, b.String()))
	require.ErrorIs(t, err, errExcessiveAliasExpansion)
}

func TestNodeToValueBudget_NodeChargesOnly(t *testing.T) {
	// Aliased empty mappings and sequences carry no scalars: each alias, one
	// level deep, charges its base cost plus aliasedIndentCost.
	const n = 30
	for anchored, base := range map[string]int{"{}": aliasedMapCost, "[]": aliasedSeqCost} {
		doc := "[&e " + anchored + strings.Repeat(", *e", n) + "]"
		total := n * (base + aliasedIndentCost)

		budget := total
		_, err := nodeToValueBudget(parseNode(t, doc), &budget)
		require.NoError(t, err, anchored)
		assert.Equal(t, 0, budget, anchored)

		budget = total - 1
		_, err = nodeToValueBudget(parseNode(t, doc), &budget)
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
		tc{"tagged-bool", "&a !!bool yes", "*a"},
		tc{"map-int-values-1", typed(1, false), "*a"},
		tc{"map-int-values-8", typed(8, false), "*a"},
		tc{"map-int-keys-1", typed(1, true), "*a"},
		tc{"map-int-keys-8", typed(8, true), "*a"},
		tc{"scalar-empty", "&a \"\"", "*a"},
		tc{"scalar-5", "&a hello", "*a"},
		tc{"scalar-100", "&a " + strings.Repeat("x", 100), "*a"},
	)

	measure := func(node *yaml.Node) (alloc uint64, charged int) {
		budget := 1 << 40
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		v, err := nodeToValueBudget(node, &budget)
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
			// to a measurement: the minimum over several repeats is the
			// closest to the conversion's own allocation.
			alloc, charged := ^uint64(0), 0
			for range 5 {
				allocS, chargedS := measure(doc(n))
				allocL, chargedL := measure(doc(2 * n))
				if allocL < allocS {
					continue
				}
				alloc = min(alloc, (allocL-allocS)/n)
				charged = (chargedL - chargedS) / n
			}
			t.Logf("per alias: allocated %d, charged %d", alloc, charged)
			assert.LessOrEqual(t, alloc, uint64(charged))
		})
	}
}
