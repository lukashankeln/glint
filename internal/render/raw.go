package render

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// RawRenderer reads raw YAML files from a directory.
type RawRenderer struct {
	cfg *config.Config
}

func newRawRenderer(cfg *config.Config) *RawRenderer {
	return &RawRenderer{cfg: cfg}
}

// Render reads YAML files inside app.RootPath. Subdirectories are only
// included when app.Recurse is set: otherwise discovery makes each of them its
// own raw app, and recursing would evaluate every nested file twice.
func (r *RawRenderer) Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	files, err := rawYAMLFiles(app.RootPath, app.Recurse)
	if err != nil {
		return nil, err
	}

	// One manifest per file is the common case; append still grows the slice
	// for multi-doc files.
	manifests := make([]manifest.Manifest, 0, len(files))
	for _, path := range files {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", path, err)
		}

		docs, err := SplitYAML(data, app.Name, path, false)
		if err != nil {
			return nil, fmt.Errorf("splitting YAML in %q: %w", path, err)
		}
		manifests = append(manifests, docs...)
	}

	return manifests, nil
}

// rawYAMLFiles lists the YAML files in root in lexical order. With recurse,
// subdirectories are included except hidden ones, matching discovery.
func rawYAMLFiles(root string, recurse bool) ([]string, error) {
	if !recurse {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", root, err)
		}
		var files []string
		for _, entry := range entries {
			if !entry.IsDir() && isYAMLName(entry.Name()) {
				files = append(files, root+"/"+entry.Name())
			}
		}
		return files, nil
	}

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if isYAMLName(d.Name()) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", root, err)
	}
	return files, nil
}

func isYAMLName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
