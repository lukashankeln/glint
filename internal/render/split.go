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
// A malformed document returns an error rather than being dropped.
func SplitYAML(data []byte, appName string, sourcePath string, rendered bool) ([]manifest.Manifest, error) {
	var manifests []manifest.Manifest

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
		val, err := nodeToValue(&node)
		if err != nil {
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
// — including !!str and !!timestamp — kept as a plain string, matching JSON's
// lack of a native date/time type and avoiding go-yaml's generic-decode
// behavior of turning bare dates into time.Time.
//
// Merge keys (<<) follow go-yaml v2 semantics: mapping pairs are processed in
// order, so a merge overwrites explicit keys before it and is overwritten by
// explicit keys after it. A merge value must be a mapping, an alias to a
// mapping, or a sequence of those (applied in reverse so earlier sources win);
// anything else is an error. Merging is shallow. Self-referential anchors
// return an error instead of recursing forever.
func nodeToValue(n *yaml.Node) (any, error) {
	c := converter{expanding: map[*yaml.Node]bool{}}
	return c.convert(n)
}

// converter carries the per-call state of nodeToValue. expanding holds the
// alias targets currently being converted, to detect anchors that contain
// themselves.
type converter struct {
	expanding map[*yaml.Node]bool
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
	if c.expanding[target] {
		name := target.Anchor
		if name == "" {
			name = alias.Value
		}
		return nil, fmt.Errorf("anchor %q value contains itself", name)
	}
	c.expanding[target] = true
	defer delete(c.expanding, target)
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
	for k, x := range sm {
		m[k] = x
	}
	return nil
}

var errMergeValue = fmt.Errorf("map merge requires map or sequence of maps as the value")

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
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return c.convert(n.Content[0])

	case yaml.AliasNode:
		return c.expand(n)

	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
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
			ks, ok := k.(string)
			if !ok {
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
		s := make([]any, len(n.Content))
		for i, e := range n.Content {
			v, err := c.convert(e)
			if err != nil {
				return nil, err
			}
			s[i] = v
		}
		return s, nil

	case yaml.ScalarNode:
		switch n.Tag {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int", "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, err
			}
			return f, nil
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
