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

// newRawRenderer creates a RawRenderer.
func newRawRenderer(cfg *config.Config) *RawRenderer {
	return &RawRenderer{cfg: cfg}
}

// Render reads all YAML files from app.RootPath and returns manifests.
func (r *RawRenderer) Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	var manifests []manifest.Manifest

	err := filepath.WalkDir(app.RootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %q: %w", path, err)
		}

		docs, err := SplitYAML(data, app.Name, path, false)
		if err != nil {
			return fmt.Errorf("splitting YAML in %q: %w", path, err)
		}
		manifests = append(manifests, docs...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %q: %w", app.RootPath, err)
	}

	return manifests, nil
}
