package render

import (
	"context"
	"fmt"
	"os"
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

// Render reads YAML files directly inside app.RootPath (non-recursive).
// Subdirectories are each their own raw app in discovery, so recursing here
// would cause every file in a nested dir to be evaluated twice.
func (r *RawRenderer) Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	entries, err := os.ReadDir(app.RootPath)
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", app.RootPath, err)
	}

	var manifests []manifest.Manifest
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
			continue
		}

		path := app.RootPath + "/" + entry.Name()
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
