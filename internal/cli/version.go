package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lukashankeln/glint/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "glint %s (commit: %s, built: %s)\n",
				version.Version, version.Commit, version.Date)
		},
	}
}
