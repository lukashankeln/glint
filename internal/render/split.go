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
func nodeToValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return nodeToValue(n.Content[0])

	case yaml.AliasNode:
		return nodeToValue(n.Alias)

	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, err := nodeToValue(n.Content[i])
			if err != nil {
				return nil, err
			}
			ks, ok := k.(string)
			if !ok {
				ks = fmt.Sprint(k)
			}
			v, err := nodeToValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[ks] = v
		}
		return m, nil

	case yaml.SequenceNode:
		s := make([]any, len(n.Content))
		for i, c := range n.Content {
			v, err := nodeToValue(c)
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

