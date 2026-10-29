package render

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"strings"
	"unicode/utf8"

	"log/slog"

	"gopkg.in/yaml.v3"

	"github.com/lukashankeln/glint/internal/manifest"
)

// Stream limits. They are variables so tests can lower them; such tests must
// not run in parallel.
var (
	maxStreamBytes     = 64 << 20
	maxStreamDocuments = 10000

	errStreamTooLarge   = errors.New("YAML stream too large")
	errTooManyDocuments = errors.New("YAML stream has too many documents")
)

// SplitYAML splits a multi-document YAML stream into individual Manifest objects.
// Documents without apiVersion/kind are skipped with a debug log.
// Empty documents and comment-only docs are skipped silently.
// A stream over maxStreamBytes, or with more than maxStreamDocuments
// documents (empty ones included), returns an error wrapping errStreamTooLarge
// or errTooManyDocuments. A document that fails to decode returns an error.
// A document that decodes but fails conversion (e.g. alias limits, the
// nesting/indentation budget, invalid map keys, self-referential or unknown
// anchors) is skipped with a debug log.
func SplitYAML(data []byte, appName string, sourcePath string, rendered bool) ([]manifest.Manifest, error) {
	if len(data) > maxStreamBytes {
		return nil, fmt.Errorf("YAML stream is %d bytes, over the %d-byte limit: %w",
			len(data), maxStreamBytes, errStreamTooLarge)
	}

	var manifests []manifest.Manifest

	// One alias byte budget for the whole stream: see nodeToValue.
	budget := aliasByteBudget(len(data))
	// And one nesting/indentation budget, likewise shared.
	indentBudget := indentByteBudget(len(data))

	dec := yaml.NewDecoder(bytes.NewReader(data))
	docCount := 0
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decoding YAML document: %w", err)
		}
		docCount++
		if docCount > maxStreamDocuments {
			return nil, fmt.Errorf("YAML stream has more than %d documents: %w",
				maxStreamDocuments, errTooManyDocuments)
		}
		if node.Kind == 0 {
			continue // empty doc
		}

		// Re-encode the single document (needed for Manifest.Raw).
		docBytes, err := yaml.Marshal(&node)
		if err != nil {
			continue
		}

		// Build the JSON-compatible object straight from the already-parsed
		// Node tree instead of re-parsing docBytes through a second YAML/JSON
		// library: nodeToValue walks the tree once in memory, avoiding the
		// yaml.v2-decode + json.Marshal + json.Unmarshal chain that
		// sigs.k8s.io/yaml performs internally on every document.
		// Only accepted documents consume the shared budgets: a rejected
		// document's charges are refunded.
		budgetBefore, indentBefore := budget, indentBudget
		val, err := nodeToValueBudget(&node, &budget, &indentBudget)
		if err != nil {
			budget, indentBudget = budgetBefore, indentBefore
			slog.Debug("skipping document", "app", appName, "err", err)
			continue
		}
		obj, ok := val.(map[string]any)
		if !ok {
			if val == nil {
				continue // comment-only or blank (e.g. a bare "null" document)
			}
			slog.Debug("skipping document: not a mapping", "app", appName)
			continue
		}

		apiVersion, _ := obj["apiVersion"].(string)
		kind, _ := obj["kind"].(string)

		if apiVersion == "" || kind == "" {
			slog.Debug("skipping document without apiVersion/kind", "app", appName, "source", sourcePath)
			continue
		}

		name, namespace := extractMeta(obj)

		manifests = append(manifests, manifest.Manifest{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
			Namespace:  namespace,
			Raw:        docBytes,
			SourceApp:  appName,
			SourcePath: sourcePath,
			Rendered:   rendered,
			Object:     obj,
		})
	}

	return manifests, nil
}

