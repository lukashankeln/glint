package render

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	sigsyaml "sigs.k8s.io/yaml"
)

func sigsObject(t *testing.T, doc string) map[string]any {
	t.Helper()
	var want map[string]any
	require.NoError(t, sigsyaml.Unmarshal([]byte(doc), &want))
	return want
}

// checkAgainstSigs splits the stream and compares every document with sigs
// decoding that document alone (docs are the unjoined documents).
func checkAgainstSigs(t *testing.T, stream string, docs []string) {
	t.Helper()
	manifests, err := SplitYAML([]byte(stream), "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, len(docs))
	for i, d := range docs {
		assert.Equal(t, sigsObject(t, d), manifests[i].Object, "doc %d", i)
		var rt map[string]any
		require.NoError(t, sigsyaml.Unmarshal(manifests[i].Raw, &rt))
		assert.Equal(t, manifests[i].Object, rt, "raw round trip doc %d", i)
	}
}

func TestSplitYAML_BangMergeKeyForms(t *testing.T) {
	cases := []struct {
		name  string
		data  string
		merge bool // the merged value x must show up (documented expectation)
	}{
		{"bang double", "\n  y: 1\n  ! \"<<\": {x: 1, y: 2}\n", true},
		{"bang single", "\n  y: 1\n  ! '<<': {x: 1, y: 2}\n", true},
		{"verbatim bang", "\n  y: 1\n  !<!> \"<<\": {x: 1, y: 2}\n", true},
		{"verbatim escaped bang", "\n  y: 1\n  !<%21> \"<<\": {x: 1, y: 2}\n", true},
		{"tab separator", "\n  y: 1\n  !\t\"<<\": {x: 1, y: 2}\n", true},
		{"anchor then bang", "\n  y: 1\n  &x ! \"<<\": {x: 1, y: 2}\n", true},
		{"bang then anchor", "\n  y: 1\n  ! &x \"<<\": {x: 1, y: 2}\n", true},
		{"explicit key", "\n  y: 1\n  ? ! \"<<\"\n  : {x: 1, y: 2}\n", true},
		{"explicit key split with comments", "\n  y: 1\n  ? &x # c\n    ! # d\n    '<<'\n  : {x: 1, y: 2}\n", true},
		{"explicit literal", "\n  y: 1\n  ? ! |-\n    <<\n  : {x: 1, y: 2}\n", true},
		{"explicit folded", "\n  y: 1\n  ? ! >-\n    <<\n  : {x: 1, y: 2}\n", true},
		{"escaped scalar", "\n  y: 1\n  ! \"\\x3c<\": {x: 1, y: 2}\n", true},
		{"flow mapping", " {y: 1, ! \"<<\": {x: 1, y: 2}}\n", true},
		{"single pair in flow sequence", "\n  s: [! \"<<\": {x: 1}]\n", false},
		{"merge sequence", "\n  a: &a {x: 1}\n  b: &b {y: 2}\n  ! \"<<\": [*a, *b]\n", true},
		{"anchored mapping merged elsewhere", "\n  a: &a\n    ! \"<<\": {p: 1}\n    q: 2\n  m:\n    <<: *a\n", true},
		{"anchored mapping merged with alias value", "\n  base: &base {p: 1}\n  a: &a\n    ! \"<<\": *base\n    q: 2\n  m:\n    <<: *a\n", true},
		{"untagged double", "\n  y: 1\n  \"<<\": {x: 1, y: 2}\n", false},
		{"untagged single", "\n  y: 1\n  '<<': {x: 1, y: 2}\n", false},
		{"str tag", "\n  y: 1\n  !!str \"<<\": {x: 1, y: 2}\n", false},
		{"verbatim str tag", "\n  y: 1\n  !<tag:yaml.org,2002:str> '<<': {x: 1, y: 2}\n", false},
		{"quoted local tag", "\n  y: 1\n  !'x' \"<<\": {x: 1, y: 2}\n", false},
		{"plain bang", "\n  y: 1\n  ! <<: {x: 1, y: 2}\n", true},
		{"anchor only", "\n  y: 1\n  &x \"<<\": {x: 1, y: 2}\n", false},
		{"anchored merge key aliased as value", "\n  m: {y: 1, &k ! \"<<\": {x: 1}}\n  z: *k\n", false},
		{"anchored merge key aliased as key", "\n  m: {y: 1, &k ! \"<<\": {x: 1}}\n  n: {*k : 3}\n", false},
		{"anchored plain merge key aliased", "\n  m: {y: 1, &k <<: {x: 1}}\n  z: *k\n  n: {*k : 3}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := mergeDocHeader + "data:" + tc.data
			checkAgainstSigs(t, doc, []string{doc})
			if tc.merge {
				ms, err := SplitYAML([]byte(doc), "app", "f.yaml", false)
				require.NoError(t, err)
				data := ms[0].Object["data"].(map[string]any)
				_, hasX := data["x"]
				_, nested := data["m"]
				assert.True(t, hasX || nested || strings.Contains(tc.name, "alias"), "merge expected: %v", data)
			}
		})
	}
}

