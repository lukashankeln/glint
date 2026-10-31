package render

import (
	"context"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

// renderKustomizeSubprocess renders a Kustomize overlay using the `kustomize build` CLI command.
func renderKustomizeSubprocess(ctx context.Context, cfg *config.Config, app discovery.DiscoveredApp) ([]manifest.Manifest, error) {
	restrictor := "LoadRestrictionsRootOnly"
	if cfg.Render.Kustomize.LoadRestrictor == config.LoadRestrictorNone {
		restrictor = "LoadRestrictionsNone"
	}
	args := []string{"build", "--load-restrictor", restrictor}
	if cfg.Render.Kustomize.EnableHelm {
		args = append(args, "--enable-helm")
	}
	args = append(args, app.RootPath)
	cmd := execCommandContext(ctx, "kustomize", args...)
	stdout, err := runCappedSubprocess(ctx, cmd, "kustomize build", app.Name)
	if err != nil {
		return nil, err
	}

	return SplitYAML(stdout, app.Name, "<rendered>", true)
}