// nodeToValue converts a parsed yaml.Node tree into the same JSON-compatible
// shape sigs.k8s.io/yaml produces (map[string]any / []any / string / float64 /
// bool / nil), without re-parsing the document as text. Scalars are typed by
// their resolved YAML tag, with anything other than
// !!null/!!bool/!!int/!!float/!!binary — including !!str and !!timestamp —
// kept as a plain string (except plain
// YAML 1.1 bool literals, below), matching JSON's lack of a native date/time
// type and avoiding go-yaml's generic-decode behavior of turning bare dates
// into time.Time.
//
// Plain (unquoted, untagged) YAML 1.1 bool literals that yaml.v3 resolves as
// strings — y/yes/on/n/no/off in the case forms go-yaml v2 accepts — become
// bools, as in sigs.k8s.io/yaml and Kubernetes; as mapping keys they become
// "true"/"false". An explicit !!bool tag accepts them in any style. Quoted,
// !!str-tagged and block scalars stay strings. yaml.v3
// drops a bare "!" tag, so `! yes` is seen as plain and becomes true, matching
// the old v3 re-encode pipeline rather than raw sigs.k8s.io/yaml.
//
// A !!null scalar must be one of the YAML 1.1 null literals ("", ~, null, Null,
// NULL, in any style); `!!null yes` is an error. A !!binary scalar is
// base64-decoded (invalid data is an error) and each invalid UTF-8 byte of the
// result becomes U+FFFD, as after the old JSON round trip. Two !!binary keys
// that collapse to the same string keep the later one in document order; the
// old pipeline kept the one whose raw key bytes sorted last, a difference
// not matched here. A non-finite float (.inf, -.inf, .nan) is an error as a
// value, because JSON cannot carry it, but is accepted as a mapping key, named
// ".inf", "-.inf" or ".nan". A document whose root is such a scalar is skipped
// because conversion fails, rather than for not being a mapping.
//
// Merge keys (<<) follow go-yaml v2 semantics: mapping pairs are processed in
// order, so a merge overwrites explicit keys before it and is overwritten by
// explicit keys after it. A merge value must be a mapping, an alias to a
// mapping, or a sequence of those (converted in document order, then copied so
// earlier sources win); anything else is an error. Merging is shallow.
// Self-referential anchors return an error instead of recursing forever.
//
// Detection works on the yaml.v3 Node, which drops a bare non-specific "!" tag,
// so `! "<<"` (a merge key to go-yaml v2 / Kubernetes) is seen here as the
// literal key "<<". This matches the old v3 re-encode pipeline, not raw sigs.k8s.io/yaml.
//
// Alias expansion is bounded two ways. yaml.v3's excessive-aliasing heuristic
// makes a "billion laughs" document fail with errExcessiveAliasing instead of
// exhausting CPU and memory; its counters are per call, i.e. per document. A
// byte budget catches what the ratio misses: a large scalar aliased many times,
// or a small mapping aliased many times. Every scalar converted through an
// alias is charged len(Value) plus aliasedScalarCost, every aliased mapping
// aliasedMapCost plus aliasedEntryCost per pair, every aliased sequence
// aliasedSeqCost plus aliasedElemCost per element, and every entry a merge
// copies from an alias or inside aliased content aliasedMergeEntryCost;
// aliased typed scalars add aliasedDecodeCost plus 2*len(n.Value) (aliased
// !!binary scalars aliasedBinaryCost plus 3*len(n.Value) instead), and
// non-string keys add aliasedKeyFormatCost.
// These are shaped like the heap the conversion allocates, so the budget bounds
// memory and not just output size. On top of that comes 2 bytes per level of
// nesting (aliasedIndentCost, the indentation the output writes on each line;
// an aliased target's depth adds to the alias site's), and exceeding the budget
// fails with errExcessiveAliasExpansion. Here the budget is the floor
// (aliasedBytesFloor); SplitYAML shares one budget, aliasByteBudget(len(data)),
// that scales with the input up to aliasedBytesCeiling and is shared across the
// documents of a stream, so the result depends on order: a later document can
// be the one that is skipped. A rejected document's charges are
// refunded, so only accepted documents consume the shared budget.
//
// Nesting is bounded separately, because the output writers (YAML and JSON)
// indent every line by its depth, so a deeply nested document, aliased or not,
// writes output quadratic in its depth, and go-yaml v2 (behind
// sigs.k8s.io/yaml) also repeats that indentation on every line of a multi-line
// string and wraps long strings at spaces. Every mapping is charged
// indentCost per pair per level of depth, every sequence indentCost per element
// per level, and every scalar (keys included) indentCost per level for each
// extra line it may write: each non-blank line after a line break (LF, CR, NEL,
// LS or PS) and the first line of a string that contains one, and each space.
// A !!binary scalar is also charged on its decoded value, and a key longer
// than 128 bytes or containing a break is charged one more line for the
// separate ": value" line. This is separate from, and in addition to, the alias
// charges above.
// Exceeding it fails with errExcessiveNesting. The budget is
// indentByteBudget(len(data)) in SplitYAML (floor indentBytesFloor, ceiling
// indentBytesCeiling), shared across a stream and refunded for a rejected
// document like the alias budget; nodeToValue uses the floor.
//
// An alias whose anchor is defined outside the given tree (yaml.v3 keeps
// anchors across Decode calls on one stream) is an "unknown anchor" error, as
// when each document was parsed alone. A mapping key that converts to a
// mapping or sequence is an "invalid map key" error, as in sigs.k8s.io/yaml.
func nodeToValue(n *yaml.Node) (any, error) {
	aliasBudget := aliasByteBudget(0)
	indentBudget := indentByteBudget(0)
	return nodeToValueBudget(n, &aliasBudget, &indentBudget)
}

