package cli

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile  string
	logLevel string
	noColor  bool
)

// Execute is the entry point called from main.go.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "glint",
		Short: "GitOps linter for ArgoCD and Flux repositories",
		Long: `glint is a unified GitOps linting CLI.

It auto-renders Helm charts and Kustomize overlays, validates manifests against
their Kubernetes JSON schemas, and enforces custom policies via CEL expressions.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			initLogger(cmd.OutOrStderr())
			return nil
		},
	}

	// Global flags
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: glint.yaml)")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")

	// Bind viper
	viper.BindPFlag("log_level", root.PersistentFlags().Lookup("log-level")) //nolint:errcheck

	// Sub-commands
	root.AddCommand(newVersionCmd())
	root.AddCommand(newDiscoverCmd())
	root.AddCommand(newRenderCmd())
	root.AddCommand(newLintCmd())
	root.AddCommand(newRulesCmd())
	root.AddCommand(newInitCmd())

	return root
}

func initLogger(w io.Writer) {
	// Use console writer for human-readable output; disable color if requested
	// or when not a TTY.
	useColor := !noColor && isColorTerminal(w)

	var output io.Writer
	if useColor {
		output = zerolog.ConsoleWriter{Out: w, TimeFormat: "15:04:05"}
	} else {
		output = zerolog.ConsoleWriter{Out: w, TimeFormat: "15:04:05", NoColor: true}
	}

	lvl, err := zerolog.ParseLevel(strings.ToLower(logLevel))
	if err != nil {
		lvl = zerolog.InfoLevel
	}

	// In CI environments, default to warn to reduce noise (can be overridden)
	if os.Getenv("CI") == "true" && logLevel == "info" {
		lvl = zerolog.WarnLevel
	}

	log.Logger = zerolog.New(output).With().Timestamp().Logger().Level(lvl)
}

func isColorTerminal(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
