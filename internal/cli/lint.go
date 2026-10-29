package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"log/slog"

	"github.com/spf13/cobra"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/plugins"
	"github.com/lukashankeln/glint/internal/render"
	"github.com/lukashankeln/glint/internal/rules"
)

func newLintCmd() *cobra.Command {
	var (
		onlyRules   string
		skipRules   string
		format      string
		failOn      string
		output      string
		outputJSON  string
		outputSARIF string
		outputJUnit string
	)

	cmd := &cobra.Command{
		Use:   "lint [path...]",
		Short: "Discover, render, and evaluate policy rules against all manifests",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args
			if len(paths) == 0 {
				paths = []string{"."}
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// CLI flags override config values.
			if cmd.Flags().Changed("format") {
				cfg.Output.Format = format
			} else if os.Getenv("GITHUB_ACTIONS") == "true" {
				cfg.Output.Format = "github-actions"
			}
			if cmd.Flags().Changed("fail-on") {
				cfg.FailOn = splitCSV2(failOn)
			}
			if cmd.Flags().Changed("output") {
				cfg.Output.OutputFile = output
			}

			// Build the rule engine — fail fast on bad CEL.
			engine, err := rules.NewEngine(cfg.Rules)
			if err != nil {
				return fmt.Errorf("initializing rule engine: %w", err)
			}

			// Build plugins — fail fast on unknown adapters or bad config.
			pluginList, err := plugins.Build(cfg.Plugins)
			if err != nil {
				return fmt.Errorf("initializing plugins: %w", err)
			}

			// Discover apps.
			apps, parseErrors, err := discovery.Discover(cmd.Context(), paths, cfg)
			if err != nil {
				return fmt.Errorf("discovery failed: %w", err)
			}

			if len(apps) == 0 {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No apps discovered."); err != nil {
					return fmt.Errorf("writing output: %w", err)
				}
				return nil
			}

			// Render all apps in parallel and collect manifests.
			var allManifests []manifest.Manifest
			var successfulRenders []renderResult
			var renderViolations []rules.Violation
			rendered := 0
			for _, r := range renderApps(cmd.Context(), apps, cfg) {
				if cmd.Context().Err() != nil {
					return cmd.Context().Err()
				}
				if r.err != nil {
					slog.Warn("render failed, reporting as error", "app", r.app.Name, "err", r.err)
					if v, ok := renderErrorViolation(r); ok {
						renderViolations = append(renderViolations, v)
					}
					continue
				}
				rendered++
				allManifests = append(allManifests, r.manifests...)
				successfulRenders = append(successfulRenders, r)
			}
			slog.Info("rendering complete", "apps", rendered, "manifests", len(allManifests))

			// Run plugins against each app's manifests after all rendering is done.
			var pluginViolations []rules.Violation
			for _, r := range successfulRenders {
				for _, p := range pluginList {
					slog.Debug("running plugin", "plugin", p.Name(), "app", r.app.Name)
					pvs, perr := p.Run(cmd.Context(), r.manifests)
					if perr != nil {
						return fmt.Errorf("plugin %q on app %q: %w", p.Name(), r.app.Name, perr)
					}
					pluginViolations = append(pluginViolations, pvs...)
				}
			}

			// Evaluate rules.
			violations := engine.Evaluate(allManifests)
			violations = append(violations, pluginViolations...)
			violations = append(violations, renderViolations...)

			// Files that were identified as GitOps CRDs but could not be parsed
			// cannot be validated at all, so they are reported as errors.
			for _, pe := range parseErrors {
				violations = append(violations, rules.Violation{
					RuleID:   "glint/parse-error",
					Severity: rules.SeverityError,
					Message:  fmt.Sprintf("file could not be parsed and cannot be validated: %s", pe.Message),
					FilePath: pe.FilePath,
					Source:   "discovery",
				})
			}

			// Apply --only-rules / --skip-rules filters.
			violations = filterViolations(violations, onlyRules, skipRules)

			// Write primary output.
			if cfg.Output.OutputFile != "" {
				err := writeReportFile(cfg.Output.OutputFile, func(w io.Writer) error {
					return writeOutput(w, violations, cfg.Output.Format)
				})
				if err != nil {
					return fmt.Errorf("writing output: %w", err)
				}
			} else if err := writeOutput(cmd.OutOrStdout(), violations, cfg.Output.Format); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			// For text format, append summary line to stdout (not the output file).
			if cfg.Output.Format == "" || cfg.Output.Format == "text" {
				stdout := cmd.OutOrStdout()
				if cfg.Output.OutputFile != "" {
					stdout = os.Stdout
				}
				errCount := countBySeverity(violations, rules.SeverityError)
				warnCount := countBySeverity(violations, rules.SeverityWarning)
				if _, err := fmt.Fprintf(stdout, "\n%d errors, %d warnings across %d resources\n",
					errCount, warnCount, len(allManifests)); err != nil {
					return fmt.Errorf("writing summary: %w", err)
				}
			}

			// Write SARIF alongside any other format (e.g. github-actions + sarif in one run).
			if outputSARIF != "" {
				err := writeReportFile(outputSARIF, func(w io.Writer) error {
					return writeSARIF(w, violations)
				})
				if err != nil {
					return fmt.Errorf("writing SARIF output: %w", err)
				}
			}

			// Write JUnit alongside any other format.
			if outputJUnit != "" {
				err := writeReportFile(outputJUnit, func(w io.Writer) error {
					return writeJUnit(w, violations)
				})
				if err != nil {
					return fmt.Errorf("writing JUnit output: %w", err)
				}
			}

			// Write JSON summary if requested (for CI step output capture).
			if outputJSON != "" {
				s := buildSummary(violations)
				err := writeReportFile(outputJSON, func(w io.Writer) error {
					enc := json.NewEncoder(w)
					enc.SetIndent("", "  ")
					return enc.Encode(s)
				})
				if err != nil {
					return fmt.Errorf("writing json summary: %w", err)
				}
			}

			// Exit code based on fail_on config.
			if failsThreshold(violations, cfg.FailOn) {
				return &ViolationsFoundError{}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&onlyRules, "only-rules", "", "comma-separated rule IDs to run (all others skipped)")
	cmd.Flags().StringVar(&skipRules, "skip-rules", "", "comma-separated rule IDs to skip")
	cmd.Flags().StringVar(&format, "format", "", "output format: text|json|sarif|junit|github-actions (overrides config)")
	cmd.Flags().StringVar(&failOn, "fail-on", "", "comma-separated severities that cause non-zero exit (overrides config)")
	cmd.Flags().StringVar(&output, "output", "", "write output to file instead of stdout")
	cmd.Flags().StringVar(&outputJSON, "output-json", "", "write JSON summary to file (for CI step capture)")
	cmd.Flags().StringVar(&outputSARIF, "output-sarif", "", "write SARIF output to file alongside the primary format")
	cmd.Flags().StringVar(&outputJUnit, "output-junit", "", "write JUnit XML to file alongside the primary format")
	return cmd
}

func filterViolations(violations []rules.Violation, onlyRules, skipRules string) []rules.Violation {
	if onlyRules == "" && skipRules == "" {
		return violations
	}
	only := splitCSV(onlyRules)
	skip := splitCSV(skipRules)

	var out []rules.Violation
	for _, v := range violations {
		if len(only) > 0 && !only[v.RuleID] {
			continue
		}
		if skip[v.RuleID] {
			continue
		}
		out = append(out, v)
	}
	return out
}

func splitCSV(s string) map[string]bool {
	m := map[string]bool{}
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			m[p] = true
		}
	}
	return m
}

