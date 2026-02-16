package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/timvw/pane-patrol/internal/config"
	"github.com/timvw/pane-patrol/internal/evaluator"
	"github.com/timvw/pane-patrol/internal/model"
	"github.com/timvw/pane-patrol/internal/mux"
	ppotel "github.com/timvw/pane-patrol/internal/otel"
	"github.com/timvw/pane-patrol/internal/parser"
)

var tracer = otel.Tracer("pane-supervisor")

// Scanner wraps the pane-patrol scan functionality for use by the supervisor.
type Scanner struct {
	Mux             mux.Multiplexer
	Evaluator       evaluator.Evaluator
	Parsers         *parser.Registry // Deterministic parsers for known agents; nil disables
	Filter          string
	ExcludeSessions []string // Session names to exclude from scanning (exact match)
	Parallel        int
	Verbose         bool
	Cache           *VerdictCache
	Metrics         *ppotel.Metrics // OTEL metric counters; nil-safe
	SessionID       string          // Langfuse session ID — groups all scans from one supervisor run
	SelfTarget      string          // pane target of this supervisor process (skipped during scan)
}

// ScanResult contains the verdicts and metadata from a scan.
type ScanResult struct {
	Verdicts  []model.Verdict
	CacheHits int
}

// Scan captures and evaluates all panes, returning verdicts.
// This is the same logic as pane-patrol scan, but as a Go function call.
func (s *Scanner) Scan(ctx context.Context) (*ScanResult, error) {
	ctx, span := tracer.Start(ctx, "scan",
		trace.WithAttributes(
			attribute.String("filter", s.Filter),

			// Langfuse trace-level attributes
			attribute.String("langfuse.trace.name", "pane-supervisor-scan"),
			attribute.String("langfuse.session.id", s.SessionID),
			attribute.StringSlice("langfuse.trace.tags", []string{"pane-supervisor", "scan"}),
		))
	defer span.End()

	panes, err := s.Mux.ListPanes(ctx, s.Filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list panes: %w", err)
	}

	// Filter panes: skip self-target and excluded sessions.
	// Use a fresh slice to avoid aliasing the original backing array.
	filtered := make([]model.Pane, 0, len(panes))
	for _, p := range panes {
		if s.SelfTarget != "" && p.Target == s.SelfTarget {
			continue
		}
		if len(s.ExcludeSessions) > 0 && config.MatchesExcludeList(p.Session, s.ExcludeSessions) {
			continue
		}
		filtered = append(filtered, p)
	}
	panes = filtered

	if len(panes) == 0 {
		return &ScanResult{}, nil
	}

	verdicts := make([]model.Verdict, len(panes))
	cacheHits := int64(0)
	parallel := s.Parallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(panes) {
		parallel = len(panes)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)

	for i, pane := range panes {
		wg.Add(1)
		go func(idx int, p model.Pane) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			start := time.Now()
			v, err := s.evaluatePane(ctx, p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: pane %s: %v\n", p.Target, err)
				s.Metrics.RecordEvaluation(ctx, "error")
				verdicts[idx] = model.Verdict{
					Target:      p.Target,
					Session:     p.Session,
					Window:      p.Window,
					Pane:        p.Pane,
					Command:     p.Command,
					Agent:       "error",
					Blocked:     false,
					Reason:      fmt.Sprintf("evaluation failed: %v", err),
					Model:       s.Evaluator.Model(),
					Provider:    s.Evaluator.Provider(),
					EvaluatedAt: time.Now().UTC(),
					DurationMs:  time.Since(start).Milliseconds(),
				}
				return
			}
			// Detect cache hit: zero usage means no LLM call was made
			if v.Usage.InputTokens == 0 && v.Usage.OutputTokens == 0 {
				atomic.AddInt64(&cacheHits, 1)
			}
			verdicts[idx] = *v
		}(i, pane)
	}

	wg.Wait()

	result := &ScanResult{
		Verdicts:  verdicts,
		CacheHits: int(cacheHits),
	}

	// Record span attributes for the completed scan
	blocked := 0
	var totalIn, totalOut, totalCacheRead, totalCacheCreation int64
	for _, v := range verdicts {
		if v.Blocked {
			blocked++
		}
		totalIn += v.Usage.InputTokens
		totalOut += v.Usage.OutputTokens
		totalCacheRead += v.Usage.CacheReadInputTokens
		totalCacheCreation += v.Usage.CacheCreationInputTokens
	}
	span.SetAttributes(
		attribute.Int("panes.total", len(verdicts)),
		attribute.Int("panes.blocked", blocked),
		attribute.Int("cache.hits", int(cacheHits)),
		attribute.Int64("tokens.input", totalIn),
		attribute.Int64("tokens.output", totalOut),
		attribute.Int64("tokens.cache_read", totalCacheRead),
		attribute.Int64("tokens.cache_creation", totalCacheCreation),
	)

	return result, nil
}

