package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/rules"
)

func newLintCmd() *cobra.Command {
	var (
		onlyRules  string
		skipRules  string
		format     string
		failOn     string
		output     string
		outputJSON string
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

			// Discover apps.
			apps, err := discovery.Discover(cmd.Context(), paths, cfg)
			if err != nil {
				return fmt.Errorf("discovery failed: %w", err)
			}

			if len(apps) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No apps discovered.")
				return nil
			}

			// Render all apps in parallel and collect manifests.
			var allManifests []manifest.Manifest
			rendered := 0
			for _, r := range renderAppsParallel(cmd.Context(), apps, cfg) {
				if cmd.Context().Err() != nil {
					return cmd.Context().Err()
				}
				if r.err != nil {
					log.Warn().Err(r.err).Str("app", r.app.Name).Msg("render failed, skipping")
					continue
				}
				rendered++
				allManifests = append(allManifests, r.manifests...)
			}
			log.Info().Int("apps", rendered).Int("manifests", len(allManifests)).Msg("rendering complete")

			// Evaluate rules.
			violations := engine.Evaluate(allManifests)

			// Apply --only-rules / --skip-rules filters.
			violations = filterViolations(violations, onlyRules, skipRules)

			// Write primary output.
			w := cmd.OutOrStdout()
			if cfg.Output.OutputFile != "" {
				f, err := os.Create(cfg.Output.OutputFile)
				if err != nil {
					return fmt.Errorf("creating output file: %w", err)
				}
				defer f.Close()
				w = f
			}

			if err := writeOutput(w, violations, cfg.Output.Format); err != nil {
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
				fmt.Fprintf(stdout, "\n%d errors, %d warnings across %d resources\n",
					errCount, warnCount, len(allManifests))
			}

			// Write JSON summary if requested (for CI step output capture).
			if outputJSON != "" {
				s := buildSummary(violations)
				f, err := os.Create(outputJSON)
				if err != nil {
					return fmt.Errorf("creating json output file: %w", err)
				}
				defer f.Close()
				enc := json.NewEncoder(f)
				enc.SetIndent("", "  ")
				if err := enc.Encode(s); err != nil {
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
	for _, p := range strings.Split(s, ",") {
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
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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
