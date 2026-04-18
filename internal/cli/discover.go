package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
)

func newDiscoverCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "discover [path...]",
		Short: "Show what glint discovers in a repository",
		Long: `Walk one or more paths and print every app glint would render and lint.

Useful for debugging discovery configuration before running a full lint.`,
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

			switch format {
			case "json":
				return printDiscoverJSON(cmd, apps)
			default:
				return printDiscoverText(cmd, paths, apps)
			}
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "text", "output format: text|json")
	return cmd
}

func printDiscoverText(cmd *cobra.Command, paths []string, apps []discovery.DiscoveredApp) error {
	w := cmd.OutOrStdout()

	if len(apps) == 0 {
		fmt.Fprintf(w, "No apps discovered in %s\n", strings.Join(paths, ", "))
		return nil
	}

	fmt.Fprintf(w, "Discovered %d app(s) in %s\n\n", len(apps), strings.Join(paths, ", "))

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tRENDERER\tFRAMEWORK\tPATH")
	fmt.Fprintln(tw, "  ────\t────────\t─────────\t────")

	for _, app := range apps {
		path := app.RootPath
		// Make path relative to cwd for readability.
		if cwd, err := os.Getwd(); err == nil {
			if rel, err := relativePath(cwd, path); err == nil {
				path = rel
			}
		}
		fmt.Fprintf(tw, "  %s\t%s\t(%s)\t%s\n",
			app.Name,
			string(app.Renderer),
			string(app.Framework),
			path,
		)
	}
	return tw.Flush()
}

func printDiscoverJSON(cmd *cobra.Command, apps []discovery.DiscoveredApp) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(apps)
}

// relativePath returns path relative to base, prefixed with "./" for clarity.
func relativePath(base, path string) (string, error) {
	// Use strings trimming rather than filepath.Rel to avoid os.Getwd issues in tests.
	if rel, ok := strings.CutPrefix(path, base); ok {
		rel = strings.TrimPrefix(rel, "/")
		rel = strings.TrimPrefix(rel, "\\")
		if rel == "" {
			return ".", nil
		}
		return "./" + rel, nil
	}
	return path, nil
}