func (s *Scanner) evaluatePane(ctx context.Context, pane model.Pane) (*model.Verdict, error) {
	ctx, span := tracer.Start(ctx, "evaluate_pane",
		trace.WithAttributes(
			attribute.String("pane.target", pane.Target),
			attribute.String("pane.session", pane.Session),
			attribute.String("pane.command", pane.Command),

			// Langfuse observation metadata (filterable top-level keys)
			attribute.String("langfuse.observation.metadata.pane_target", pane.Target),
			attribute.String("langfuse.observation.metadata.pane_session", pane.Session),
			attribute.String("langfuse.observation.metadata.pane_command", pane.Command),
		))
	defer span.End()

	start := time.Now()

	capture, err := s.Mux.CapturePane(ctx, pane.Target)
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}

	// Prepend process metadata for context.
	content := model.BuildProcessHeader(pane) + capture

	// Set the pane content as the observation input for Langfuse
	span.SetAttributes(attribute.String("langfuse.observation.input", content))

	// Check cache: if content hasn't changed, reuse the previous verdict
	if s.Cache != nil {
		if cached, ok := s.Cache.Lookup(pane.Target, content); ok {
			cached.DurationMs = time.Since(start).Milliseconds()
			// Zero out usage for cache hits — no LLM call was made
			cached.Usage = model.TokenUsage{}

			// Set output for Langfuse even on cache hits
			cachedOutput := map[string]any{
				"agent":   cached.Agent,
				"blocked": cached.Blocked,
				"reason":  cached.Reason,
			}
			if outJSON, err := json.Marshal(cachedOutput); err == nil {
				span.SetAttributes(attribute.String("langfuse.observation.output", string(outJSON)))
			}

			span.SetAttributes(
				attribute.Bool("cache.hit", true),
				attribute.String("verdict.agent", cached.Agent),
				attribute.Bool("verdict.blocked", cached.Blocked),
			)
			s.Metrics.RecordCacheHit(ctx)
			s.Metrics.RecordEvaluation(ctx, "cache")
			return cached, nil
		}
	}

	// --- Tier 1: Deterministic parser for known agents ---
	// Try parsers first — instant, free, 100% accurate for known agents.
	if s.Parsers != nil {
		if parsed := s.Parsers.Parse(capture, pane.ProcessTree); parsed != nil {
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
				Usage:       model.TokenUsage{}, // No LLM call
				Model:       "deterministic",
				Provider:    "parser",
				EvaluatedAt: time.Now().UTC(),
				DurationMs:  time.Since(start).Milliseconds(),
			}

			if s.Verbose {
				verdict.Content = content
			}

			// Langfuse output for parser results
			parserOutput := map[string]any{
				"agent":       verdict.Agent,
				"blocked":     verdict.Blocked,
				"reason":      verdict.Reason,
				"waiting_for": verdict.WaitingFor,
				"reasoning":   verdict.Reasoning,
				"source":      "deterministic_parser",
			}
			if outputJSON, err := json.Marshal(parserOutput); err == nil {
				span.SetAttributes(attribute.String("langfuse.observation.output", string(outputJSON)))
			}

			span.SetAttributes(
				attribute.Bool("cache.hit", false),
				attribute.Bool("parser.hit", true),
				attribute.String("verdict.agent", verdict.Agent),
				attribute.Bool("verdict.blocked", verdict.Blocked),
				attribute.String("langfuse.observation.metadata.verdict_agent", verdict.Agent),
				attribute.String("langfuse.observation.metadata.verdict_blocked", fmt.Sprintf("%v", verdict.Blocked)),
				attribute.String("langfuse.observation.metadata.verdict_source", "deterministic_parser"),
			)

			s.Metrics.RecordEvaluation(ctx, "parser")

			// Store in cache for future scans
			if s.Cache != nil {
				s.Cache.Store(pane.Target, content, *verdict)
			}

			return verdict, nil
		}
	}

	// --- Tier 2: LLM fallback for unrecognized agents ---
	// Record cache miss only when falling through to LLM (parser hits are
	// not cache misses in the meaningful sense — they bypass both caches).
	s.Metrics.RecordCacheMiss(ctx)
	llmVerdict, err := s.Evaluator.Evaluate(ctx, content)
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
		Model:       s.Evaluator.Model(),
		Provider:    s.Evaluator.Provider(),
		EvaluatedAt: time.Now().UTC(),
		DurationMs:  time.Since(start).Milliseconds(),
	}

	if s.Verbose {
		verdict.Content = content
	}

	// Set the verdict as the observation output for Langfuse
	verdictOutput := map[string]any{
		"agent":       verdict.Agent,
		"blocked":     verdict.Blocked,
		"reason":      verdict.Reason,
		"waiting_for": verdict.WaitingFor,
		"reasoning":   verdict.Reasoning,
		"actions":     verdict.Actions,
		"recommended": verdict.Recommended,
	}
	if outputJSON, err := json.Marshal(verdictOutput); err == nil {
		span.SetAttributes(attribute.String("langfuse.observation.output", string(outputJSON)))
	}

	span.SetAttributes(
		attribute.Bool("cache.hit", false),
		attribute.Bool("parser.hit", false),
		attribute.String("verdict.agent", verdict.Agent),
		attribute.Bool("verdict.blocked", verdict.Blocked),
		attribute.Int64("tokens.input", verdict.Usage.InputTokens),
		attribute.Int64("tokens.output", verdict.Usage.OutputTokens),
		attribute.Int64("tokens.cache_read", verdict.Usage.CacheReadInputTokens),
		attribute.Int64("tokens.cache_creation", verdict.Usage.CacheCreationInputTokens),

		// Langfuse filterable metadata for verdict results
		attribute.String("langfuse.observation.metadata.verdict_agent", verdict.Agent),
		attribute.String("langfuse.observation.metadata.verdict_blocked", fmt.Sprintf("%v", verdict.Blocked)),
		attribute.String("langfuse.observation.metadata.verdict_reason", verdict.Reason),
	)

	s.Metrics.RecordEvaluation(ctx, "llm")
	s.Metrics.RecordTokens(ctx,
		s.Evaluator.Provider(), s.Evaluator.Model(),
		verdict.Usage.InputTokens, verdict.Usage.OutputTokens,
		verdict.Usage.CacheReadInputTokens, verdict.Usage.CacheCreationInputTokens,
	)

	// Store in cache for future scans
	if s.Cache != nil {
		s.Cache.Store(pane.Target, content, *verdict)
	}

	return verdict, nil
}