// nodeToValueBudget is nodeToValue with caller-supplied alias and
// nesting/indentation byte budgets, decremented in place so that several calls
// can share them.
func nodeToValueBudget(n *yaml.Node, aliasBudget, indentBudget *int) (any, error) {
	c := converter{expanding: map[*yaml.Node]bool{}, anchors: map[*yaml.Node]bool{},
		aliasBytesLeft: aliasBudget, indentBytesLeft: indentBudget}
	return c.convert(n)
}

// converter carries the per-call state of nodeToValue. expanding holds the
// alias targets currently being converted, to detect anchors that contain
// themselves. anchors records every anchored node convert has reached so far
// in this document, so an alias can be checked against it directly instead of
// pre-walking the whole tree: YAML requires an anchor to be defined (and thus
// already visited, since convert follows document order, merge sequences
// included) before any alias references it — yaml.v3 itself rejects
// forward references at parse time — so the only way expand's lookup can miss is a target from outside this
// document, e.g. an earlier document on the same decoder stream. decodeCount,
// aliasCount and aliasDepth mirror the fields of the same names on yaml.v3's
// decoder and drive the excessive-aliasing guard.
// aliasBytesLeft is the remaining alias byte budget, possibly shared with other
// converters (one per document of a stream); indentBytesLeft likewise for the
// nesting/indentation budget.
type converter struct {
	expanding   map[*yaml.Node]bool
	anchors     map[*yaml.Node]bool // anchored nodes reached so far in this document
	decodeCount int
	aliasCount  int
	aliasDepth  int
	depth       int // current mapping/sequence nesting, continuing across aliases

	aliasBytesLeft  *int
	indentBytesLeft *int
}

// yaml11Bools holds the go-yaml v2 (YAML 1.1) bool literals that yaml.v3's
// YAML 1.2 resolver tags !!str. true/false are already !!bool in yaml.v3.
var yaml11Bools = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true,
	"on": true, "On": true, "ON": true,
	"n": false, "N": false, "no": false, "No": false, "NO": false,
	"off": false, "Off": false, "OFF": false,
}

// nonPlainStyle marks a scalar whose presentation pins it to a string.
const nonPlainStyle = yaml.TaggedStyle | yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle |
	yaml.LiteralStyle | yaml.FoldedStyle

var errExcessiveAliasing = errors.New("document contains excessive aliasing")