// splitCSV2 returns a slice (preserving order) rather than a set.
func splitCSV2(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// renderApps is renderAppsParallel, used by lint and render. It is a variable
// only so a test can inject render results that real input cannot produce;
// tests that replace it must not run in parallel.
var renderApps = renderAppsParallel

// renderErrorViolation reports a render failure that must not pass silently:
// an app that could not be rendered cannot be validated, so the failure is an
// error-severity violation rather than a skip. It returns false only when the
// render succeeded. A merge key glint could not resolve keeps the more
// specific glint/parse-error ID; every other failure is glint/render-error.
func renderErrorViolation(r renderResult) (rules.Violation, bool) {
	if r.err == nil {
		return rules.Violation{}, false
	}
	if errors.Is(r.err, render.ErrAmbiguousMergeKey) {
		return rules.Violation{
			RuleID:   "glint/parse-error",
			Severity: rules.SeverityError,
			Message:  fmt.Sprintf("app %q could not be parsed and cannot be validated: %v", r.app.Name, r.err),
			FilePath: r.app.RootPath,
			Source:   "render",
		}, true
	}
	return rules.Violation{
		RuleID:   "glint/render-error",
		Severity: rules.SeverityError,
		Message:  fmt.Sprintf("app %q could not be rendered and cannot be validated: %v", r.app.Name, r.err),
		FilePath: r.app.RootPath,
		Source:   "render",
	}, true
}

func countBySeverity(violations []rules.Violation, sev rules.Severity) int {
	n := 0
	for _, v := range violations {
		if v.Severity == sev {
			n++
		}
	}
	return n
}

func failsThreshold(violations []rules.Violation, failOn []string) bool {
	sevSet := map[string]bool{}
	for _, s := range failOn {
		sevSet[strings.ToLower(s)] = true
	}
	for _, v := range violations {
		if sevSet[strings.ToLower(string(v.Severity))] {
			return true
		}
	}
	return false
}

// writeReportFile creates path, writes it with write and closes it. Any write
// or close error is returned; a partially written regular file is removed so
// it cannot be mistaken for a complete report. Paths that are not regular
// files (a symlink, FIFO or device such as /dev/stdout) are never removed.
func writeReportFile(path string, write func(io.Writer) error) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %q: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing %q: %w", path, cerr)
		}
		if err != nil {
			// Best effort; a failed removal must not replace the original error.
			if fi, serr := os.Lstat(path); serr == nil && fi.Mode().IsRegular() {
				_ = os.Remove(path)
			}
		}
	}()

	if werr := write(f); werr != nil {
		return fmt.Errorf("writing %q: %w", path, werr)
	}
	return nil
}
