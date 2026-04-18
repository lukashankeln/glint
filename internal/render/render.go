package render

import (
	"context"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// Renderer renders a DiscoveredApp into a list of Manifests.
type Renderer interface {
	Render(ctx context.Context, app discovery.DiscoveredApp) ([]manifest.Manifest, error)
}

// New returns the appropriate Renderer for app.Renderer type.
func New(app discovery.DiscoveredApp, cfg *config.Config) Renderer {
	switch app.Renderer {
	case discovery.RendererHelm:
		return newHelmRenderer(cfg)
	case discovery.RendererKustomize:
		return newKustomizeRenderer(cfg)
	default:
		return newRawRenderer(cfg)
	}
}