var errExcessiveNesting = errors.New("document nests too deeply for its size")

var errExcessiveAliasExpansion = errors.New("document expands too many bytes through aliases")

const (
	// aliasedBytesFloor is the minimum alias byte budget.
	aliasedBytesFloor = 1 << 20
	// aliasedBytesPerInputByte scales the budget with the size of the input.
	aliasedBytesPerInputByte = 10
	// aliasedBytesCeiling caps the scaled budget, so a huge input cannot buy an
	// unbounded amount of alias expansion.
	aliasedBytesCeiling = 64 << 20

	// The costs below approximate the heap nodeToValue allocates for aliased
	// content (measured on Go 1.27 as marginal runtime.MemStats.TotalAlloc per
	// alias, see TestAliasCharges_CoverHeapAllocation) and are rounded up, so
	// the budget bounds heap rather than serialized bytes.

	// aliasedMapCost is charged for each aliased mapping: the map header and
	// the first swiss-map group (a one-pair map allocates ~370 bytes in all,
	// of which ~100 are charged as its scalars).
	aliasedMapCost = 256
	// aliasedEntryCost is charged per key/value pair of an aliased mapping,
	// for its slot in the map (tables of presized maps run ~120 bytes per
	// entry including the boxed key and value, which are charged as scalars).
	aliasedEntryCost = 32
	// aliasedSeqCost is charged for each aliased sequence: slice header plus
	// the slot in its parent.
	aliasedSeqCost = 48
	// aliasedElemCost is charged per element of an aliased sequence, for its
	// 16-byte interface slot.
	aliasedElemCost = 16
	// aliasedScalarCost is charged per aliased scalar on top of len(Value): the
	// boxed value (~16 bytes for a string), rounded up. The interface slot
	// holding it is charged by the parent (aliasedElemCost, aliasedEntryCost).
	aliasedScalarCost = 48
	// aliasedDecodeCost is charged on top of that for an aliased scalar that
	// goes through yaml.v3's Node.Decode (!!int, !!float, and !!bool other
	// than the YAML 1.1 spellings), which allocates a decoder per call
	// (measured: ~279 bytes beyond the boxed value for the float 1.5, the
	// worst of the three; the constant keeps a margin). The resolver also
	// copies the input in failed strconv attempts, so 2*len(Value) more is
	// charged per decoded scalar (len(Value) itself is already charged once
	// as a scalar); that extra 2*len covers the decode allocation (~2*len).
	aliasedDecodeCost = 320
	// aliasedBinaryCost is charged for an aliased !!binary scalar, on top of
	// 3*len(Value) (len(Value) itself is already charged once as a scalar).
	// Base64 decoding allocates ~0.75*len, and the UTF-8 fix-up builder is
	// sized for the worst case of every byte invalid, 3 output bytes per
	// decoded byte (2.25*len), so ~3*len in all. The constant is the fixed
	// part: the builder header and the boxed string, with margin.
	aliasedBinaryCost = 64
	// aliasedKeyFormatCost is charged per aliased mapping key that is not a
	// string and so is formatted with fmt.Sprint.
	aliasedKeyFormatCost = 64
	// aliasedMergeEntryCost is charged per entry a merge copies out of an
	// aliased mapping (or into aliased content): the outer mapping is not
	// charged itself and grows past its presize, which costs up to ~180 bytes
	// per entry, garbage from the doubling tables included.
	aliasedMergeEntryCost = 192
	// aliasedIndentCost is charged per level of nesting of an aliased node.
	aliasedIndentCost = 2

	// indentBytesFloor is the minimum nesting/indentation budget.
	indentBytesFloor = 1 << 20
	// indentBytesPerInputByte scales that budget with the size of the input.
	indentBytesPerInputByte = 10
	// indentBytesCeiling caps the scaled budget. It is higher than the alias
	// ceiling because non-aliased indentation is ordinary output: a legitimate
	// stream near the 64 MiB input cap can need tens of MiB of indentation,
	// mostly per-space charges on prose.
	indentBytesCeiling = 256 << 20
	// indentCost is charged per level of depth for each line a node adds to the
	// output (the indentation the writers put on it).
	indentCost = 2
	// maxSimpleKeyLen is the longest key go-yaml v2 writes inline as "key: v".
	maxSimpleKeyLen = 128
)

