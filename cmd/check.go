package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/model"
	"github.com/timvw/pane-patrol/internal/parser"
)

var checkCmd = &cobra.Command{
	Use:   "check <target>",
	Short: "Evaluate if a single pane is blocked",
	Long: `Evaluate a single terminal multiplexer pane.

Known agents (OpenCode, Claude Code, Codex) are evaluated by deterministic
parsers. Unknown agents fall back to LLM evaluation.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]

		m, err := getMultiplexer()
		if err != nil {
			return err
		}

		eval, err := getEvaluator()
		if err != nil {
			return err
		}

		start := time.Now()

		// Look up pane metadata (PID, process tree) for the target.
		panes, err := m.ListPanes(cmd.Context(), "")
		if err != nil {
			return fmt.Errorf("failed to list panes: %w", err)
		}

		var pane model.Pane
		for _, p := range panes {
			if p.Target == target {
				pane = p
				break
			}
		}
		if pane.Target == "" {
			pane.Target = target
		}

		// Capture pane content (transport).
		capture, err := m.CapturePane(cmd.Context(), target)
		if err != nil {
			return fmt.Errorf("failed to capture pane %q: %w", target, err)
		}

		content := model.BuildProcessHeader(pane) + capture

		// Tier 1: Try deterministic parsers first (instant, free).
		registry := parser.NewRegistry()
		var verdict model.Verdict
		if parsed := registry.Parse(capture, pane.ProcessTree); parsed != nil {
			verdict = model.Verdict{
				Target:      pane.Target,
				Session:     pane.Session,
				Window:      pane.Window,
				Pane:        pane.Pane,
				Command:     pane.Command,
				Agent:       parsed.Agent,
				Blocked:     parsed.Blocked,
				Reason:      parsed.Reason,
				WaitingFor:  parsed.WaitingFor,
				Reasoning:   parsed.Reasoning,
				Actions:     parsed.Actions,
				Recommended: parsed.Recommended,
				Model:       "deterministic",
				Provider:    "parser",
				EvaluatedAt: time.Now().UTC(),
				DurationMs:  time.Since(start).Milliseconds(),
			}
		} else {
			// Tier 2: LLM fallback.
			llmVerdict, err := eval.Evaluate(cmd.Context(), content)
			if err != nil {
				return fmt.Errorf("evaluation failed for %q: %w", target, err)
			}
			verdict = model.Verdict{
				Target:      pane.Target,
				Session:     pane.Session,
				Window:      pane.Window,
				Pane:        pane.Pane,
				Command:     pane.Command,
				Agent:       llmVerdict.Agent,
				Blocked:     llmVerdict.Blocked,
				Reason:      llmVerdict.Reason,
				WaitingFor:  llmVerdict.WaitingFor,
				Reasoning:   llmVerdict.Reasoning,
				Actions:     llmVerdict.Actions,
				Recommended: llmVerdict.Recommended,
				Usage:       llmVerdict.Usage,
				Model:       eval.Model(),
				Provider:    eval.Provider(),
				EvaluatedAt: time.Now().UTC(),
				DurationMs:  time.Since(start).Milliseconds(),
			}
		}

		if flagVerbose {
			verdict.Content = content
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(verdict)
	},
}

func init() {
	rootCmd.AddCommand(checkCmd)
}
