package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"log/slog"

	"gopkg.in/yaml.v3"

	"github.com/lukashankeln/glint/internal/manifest"
)

// SplitYAML splits a multi-document YAML stream into individual Manifest objects.
// Documents without apiVersion/kind are skipped with a debug log.
// Empty documents and comment-only docs are skipped silently.
// A document that fails to decode returns an error. A document that decodes but
// fails conversion (e.g. alias limits, invalid map keys, self-referential or
// unknown anchors) is skipped with a debug log.
func SplitYAML(data []byte, appName string, sourcePath string, rendered bool) ([]manifest.Manifest, error) {
	var manifests []manifest.Manifest

	// One alias byte budget for the whole stream: see nodeToValue.
	budget := aliasByteBudget(len(data))

	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decoding YAML document: %w", err)
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
		// Only accepted documents consume the shared alias budget: a rejected
		// document's charges are refunded.
		budgetBefore := budget
		val, err := nodeToValueBudget(&node, &budget)
		if err != nil {
			budget = budgetBefore
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
// their resolved YAML tag, with anything other than !!null/!!bool/!!int/!!float
// — including !!str and !!timestamp — kept as a plain string (except plain
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
// Merge keys (<<) follow go-yaml v2 semantics: mapping pairs are processed in
// order, so a merge overwrites explicit keys before it and is overwritten by
// explicit keys after it. A merge value must be a mapping, an alias to a
// mapping, or a sequence of those (applied in reverse so earlier sources win);
// anything else is an error. Merging is shallow. Self-referential anchors
// return an error instead of recursing forever.
//
// Detection works on the yaml.v3 Node, which drops a bare non-specific "!" tag,
// so `! "<<"` (a merge key to go-yaml v2 / Kubernetes) is seen here as the
// literal key "<<". This matches the old v3 re-encode pipeline, not raw sigs.k8s.io/yaml.
//
// Alias expansion is bounded two ways. yaml.v3's excessive-aliasing heuristic
// makes a "billion laughs" document fail with errExcessiveAliasing instead of
// exhausting CPU and memory; its counters are per call, i.e. per document. A
// byte budget catches what the ratio misses: a large scalar aliased many times,
// or a small mapping aliased many times. Every scalar converted through an alias is charged len(Value) plus
// aliasedScalarCost, every aliased mapping aliasedMapCost plus aliasedEntryCost
// per pair, every aliased sequence aliasedSeqCost plus aliasedElemCost per
// element, and every entry a merge copies from an alias or inside aliased
// content aliasedMergeEntryCost; aliased typed scalars and non-string keys add
// aliasedDecodeCost and aliasedKeyFormatCost.
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
// An alias whose anchor is defined outside the given tree (yaml.v3 keeps
// anchors across Decode calls on one stream) is an "unknown anchor" error, as
// when each document was parsed alone. A mapping key that converts to a
// mapping or sequence is an "invalid map key" error, as in sigs.k8s.io/yaml.
func nodeToValue(n *yaml.Node) (any, error) {
	budget := aliasByteBudget(0)
	return nodeToValueBudget(n, &budget)
}

// nodeToValueBudget is nodeToValue with a caller-supplied alias byte budget,
// decremented in place so that several calls can share it.
func nodeToValueBudget(n *yaml.Node, budget *int) (any, error) {
	c := converter{expanding: map[*yaml.Node]bool{}, anchors: map[*yaml.Node]bool{}, aliasBytesLeft: budget}
	return c.convert(n)
}

// converter carries the per-call state of nodeToValue. expanding holds the
// alias targets currently being converted, to detect anchors that contain
// themselves. anchors records every anchored node convert has reached so far
// in this document, so an alias can be checked against it directly instead of
// pre-walking the whole tree: YAML requires an anchor to be defined (and thus
// already visited, since convert follows document order) before any alias
// references it — yaml.v3 itself rejects forward references at parse time —
// so the only way expand's lookup can miss is a target from outside this
// document, e.g. an earlier document on the same decoder stream. decodeCount,
// aliasCount and aliasDepth mirror the fields of the same names on yaml.v3's
// decoder and drive the excessive-aliasing guard.
// aliasBytesLeft is the remaining alias byte budget, possibly shared with other
// converters (one per document of a stream).
type converter struct {
	expanding   map[*yaml.Node]bool
	anchors     map[*yaml.Node]bool // anchored nodes reached so far in this document
	decodeCount int
	aliasCount  int
	aliasDepth  int
	depth       int // current mapping/sequence nesting, continuing across aliases

	aliasBytesLeft *int
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
	// goes through yaml.v3's Node.Decode (!!int, !!float, and !!bool other than
	// the YAML 1.1 spellings), which allocates a decoder per call (~130 to
	// ~240 bytes beyond the boxed value, the float being the worst; the constant keeps a margin).
	aliasedDecodeCost = 320
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
)

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
// and copies its top-level entries into m.
func (c *converter) mergeSource(m map[string]any, src *yaml.Node) error {
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
		return errMergeValue
	}
	if err != nil {
		return err
	}
	sm, ok := v.(map[string]any)
	if !ok {
		return errMergeValue
	}
	// The copy grows m past its presize, and m is usually not an aliased node
	// itself, so charge for that growth whenever the copied entries came from
	// or into aliased content.
	if src.Kind == yaml.AliasNode || c.aliasDepth > 0 {
		if err := c.charge(aliasedMergeEntryCost * len(sm)); err != nil {
			return err
		}
	}
	for k, x := range sm {
		m[k] = x
	}
	return nil
}

var errInvalidMapKey = errors.New("invalid map key: mapping or sequence")

var errMergeValue = errors.New("map merge requires map or sequence of maps as the value")

func (c *converter) merge(m map[string]any, v *yaml.Node) error {
	if v.Kind == yaml.SequenceNode {
		for i := len(v.Content) - 1; i >= 0; i-- {
			if err := c.mergeSource(m, v.Content[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return c.mergeSource(m, v)
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
		return c.convert(n.Content[0])

	case yaml.AliasNode:
		return c.expand(n)

	case yaml.MappingNode:
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
			if !ok {
				if c.aliasDepth > 0 {
					if err := c.charge(aliasedKeyFormatCost); err != nil {
						return nil, err
					}
				}
				ks = fmt.Sprint(k)
			}
			v, err := c.convert(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[ks] = v
		}
		return m, nil

	case yaml.SequenceNode:
		if c.aliasDepth > 0 {
			if err := c.charge(aliasedSeqCost + aliasedElemCost*len(n.Content) + aliasedIndentCost*c.depth); err != nil {
				return nil, err
			}
		}
		s := make([]any, len(n.Content))
		c.depth++
		defer func() { c.depth-- }()
		for i, e := range n.Content {
			v, err := c.convert(e)
			if err != nil {
				return nil, err
			}
			s[i] = v
		}
		return s, nil

	case yaml.ScalarNode:
		if c.aliasDepth > 0 {
			if err := c.charge(len(n.Value) + aliasedScalarCost + aliasedIndentCost*c.depth); err != nil {
				return nil, err
			}
		}
		switch n.Tag {
		case "!!null":
			return nil, nil
		case "!!bool":
			if b, ok := yaml11Bools[n.Value]; ok {
				return b, nil // e.g. `!!bool yes`, which yaml.v3 fails to decode
			}
			if c.aliasDepth > 0 {
				if err := c.charge(aliasedDecodeCost); err != nil {
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
				if err := c.charge(aliasedDecodeCost); err != nil {
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