// indentByteBudget returns the nesting/indentation budget for an input of
// inputLen bytes.
func indentByteBudget(inputLen int) int {
	return min(max(indentBytesFloor, indentBytesPerInputByte*inputLen), indentBytesCeiling)
}

// chargeIndent deducts n bytes from the nesting/indentation budget, failing
// once it is overdrawn.
func (c *converter) chargeIndent(n int) error {
	*c.indentBytesLeft -= n
	if *c.indentBytesLeft < 0 {
		return errExcessiveNesting
	}
	return nil
}

// isBreakRune reports whether r is a line break for go-yaml v2's writer: LF,
// CR, NEL, LS or PS.
func isBreakRune(r rune) bool {
	return r == '\n' || r == '\r' || r == '\u0085' || r == '\u2028' || r == '\u2029'
}

// extraLines counts the lines a string can add to the output beyond the key's
// own line that carry indentation: each line that starts after a line break
// (see isBreakRune) and is not blank (a trailing break starts nothing), plus
// each space, where the writer may wrap. A string with a break is written as a
// literal block whose first line also starts on a new line, so that line counts
// too.
func extraLines(s string) int {
	n := strings.Count(s, " ")
	prevBreak, hasBreak, startsBreak := false, false, false
	for i, r := range s {
		br := isBreakRune(r)
		if i == 0 {
			startsBreak = br
		}
		if br {
			hasBreak = true
		} else if prevBreak {
			n++
		}
		prevBreak = br
	}
	if hasBreak && !startsBreak {
		n++
	}
	return n
}

// aliasByteBudget returns the alias byte budget for an input of inputLen bytes.
func aliasByteBudget(inputLen int) int {
	return min(max(aliasedBytesFloor, aliasedBytesPerInputByte*inputLen), aliasedBytesCeiling)
}

// charge deducts n bytes from the alias budget, failing once it is overdrawn.
func (c *converter) charge(n int) error {
	*c.aliasBytesLeft -= n
	if *c.aliasBytesLeft < 0 {
		return errExcessiveAliasExpansion
	}
	return nil
}

// Ported from gopkg.in/yaml.v3 v3.0.1 decode.go (Copyright (c) 2011-2019 Canonical Ltd,
// Apache License 2.0, http://www.apache.org/licenses/LICENSE-2.0): the constants and
// allowedAliasRatio are copied unchanged; the decodeCount/aliasCount/aliasDepth
// accounting in convert and expand is adapted from (*decoder).unmarshal and
// (*decoder).alias.
const (
	// 400,000 decode operations is ~500kb of dense object declarations, or
	// ~5kb of dense object declarations with 10000% alias expansion
	alias_ratio_range_low = 400000

	// 4,000,000 decode operations is ~5MB of dense object declarations, or
	// ~4.5MB of dense object declarations with 10% alias expansion
	alias_ratio_range_high = 4000000

	// alias_ratio_range is the range over which we scale allowed alias ratios
	alias_ratio_range = float64(alias_ratio_range_high - alias_ratio_range_low)
)

func allowedAliasRatio(decodeCount int) float64 {
	switch {
	case decodeCount <= alias_ratio_range_low:
		// allow 99% to come from alias expansion for small-to-medium documents
		return 0.99
	case decodeCount >= alias_ratio_range_high:
		// allow 10% to come from alias expansion for very large documents
		return 0.10
	default:
		// scale smoothly from 99% down to 10% over the range.
		// this maps to 396,000 - 400,000 allowed alias-driven decodes over the range.
		// 400,000 decode operations is ~100MB of allocations in worst-case scenarios (single-item maps).
		return 0.99 - 0.89*(float64(decodeCount-alias_ratio_range_low)/alias_ratio_range)
	}
}

