package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/model"
)

var checkCmd = &cobra.Command{
	Use:   "check <target>",
	Short: "Evaluate if a single pane is blocked",
	Long: `Evaluate a single terminal multiplexer pane using an LLM.

The LLM determines whether the pane is running an AI coding agent and
whether that agent is blocked waiting for human input.

ZFC compliance: Go captures the pane content and sends it to the LLM.
The LLM makes ALL judgment calls (agent detection, blocked state, reason).`,
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

		// Capture pane content (transport).
		content, err := m.CapturePane(cmd.Context(), target)
		if err != nil {
			return fmt.Errorf("failed to capture pane %q: %w", target, err)
		}

		// Send to LLM for evaluation (judgment).
		llmVerdict, err := eval.Evaluate(cmd.Context(), content)
		if err != nil {
			return fmt.Errorf("evaluation failed for %q: %w", target, err)
		}

		// Parse target components for the output.
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

		// Assemble verdict (transport — no interpretation).
		verdict := model.Verdict{
			Target:      pane.Target,
			Session:     pane.Session,
			Window:      pane.Window,
			Pane:        pane.Pane,
			Command:     pane.Command,
			Agent:       llmVerdict.Agent,
			Blocked:     llmVerdict.Blocked,
			Reason:      llmVerdict.Reason,
			Reasoning:   llmVerdict.Reasoning,
			Actions:     llmVerdict.Actions,
			Recommended: llmVerdict.Recommended,
			Usage:       llmVerdict.Usage,
			Model:       eval.Model(),
			Provider:    eval.Provider(),
			EvaluatedAt: time.Now().UTC(),
			DurationMs:  time.Since(start).Milliseconds(),
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
