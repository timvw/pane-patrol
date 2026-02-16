package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/config"
	"github.com/timvw/pane-patrol/internal/evaluator"
	"github.com/timvw/pane-patrol/internal/model"
	"github.com/timvw/pane-patrol/internal/mux"
	"github.com/timvw/pane-patrol/internal/parser"
)

var (
	flagScanFilter   string
	flagScanParallel int
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Evaluate all panes for blocked agents",
	Long: `Scan all terminal multiplexer panes and evaluate each one.

Known agents (OpenCode, Claude Code, Codex) are evaluated by deterministic
parsers. Unknown agents fall back to LLM evaluation.

Outputs a JSON array of verdicts. Use --filter to restrict to sessions
matching a regex pattern. Use --parallel to evaluate concurrently.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		m, err := getMultiplexer()
		if err != nil {
			return err
		}

		eval, err := getEvaluator()
		if err != nil {
			return err
		}

		panes, err := m.ListPanes(ctx, flagScanFilter)
		if err != nil {
			return fmt.Errorf("failed to list panes: %w", err)
		}

		// Apply exclude_sessions from config file
		cfg, cfgErr := config.Load()
		if cfgErr == nil && len(cfg.ExcludeSessions) > 0 {
			filtered := make([]model.Pane, 0, len(panes))
			for _, p := range panes {
				if !config.MatchesExcludeList(p.Session, cfg.ExcludeSessions) {
					filtered = append(filtered, p)
				}
			}
			panes = filtered
		}

		if len(panes) == 0 {
			fmt.Fprintln(os.Stderr, "no panes found")
			fmt.Println("[]")
			return nil
		}

		verdicts := make([]model.Verdict, len(panes))
		parallel := flagScanParallel
		if parallel < 1 {
			parallel = 1
		}
		if parallel > len(panes) {
			parallel = len(panes)
		}

		// Evaluate panes with bounded parallelism.
		registry := parser.NewRegistry()
		var wg sync.WaitGroup
		sem := make(chan struct{}, parallel)
		errCh := make(chan error, len(panes))

		for i, pane := range panes {
			wg.Add(1)
			go func(idx int, p model.Pane) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				start := time.Now()
				v, err := evaluatePane(ctx, m, eval, registry, p)
				if err != nil {
					errCh <- fmt.Errorf("pane %s: %w", p.Target, err)
					evalModel, evalProvider := "", ""
					if eval != nil {
						evalModel = eval.Model()
						evalProvider = eval.Provider()
					}
					// Return a verdict with error info instead of failing the whole scan.
					verdicts[idx] = model.Verdict{
						Target:      p.Target,
						Session:     p.Session,
						Window:      p.Window,
						Pane:        p.Pane,
						Command:     p.Command,
						Agent:       "error",
						Blocked:     false,
						Reason:      fmt.Sprintf("evaluation failed: %v", err),
						Model:       evalModel,
						Provider:    evalProvider,
						EvaluatedAt: time.Now().UTC(),
						DurationMs:  time.Since(start).Milliseconds(),
					}
					return
				}
				verdicts[idx] = *v
			}(i, pane)
		}

		wg.Wait()
		close(errCh)

		// Log errors to stderr but don't fail.
		for err := range errCh {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(verdicts)
	},
}

// evaluatePane captures and evaluates a single pane.
// Uses deterministic parsers first; falls back to LLM for unrecognized agents.
func evaluatePane(ctx context.Context, m mux.Multiplexer, eval evaluator.Evaluator, registry *parser.Registry, pane model.Pane) (*model.Verdict, error) {
	start := time.Now()

	capture, err := m.CapturePane(ctx, pane.Target)
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}

	content := model.BuildProcessHeader(pane) + capture

	// Tier 1: Deterministic parsers for known agents.
	if parsed := registry.Parse(capture, pane.ProcessTree); parsed != nil {
		verdict := &model.Verdict{
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
			EvalSource:  "parser",
			Model:       "deterministic",
			Provider:    "parser",
			EvaluatedAt: time.Now().UTC(),
			DurationMs:  time.Since(start).Milliseconds(),
		}
		if flagVerbose {
			verdict.Content = content
		}
		return verdict, nil
	}

	// Tier 2: LLM fallback.
	if eval == nil {
		return nil, fmt.Errorf("no deterministic parser matched and no API key configured for LLM fallback")
	}
	llmVerdict, err := eval.Evaluate(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("evaluation failed: %w", err)
	}

	verdict := &model.Verdict{
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
		EvalSource:  "llm",
		Model:       eval.Model(),
		Provider:    eval.Provider(),
		EvaluatedAt: time.Now().UTC(),
		DurationMs:  time.Since(start).Milliseconds(),
	}

	if flagVerbose {
		verdict.Content = content
	}

	return verdict, nil
}

func init() {
	scanCmd.Flags().StringVar(&flagScanFilter, "filter", "", "regex pattern to filter by session name")
	scanCmd.Flags().IntVar(&flagScanParallel, "parallel", 10, "number of panes to evaluate concurrently")
	rootCmd.AddCommand(scanCmd)
}