// isMergeKey mirrors yaml.v3's own merge-key detection, so a quoted "<<" or a
// "<<" with an explicit non-merge tag stays a literal key.
func isMergeKey(n *yaml.Node) bool {
	if n.Kind != yaml.ScalarNode || n.Value != "<<" {
		return false
	}
	switch n.Tag {
	case "", "!", "!!merge", "tag:yaml.org,2002:merge":
		return true
	}
	return false
}

// expand converts the target of an alias node, guarding against cycles.
func (c *converter) expand(alias *yaml.Node) (any, error) {
	target := alias.Alias
	if target == nil {
		return nil, fmt.Errorf("alias %q has no target", alias.Value)
	}
	if !c.anchors[target] {
		return nil, fmt.Errorf("unknown anchor %q referenced", alias.Value)
	}
	if c.expanding[target] {
		name := target.Anchor
		if name == "" {
			name = alias.Value
		}
		return nil, fmt.Errorf("anchor %q value contains itself", name)
	}
	c.expanding[target] = true
	c.aliasDepth++
	defer func() {
		c.aliasDepth--
		delete(c.expanding, target)
	}()
	return c.convert(target)
}

// mergeSource converts one merge source (a mapping or an alias to a mapping)
// and returns its top-level entries, charged against the aliasing budget. The
// caller copies them into the merged map.
func (c *converter) mergeSource(src *yaml.Node) (map[string]any, error) {
	var (
		v   any
		err error
	)
	switch {
	case src.Kind == yaml.MappingNode:
		v, err = c.convert(src)
	case src.Kind == yaml.AliasNode && src.Alias != nil && src.Alias.Kind == yaml.MappingNode:
		v, err = c.expand(src)
	default:
		return nil, errMergeValue
	}
	if err != nil {
		return nil, err
	}
	sm, ok := v.(map[string]any)
	if !ok {
		return nil, errMergeValue
	}
	// The copy grows m past its presize, and m is usually not an aliased node
	// itself, so charge for that growth whenever the copied entries came from
	// or into aliased content.
	if src.Kind == yaml.AliasNode || c.aliasDepth > 0 {
		if err := c.charge(aliasedMergeEntryCost * len(sm)); err != nil {
			return nil, err
		}
	}
	return sm, nil
}

var errInvalidMapKey = errors.New("invalid map key: mapping or sequence")

var (
	// errNonFiniteFloat rejects .inf/.nan as a value, as encoding/json does.
	errNonFiniteFloat = errors.New("unsupported value: non-finite float")
	errInvalidBinary  = errors.New("!!binary value contains invalid base64 data")
	errInvalidNull    = errors.New("!!null value is not a null literal")
)

// nullLiterals are the scalars go-yaml v2 resolves as null (YAML 1.1), in any
// style; an explicit !!null tag accepts only these.
var nullLiterals = map[string]bool{"": true, "~": true, "null": true, "Null": true, "NULL": true}

// convertValue is convert for a value position (mapping value, sequence
// element, document root): a non-finite float there is an error, because JSON
// cannot carry it. Keys go through convert and are named by formatKey.
func (c *converter) convertValue(n *yaml.Node) (any, error) {
	v, err := c.convert(n)
	if err != nil {
		return nil, err
	}
	if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
		return nil, fmt.Errorf("%w: %v", errNonFiniteFloat, f)
	}
	return v, nil
}

// decodeBinary decodes a !!binary scalar like go-yaml v2 followed by a JSON
// round trip: base64 (newlines ignored), then each invalid UTF-8 byte becomes
// U+FFFD, one per byte as encoding/json does (strings.ToValidUTF8 would
// collapse a run into one).
func decodeBinary(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", errInvalidBinary
	}
	if utf8.Valid(raw) {
		return string(raw), nil
	}
	var b strings.Builder
	b.Grow(3 * len(raw)) // worst case: every byte invalid, 1 -> 3 bytes
	for i := 0; i < len(raw); {
		r, size := utf8.DecodeRune(raw[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString("\uFFFD")
		} else {
			b.Write(raw[i : i+size])
		}
		i += size
	}
	return b.String(), nil
}