func TestSplitYAML_TagDirectiveLiteral(t *testing.T) {
	head := "%TAG !e! tag:yaml.org,2002:\n---\n"
	body := mergeDocHeader + "data:\n  y: 1\n  \"<<\": {x: 1}\n  !e!str \"<<\": {z: 1}\n"
	checkAgainstSigs(t, head+body, []string{head + body})
}

func TestSplitYAML_BangMergeKeyIssueDemo(t *testing.T) {
	doc := mergeDocHeader + "data:\n  securityContext: {privileged: false, ! \"<<\": {privileged: true}}\n"
	checkAgainstSigs(t, doc, []string{doc})
	ms, err := SplitYAML([]byte(doc), "app", "f.yaml", false)
	require.NoError(t, err)
	sc := ms[0].Object["data"].(map[string]any)["securityContext"].(map[string]any)
	assert.Equal(t, true, sc["privileged"])
}

func TestSplitYAML_BangMergeKeyPositions(t *testing.T) {
	topKey := "! \"<<\": {apiVersion: v1, kind: ConfigMap, metadata: {name: x}}\n"
	keyDoc := func(prefix string) string {
		return mergeDocHeader + "data:\n" + prefix + "  ! \"<<\": {x: 1}\n"
	}
	t.Run("utf8 bom line one", func(t *testing.T) {
		checkAgainstSigs(t, "\xef\xbb\xbf"+topKey, []string{topKey})
	})
	t.Run("crlf", func(t *testing.T) {
		d := strings.ReplaceAll(keyDoc("  a: 1\n"), "\n", "\r\n")
		checkAgainstSigs(t, d, []string{d})
	})
	t.Run("lone cr", func(t *testing.T) {
		d := strings.ReplaceAll(keyDoc("  a: 1\n"), "\n", "\r")
		checkAgainstSigs(t, d, []string{d})
	})
	for name, br := range map[string]string{"nel": "\u0085", "ls": "\u2028", "ps": "\u2029"} {
		t.Run("break "+name, func(t *testing.T) {
			d := keyDoc("  s: \"a" + br + "b\"\n  t: 'c" + br + "d'\n")
			checkAgainstSigs(t, d, []string{d})
		})
	}
	t.Run("multibyte runes", func(t *testing.T) {
		d := mergeDocHeader + "data: {é: 1, 日本: \"é日本\", ! \"<<\": {x: 1}}\n"
		checkAgainstSigs(t, d, []string{d})
	})
	t.Run("later documents", func(t *testing.T) {
		d1 := keyDoc("")
		d2 := strings.Replace(keyDoc("  é: 1\n"), "cm1", "cm2", 1)
		d3 := strings.Replace(keyDoc("  s: \"a\u2028b\"\n"), "cm1", "cm3", 1)
		checkAgainstSigs(t, d1+"---\n"+d2+"---\n"+d3, []string{d1, d2, d3})
	})
	t.Run("mid-stream bom", func(t *testing.T) {
		d := mergeDocHeader + "data: {s: \"a\xef\xbb\xbfb\", p: c\xef\xbb\xbfd, ! \"<<\": {x: 1}}\n"
		checkAgainstSigs(t, d, []string{d})
	})
	utf16Stream := func(s string, be bool) []byte {
		u := utf16.Encode([]rune("\ufeff" + s))
		out := make([]byte, 0, 2*len(u))
		for _, c := range u {
			if be {
				out = binary.BigEndian.AppendUint16(out, c)
			} else {
				out = binary.LittleEndian.AppendUint16(out, c)
			}
		}
		return out
	}
	for name, be := range map[string]bool{"utf16le": false, "utf16be": true} {
		t.Run(name, func(t *testing.T) {
			d := keyDoc("  é: \"日本\"\n  s: \"a\u2028b\"\n")
			ms, err := SplitYAML(utf16Stream(d, be), "app", "f.yaml", false)
			require.NoError(t, err)
			require.Len(t, ms, 1)
			assert.Equal(t, sigsObject(t, d), ms[0].Object)
		})
	}
}

