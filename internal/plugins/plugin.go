package plugins

import (
	"context"

	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/rules"
)

// Plugin is an external linter that participates in the lint pipeline.
type Plugin interface {
	Name() string
	Run(ctx context.Context, manifests []manifest.Manifest) ([]rules.Violation, error)
}

// Adapter translates a plugin's raw stdout into glint violations.
// severity is the optional override from PluginConfig (zero value means use plugin-reported).
type Adapter func(stdout []byte, severity rules.Severity) ([]rules.Violation, error)

// adapters is the registry of built-in adapters, keyed by name.
var adapters = map[string]Adapter{
	"kubeconform": parseKubeconform,
}