// formatKey names a non-string mapping key. Non-finite floats use the YAML
// spellings sigs.k8s.io/yaml gives them.
func formatKey(k any) string {
	if f, ok := k.(float64); ok {
		switch {
		case math.IsInf(f, 1):
			return ".inf"
		case math.IsInf(f, -1):
			return "-.inf"
		case math.IsNaN(f):
			return ".nan"
		}
	}
	return fmt.Sprint(k)
}

var errMergeValue = errors.New("map merge requires map or sequence of maps as the value")

// merge applies the merge value v to m. A sequence's sources are converted in
// document order, as yaml.v3's forward merge iteration does (which the
// aliasing guard mirrors, and which lets a later source use an anchor defined
// in an earlier one), then copied in reverse so earlier sources win.
func (c *converter) merge(m map[string]any, v *yaml.Node) error {
	if v.Kind == yaml.SequenceNode {
		srcs := make([]map[string]any, 0, len(v.Content))
		for _, n := range v.Content {
			sm, err := c.mergeSource(n)
			if err != nil {
				return err
			}
			srcs = append(srcs, sm)
		}
		for i := len(srcs) - 1; i >= 0; i-- {
			maps.Copy(m, srcs[i])
		}
		return nil
	}
	sm, err := c.mergeSource(v)
	if err != nil {
		return err
	}
	maps.Copy(m, sm)
	return nil
}

