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
			verdict = model.BaseVerdict(pane, start)
			verdict.Agent = parsed.Agent
			verdict.Blocked = parsed.Blocked
			verdict.Reason = parsed.Reason
			verdict.WaitingFor = parsed.WaitingFor
			verdict.Reasoning = parsed.Reasoning
			verdict.Actions = parsed.Actions
			verdict.Recommended = parsed.Recommended
			verdict.EvalSource = model.EvalSourceParser
			verdict.Model = "deterministic"
			verdict.Provider = "parser"
		} else {
			// Tier 2: LLM fallback.
			if eval == nil {
				return fmt.Errorf("no deterministic parser matched pane %q and no API key configured for LLM fallback", target)
			}
			llmVerdict, err := eval.Evaluate(cmd.Context(), content)
			if err != nil {
				return fmt.Errorf("evaluation failed for %q: %w", target, err)
			}
			verdict = model.BaseVerdict(pane, start)
			verdict.Agent = llmVerdict.Agent
			verdict.Blocked = llmVerdict.Blocked
			verdict.Reason = llmVerdict.Reason
			verdict.WaitingFor = llmVerdict.WaitingFor
			verdict.Reasoning = llmVerdict.Reasoning
			verdict.Actions = llmVerdict.Actions
			verdict.Recommended = llmVerdict.Recommended
			verdict.Usage = llmVerdict.Usage
			verdict.EvalSource = model.EvalSourceLLM
			verdict.Model = eval.Model()
			verdict.Provider = eval.Provider()
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
