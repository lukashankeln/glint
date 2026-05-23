package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"log/slog"

	"github.com/spf13/cobra"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

func newRenderCmd() *cobra.Command {
	var format string
	var output string

	cmd := &cobra.Command{
		Use:   "render [path...]",
		Short: "Render all discovered apps and print the resulting manifests",
		Long: `Discover apps in the given paths (same as glint discover), then render each app.

By default, outputs YAML to stdout, each app separated by "---".
Use --output <dir> to write each app to <dir>/<appname>.yaml.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args
			if len(paths) == 0 {
				paths = []string{"."}
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			apps, err := discovery.Discover(cmd.Context(), paths, cfg)
			if err != nil {
				return fmt.Errorf("discovery failed: %w", err)
			}

			if len(apps) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No apps discovered.")
				return nil
			}

			// Ensure output dir exists if requested.
			if output != "" && output != "-" {
				if err := os.MkdirAll(output, 0o755); err != nil {
					return fmt.Errorf("creating output directory: %w", err)
				}
			}

			rendered := 0
			totalManifests := 0
			for _, r := range renderAppsParallel(cmd.Context(), apps, cfg) {
				if cmd.Context().Err() != nil {
					return cmd.Context().Err()
				}

				if r.err != nil {
					slog.Warn("render failed, skipping", "app", r.app.Name, "err", r.err)
					continue
				}

				if len(r.manifests) == 0 {
					slog.Debug("no manifests produced", "app", r.app.Name)
					continue
				}

				rendered++
				totalManifests += len(r.manifests)

				if output != "" && output != "-" {
					if err := writeManifestsToFile(output, r.app, r.manifests, format); err != nil {
						slog.Warn("failed to write output file", "app", r.app.Name, "err", err)
					}
					continue
				}

				// Write to stdout.
				if err := writeManifestsToWriter(cmd, r.app, r.manifests, format); err != nil {
					return err
				}
			}
			slog.Info("rendering complete", "apps", rendered, "manifests", totalManifests)

			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "yaml", "output format: yaml|json")
	cmd.Flags().StringVarP(&output, "output", "o", "-", "output: - for stdout, or a directory path")
	return cmd
}

func writeManifestsToWriter(cmd *cobra.Command, app discovery.DiscoveredApp, manifests []manifest.Manifest, format string) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "# --- app: %s ---\n", app.Name)
	for _, m := range manifests {
		fmt.Fprintln(w, "---")
		if format == "json" {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			if err := enc.Encode(m.Object); err != nil {
				return err
			}
		} else {
			out, err := sigsyaml.Marshal(m.Object)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s", out)
		}
	}
	return nil
}

func writeManifestsToFile(dir string, app discovery.DiscoveredApp, manifests []manifest.Manifest, format string) error {
	ext := ".yaml"
	if format == "json" {
		ext = ".json"
	}
	outPath := filepath.Join(dir, app.Name+ext)
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %q: %w", outPath, err)
	}
	defer f.Close()

	for _, m := range manifests {
		fmt.Fprintln(f, "---")
		if format == "json" {
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			if err := enc.Encode(m.Object); err != nil {
				return err
			}
		} else {
			out, err := sigsyaml.Marshal(m.Object)
			if err != nil {
				return err
			}
			fmt.Fprintf(f, "%s", out)
		}
	}
	return nil
}