func (c *converter) convert(n *yaml.Node) (any, error) {
	if n.Anchor != "" {
		c.anchors[n] = true
	}
	c.decodeCount++
	if c.aliasDepth > 0 {
		c.aliasCount++
	}
	if c.aliasCount > 100 && c.decodeCount > 1000 && float64(c.aliasCount)/float64(c.decodeCount) > allowedAliasRatio(c.decodeCount) {
		return nil, errExcessiveAliasing
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return c.convertValue(n.Content[0])

	case yaml.AliasNode:
		return c.expand(n)

	case yaml.MappingNode:
		if err := c.chargeIndent(indentCost * c.depth * (len(n.Content) / 2)); err != nil {
			return nil, err
		}
		if c.aliasDepth > 0 {
			if err := c.charge(aliasedMapCost + aliasedEntryCost*(len(n.Content)/2) + aliasedIndentCost*c.depth); err != nil {
				return nil, err
			}
		}
		m := make(map[string]any, len(n.Content)/2)
		c.depth++
		defer func() { c.depth-- }()
		for i := 0; i+1 < len(n.Content); i += 2 {
			if isMergeKey(n.Content[i]) {
				if err := c.merge(m, n.Content[i+1]); err != nil {
					return nil, err
				}
				continue
			}
			k, err := c.convert(n.Content[i])
			if err != nil {
				return nil, err
			}
			switch k.(type) {
			case map[string]any, []any:
				return nil, errInvalidMapKey
			}
			ks, ok := k.(string)
			if ok && (len(ks) > maxSimpleKeyLen || strings.ContainsFunc(ks, isBreakRune)) {
				// go-yaml v2 writes such a key as "? key" and the value on a
				// separate ": value" line.
				if err := c.chargeIndent(indentCost * c.depth); err != nil {
					return nil, err
				}
			}
			if !ok {
				if c.aliasDepth > 0 {
					if err := c.charge(aliasedKeyFormatCost); err != nil {
						return nil, err
					}
				}
				ks = formatKey(k)
			}
			v, err := c.convertValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[ks] = v
		}
		return m, nil

	case yaml.SequenceNode:
		if err := c.chargeIndent(indentCost * c.depth * len(n.Content)); err != nil {
			return nil, err
		}
		if c.aliasDepth > 0 {
			if err := c.charge(aliasedSeqCost + aliasedElemCost*len(n.Content) + aliasedIndentCost*c.depth); err != nil {
				return nil, err
			}
		}
		s := make([]any, len(n.Content))
		c.depth++
		defer func() { c.depth-- }()
		for i, e := range n.Content {
			v, err := c.convertValue(e)
			if err != nil {
				return nil, err
			}
			s[i] = v
		}
		return s, nil

	case yaml.ScalarNode:
		if c.depth > 0 {
			if err := c.chargeIndent(indentCost * c.depth * extraLines(n.Value)); err != nil {
				return nil, err
			}
		}
		if c.aliasDepth > 0 {
			if err := c.charge(len(n.Value) + aliasedScalarCost + aliasedIndentCost*c.depth); err != nil {
				return nil, err
			}
		}
		switch n.Tag {
		case "!!null":
			if !nullLiterals[n.Value] {
				return nil, errInvalidNull
			}
			return nil, nil
		case "!!binary":
			if c.aliasDepth > 0 {
				if err := c.charge(aliasedBinaryCost + 3*len(n.Value)); err != nil {
					return nil, err
				}
			}
			d, err := decodeBinary(n.Value)
			if err != nil {
				return nil, err
			}
			// The writer emits the decoded text, not the base64.
			if c.depth > 0 {
				if err := c.chargeIndent(indentCost * c.depth * extraLines(d)); err != nil {
					return nil, err
				}
			}
			return d, nil
		case "!!bool":
			if b, ok := yaml11Bools[n.Value]; ok {
				return b, nil // e.g. `!!bool yes`, which yaml.v3 fails to decode
			}
			if c.aliasDepth > 0 {
				if err := c.charge(aliasedDecodeCost + 2*len(n.Value)); err != nil {
					return nil, err
				}
			}
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int", "!!float":
			if c.aliasDepth > 0 {
				if err := c.charge(aliasedDecodeCost + 2*len(n.Value)); err != nil {
					return nil, err
				}
			}
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, err
			}
			return f, nil
		case "!!str":
			if n.Style&nonPlainStyle == 0 {
				if b, ok := yaml11Bools[n.Value]; ok {
					return b, nil
				}
			}
			return n.Value, nil
		default:
			return n.Value, nil
		}

	default:
		return nil, fmt.Errorf("unsupported YAML node kind %v", n.Kind)
	}
}

// extractMeta extracts name and namespace from a Kubernetes object map.
func extractMeta(obj map[string]any) (name, namespace string) {
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return "", ""
	}
	name, _ = meta["name"].(string)
	namespace, _ = meta["namespace"].(string)
	return name, namespace
}

// mergeValues deep-merges src into dst. Values in src override dst.
func mergeValues(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any)
	}
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstMap, ok := dst[k].(map[string]any); ok {
				dst[k] = mergeValues(dstMap, srcMap)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

// helmSetToValues converts a flat map[string]string of --set overrides to a nested map.
// Keys use dot notation (e.g. "image.tag" -> {"image": {"tag": "..."}}).
func helmSetToValues(set map[string]string) map[string]any {
	result := make(map[string]any)
	for k, v := range set {
		setNestedValue(result, k, v)
	}
	return result
}

func setNestedValue(m map[string]any, key, value string) {
	parts := splitKey(key)
	if len(parts) == 1 {
		m[key] = value
		return
	}
	sub, ok := m[parts[0]].(map[string]any)
	if !ok {
		sub = make(map[string]any)
	}
	setNestedValue(sub, joinKey(parts[1:]), value)
	m[parts[0]] = sub
}

func splitKey(key string) []string {
	var parts []string
	var cur []byte
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			parts = append(parts, string(cur))
			cur = cur[:0]
		} else {
			cur = append(cur, key[i])
		}
	}
	parts = append(parts, string(cur))
	return parts
}

func joinKey(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(p)
	}
	return b.String()
}