func TestSplitYAML_BangMergeKeysLinearCost(t *testing.T) {
	for _, key := range []string{`"<<": 1`, `! "<<": {}`} {
		var b strings.Builder
		b.WriteString(mergeDocHeader + "data: {")
		for i := 0; i < 200000; i++ {
			b.WriteString(key + ", ")
		}
		b.WriteString("z: 1}\n")
		ms, err := SplitYAML([]byte(b.String()), "app", "f.yaml", false)
		require.NoError(t, err, key)
		require.Len(t, ms, 1, key)
		assert.Equal(t, float64(1), ms[0].Object["data"].(map[string]any)["z"])
	}
}

func scalarKey(line, col int, style yaml.Style) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "<<", Style: style, Line: line, Column: col}
}

func TestResolveMergeTags_FailsClosed(t *testing.T) {
	mapping := func(keys ...*yaml.Node) *yaml.Node {
		m := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range keys {
			m.Content = append(m.Content, k, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
		}
		return m
	}
	t.Run("wrong indicator", func(t *testing.T) {
		err := resolveMergeTags(newSourceCursor([]byte("x: 1\n")), mapping(scalarKey(1, 1, yaml.DoubleQuotedStyle)))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrAmbiguousMergeKey))
		assert.Contains(t, err.Error(), "line 1, column 1")
	})
	t.Run("wrong indicator after properties", func(t *testing.T) {
		err := resolveMergeTags(newSourceCursor([]byte("! 'a'\n")), mapping(scalarKey(1, 1, yaml.DoubleQuotedStyle)))
		assert.True(t, errors.Is(err, ErrAmbiguousMergeKey))
	})
	t.Run("two tags", func(t *testing.T) {
		err := resolveMergeTags(newSourceCursor([]byte("! !a \"<<\"\n")), mapping(scalarKey(1, 1, yaml.DoubleQuotedStyle)))
		assert.True(t, errors.Is(err, ErrAmbiguousMergeKey))
	})
	t.Run("behind cursor", func(t *testing.T) {
		src := []byte("{\"<<\": 1, \"<<\": 2}\n")
		err := resolveMergeTags(newSourceCursor(src), mapping(scalarKey(1, 2, yaml.DoubleQuotedStyle), scalarKey(1, 1, yaml.DoubleQuotedStyle)))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrAmbiguousMergeKey))
	})
	t.Run("past end", func(t *testing.T) {
		err := resolveMergeTags(newSourceCursor([]byte("a\n")), mapping(scalarKey(5, 1, yaml.DoubleQuotedStyle)))
		assert.True(t, errors.Is(err, ErrAmbiguousMergeKey))
		err = resolveMergeTags(newSourceCursor([]byte("a\n")), mapping(scalarKey(1, 9, yaml.DoubleQuotedStyle)))
		assert.True(t, errors.Is(err, ErrAmbiguousMergeKey))
	})
	t.Run("correct position resolves", func(t *testing.T) {
		k := scalarKey(1, 1, yaml.DoubleQuotedStyle)
		require.NoError(t, resolveMergeTags(newSourceCursor([]byte("! \"<<\": 1\n")), mapping(k)))
		assert.Equal(t, "!!merge", k.Tag)
	})
}

func TestSplitYAML_ResolverErrorIsReturned(t *testing.T) {
	orig := mergeTagResolver
	t.Cleanup(func() { mergeTagResolver = orig })
	mergeTagResolver = func(*sourceCursor, *yaml.Node) error { return ErrAmbiguousMergeKey }
	ms, err := SplitYAML([]byte(mergeDocHeader), "app", "f.yaml", false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAmbiguousMergeKey))
	assert.Nil(t, ms)
}

func TestDecodeUTF16_MatchesStdlib(t *testing.T) {
	units := append(utf16.Encode([]rune("a: é 日本 😀\n x")), 0xD800, 'b', 0xDC00)
	want := string(utf16.Decode(units))
	for _, bigEndian := range []bool{false, true} {
		b := make([]byte, 2*len(units))
		for i, u := range units {
			if bigEndian {
				binary.BigEndian.PutUint16(b[2*i:], u)
			} else {
				binary.LittleEndian.PutUint16(b[2*i:], u)
			}
		}
		assert.Equal(t, want, string(decodeUTF16(b, bigEndian)), "bigEndian=%v", bigEndian)
		assert.Equal(t, want, string(decodeUTF16(append(b, 0x41), bigEndian)), "odd trailing byte, bigEndian=%v", bigEndian)
	}
}
