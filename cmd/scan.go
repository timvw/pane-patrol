package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/evaluator"
	"github.com/timvw/pane-patrol/internal/model"
	"github.com/timvw/pane-patrol/internal/mux"
)

var (
	flagScanFilter   string
	flagScanParallel int
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Evaluate all panes for blocked agents",
	Long: `Scan all terminal multiplexer panes and evaluate each using an LLM.

Outputs a JSON array of verdicts for all panes. Use --filter to restrict
to sessions matching a regex pattern. Use --parallel to evaluate multiple
panes concurrently.

ZFC compliance: Go lists panes, captures content, and sends to LLM.
The LLM decides everything — which panes are agents, which are blocked, and why.`,
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
		var wg sync.WaitGroup
		sem := make(chan struct{}, parallel)
		errCh := make(chan error, len(panes))

		for i, pane := range panes {
			wg.Add(1)
			go func(idx int, p model.Pane) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				v, err := evaluatePane(ctx, m, eval, p)
				if err != nil {
					errCh <- fmt.Errorf("pane %s: %w", p.Target, err)
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
						Model:       eval.Model(),
						Provider:    eval.Provider(),
						EvaluatedAt: time.Now().UTC(),
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
func evaluatePane(ctx context.Context, m mux.Multiplexer, eval evaluator.Evaluator, pane model.Pane) (*model.Verdict, error) {
	content, err := m.CapturePane(ctx, pane.Target)
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
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
		Reasoning:   llmVerdict.Reasoning,
		Model:       eval.Model(),
		Provider:    eval.Provider(),
		EvaluatedAt: time.Now().UTC(),
	}

	if flagVerbose {
		verdict.Content = content
	}

	return verdict, nil
}

func init() {
	scanCmd.Flags().StringVar(&flagScanFilter, "filter", "", "regex pattern to filter by session name")
	scanCmd.Flags().IntVar(&flagScanParallel, "parallel", 1, "number of panes to evaluate concurrently")
	rootCmd.AddCommand(scanCmd)
}
