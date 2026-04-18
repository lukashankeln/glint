package discovery

import (
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// isExcluded reports whether path matches any of the exclusion glob patterns.
// Patterns support ** to match any number of path segments.
// Matching is attempted against both the full path and each path suffix so
// that patterns like "vendor/**" match "a/b/vendor/pkg/file.go".
func isExcluded(path string, patterns []string) bool {
	// Normalise to forward slashes for consistent glob matching.
	normalised := filepath.ToSlash(path)

	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern)

		// Direct match.
		if ok, _ := doublestar.Match(pattern, normalised); ok {
			return true
		}

		// Try matching each path suffix so "vendor/**" matches "a/b/vendor/x".
		for i := 0; i < len(normalised); i++ {
			if normalised[i] == '/' {
				if ok, _ := doublestar.Match(pattern, normalised[i+1:]); ok {
					return true
				}
			}
		}
	}
	return false
}
