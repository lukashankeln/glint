package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
			initLogger(cmd.ErrOrStderr(), cfg.Output.Color)

			apps, _, err := discovery.Discover(cmd.Context(), paths, cfg)
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
			var failed, writeFailed []string
			for _, r := range renderApps(cmd.Context(), apps, cfg) {
				if cmd.Context().Err() != nil {
					return cmd.Context().Err()
				}

				if r.err != nil {
					slog.Warn("render failed", "app", r.app.Name, "err", r.err)
					failed = append(failed, r.app.Name)
					continue
				}

				if len(r.manifests) == 0 {
					slog.Debug("no manifests produced", "app", r.app.Name)
					continue
				}

				if output != "" && output != "-" {
					if err := writeManifestsToFile(output, r.app, r.manifests, format); err != nil {
						slog.Warn("failed to write output file", "app", r.app.Name, "err", err)
						writeFailed = append(writeFailed, r.app.Name)
						continue
					}
				} else if err := writeManifestsToWriter(cmd, r.app, r.manifests, format); err != nil {
					// Stdout output cannot be cleaned up, so stop instead of
					// appending further apps after a truncated document.
					return fmt.Errorf("writing output for app %s: %w", r.app.Name, err)
				}
				rendered++
				totalManifests += len(r.manifests)
			}
			slog.Info("rendering complete", "apps", rendered, "manifests", totalManifests)

			var renderErr, writeErr error
			if len(failed) > 0 {
				renderErr = fmt.Errorf("rendering failed for %d app(s): %s", len(failed), strings.Join(failed, ", "))
			}
			if len(writeFailed) > 0 {
				writeErr = fmt.Errorf("writing output failed for %d app(s): %s", len(writeFailed), strings.Join(writeFailed, ", "))
			}
			return errors.Join(renderErr, writeErr)
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "yaml", "output format: yaml|json")
	cmd.Flags().StringVarP(&output, "output", "o", "-", "output: - for stdout, or a directory path")
	return cmd
}

func writeManifestsToWriter(cmd *cobra.Command, app discovery.DiscoveredApp, manifests []manifest.Manifest, format string) error {
	return writeManifests(cmd.OutOrStdout(), app, manifests, format, true)
}

// writeManifests writes the manifests to w, checking every write. The app
// header comment is only emitted for stdout output.
func writeManifests(w io.Writer, app discovery.DiscoveredApp, manifests []manifest.Manifest, format string, header bool) error {
	if header {
		if _, err := fmt.Fprintf(w, "# --- app: %s ---\n", app.Name); err != nil {
			return err
		}
	}
	for _, m := range manifests {
		if _, err := fmt.Fprintln(w, "---"); err != nil {
			return err
		}
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
			if _, err := fmt.Fprintf(w, "%s", out); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeManifestsToFile writes the app's manifests to <dir>/<app>.yaml (or
// .json). If writing fails, a partially written regular file is removed so it
// cannot be mistaken for complete output; a symlink, FIFO or device at the
// output path is never removed.
func writeManifestsToFile(dir string, app discovery.DiscoveredApp, manifests []manifest.Manifest, format string) error {
	ext := ".yaml"
	if format == "json" {
		ext = ".json"
	}
	outPath := filepath.Join(dir, app.Name+ext)
	return writeReportFile(outPath, func(w io.Writer) error {
		return writeManifests(w, app, manifests, format, false)
	})
}
