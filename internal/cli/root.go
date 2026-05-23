package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
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

It auto-renders Helm charts and Kustomize overlays and enforces custom policies via CEL expressions.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			initLogger(cmd.ErrOrStderr())
			return nil
		},
	}

	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: glint.yaml)")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newDiscoverCmd())
	root.AddCommand(newRenderCmd())
	root.AddCommand(newLintCmd())
	root.AddCommand(newRulesCmd())
	root.AddCommand(newInitCmd())

	return root
}

func initLogger(w io.Writer) {
	level := parseLogLevel(logLevel)

	// In CI environments, default to warn to reduce noise.
	if os.Getenv("CI") == "true" && logLevel == "info" {
		level = slog.LevelWarn
	}

	useColor := !noColor && isColorTerminal(w)
	slog.SetDefault(slog.New(&consoleHandler{w: w, level: level, color: useColor}))
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
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

// consoleHandler is a minimal slog.Handler that writes compact, optionally
// colored log lines — one line per record, key=value pairs after the message.
type consoleHandler struct {
	w     io.Writer
	level slog.Level
	color bool
	mu    sync.Mutex
}

func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	b.WriteString(r.Time.Format(time.TimeOnly))
	b.WriteByte(' ')

	if h.color {
		switch {
		case r.Level >= slog.LevelError:
			b.WriteString("\033[31mERR\033[0m")
		case r.Level >= slog.LevelWarn:
			b.WriteString("\033[33mWRN\033[0m")
		case r.Level >= slog.LevelInfo:
			b.WriteString("\033[32mINF\033[0m")
		default:
			b.WriteString("\033[90mDBG\033[0m")
		}
	} else {
		switch {
		case r.Level >= slog.LevelError:
			b.WriteString("ERR")
		case r.Level >= slog.LevelWarn:
			b.WriteString("WRN")
		case r.Level >= slog.LevelInfo:
			b.WriteString("INF")
		default:
			b.WriteString("DBG")
		}
	}

	b.WriteByte(' ')
	b.WriteString(r.Message)

	r.Attrs(func(a slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		val := fmt.Sprintf("%v", a.Value.Any())
		if strings.ContainsAny(val, " \t\n\"") {
			b.WriteString(strconv.Quote(val))
		} else {
			b.WriteString(val)
		}
		return true
	})

	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *consoleHandler) WithGroup(name string) slog.Handler       { return h }
