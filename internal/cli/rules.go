package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/rules"
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Manage and inspect policy rules",
	}
	cmd.AddCommand(newRulesListCmd())
	cmd.AddCommand(newRulesValidateCmd())
	return cmd
}

func newRulesListCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all available policy rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			engine, err := rules.NewEngine(cfg.Rules)
			if err != nil {
				return fmt.Errorf("initializing rule engine: %w", err)
			}

			defs := engine.Rules()
			w := cmd.OutOrStdout()

			if format == "json" {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(defs)
			}

			// Text table
			fmt.Fprintf(w, "%-30s  %-8s  %-7s  %s\n", "ID", "SEVERITY", "ENABLED", "SOURCE")
			fmt.Fprintf(w, "%-30s  %-8s  %-7s  %s\n",
				strings.Repeat("-", 30), "--------", "-------", "------")
			for _, d := range defs {
				enabled := "no"
				if d.Enabled {
					enabled = "yes"
				}
				fmt.Fprintf(w, "%-30s  %-8s  %-7s  %s\n",
					d.ID, string(d.Severity), enabled, d.Source)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "text", "output format: text|json")
	return cmd
}

func newRulesValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [file...]",
		Short: "Compile rule files and report CEL syntax errors",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("at least one rule file is required")
			}

			defs, err := rules.LoadRuleFiles(args)
			if err != nil {
				return fmt.Errorf("loading rule files: %w", err)
			}

			if len(defs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No rules found in provided files.")
				return nil
			}

			env, err := rules.NewCELEnv()
			if err != nil {
				return fmt.Errorf("creating CEL env: %w", err)
			}

			w := cmd.OutOrStdout()
			anyErr := false
			for _, d := range defs {
				_, issues := env.Compile(d.Expression)
				if issues != nil && issues.Err() != nil {
					fmt.Fprintf(w, "✗ %-30s — CEL compile error: %s\n", d.ID, issues.Err())
					anyErr = true
				} else {
					fmt.Fprintf(w, "✓ %-30s — OK\n", d.ID)
				}
			}

			if anyErr {
				os.Exit(ExitError)
			}
			return nil
		},
	}
	return cmd
}
