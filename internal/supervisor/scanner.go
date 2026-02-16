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
	"github.com/timvw/pane-patrol/internal/model"
	"github.com/timvw/pane-patrol/internal/mux"
	ppotel "github.com/timvw/pane-patrol/internal/otel"
	"github.com/timvw/pane-patrol/internal/parser"
)

var tracer = otel.Tracer("pane-supervisor")

// Scanner wraps the pane-patrol scan functionality for use by the supervisor.
type Scanner struct {
	Mux             mux.Multiplexer
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
				v := model.BaseVerdict(p, start)
				v.Agent = "error"
				v.Reason = fmt.Sprintf("evaluation failed: %v", err)
				v.EvalSource = model.EvalSourceError
				verdicts[idx] = v
				return
			}
			if v.EvalSource == model.EvalSourceCache {
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
	for _, v := range verdicts {
		if v.Blocked {
			blocked++
		}
	}
	span.SetAttributes(
		attribute.Int("panes.total", len(verdicts)),
		attribute.Int("panes.blocked", blocked),
		attribute.Int("cache.hits", int(cacheHits)),
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
			cached.EvalSource = model.EvalSourceCache

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

	// --- Deterministic parser for known agents ---
	// Try parsers — instant, free, 100% accurate for known agents.
	if s.Parsers != nil {
		if parsed := s.Parsers.Parse(capture, pane.ProcessTree); parsed != nil {
			v := model.BaseVerdict(pane, start)
			v.Agent = parsed.Agent
			v.Blocked = parsed.Blocked
			v.Reason = parsed.Reason
			v.WaitingFor = parsed.WaitingFor
			v.Reasoning = parsed.Reasoning
			v.Actions = parsed.Actions
			v.Recommended = parsed.Recommended
			v.EvalSource = model.EvalSourceParser
			verdict := &v

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

	// --- No parser matched — return unknown verdict ---
	v := model.BaseVerdict(pane, start)
	v.Agent = "unknown"
	v.Blocked = false
	v.Reason = "not recognized by deterministic parsers"
	v.EvalSource = model.EvalSourceParser
	verdict := &v

	if s.Verbose {
		verdict.Content = content
	}

	// Langfuse output for unknown results
	unknownOutput := map[string]any{
		"agent":   verdict.Agent,
		"blocked": verdict.Blocked,
		"reason":  verdict.Reason,
		"source":  "parser_fallthrough",
	}
	if outputJSON, err := json.Marshal(unknownOutput); err == nil {
		span.SetAttributes(attribute.String("langfuse.observation.output", string(outputJSON)))
	}

	span.SetAttributes(
		attribute.Bool("cache.hit", false),
		attribute.Bool("parser.hit", false),
		attribute.String("verdict.agent", verdict.Agent),
		attribute.Bool("verdict.blocked", verdict.Blocked),
		attribute.String("langfuse.observation.metadata.verdict_agent", verdict.Agent),
		attribute.String("langfuse.observation.metadata.verdict_blocked", fmt.Sprintf("%v", verdict.Blocked)),
		attribute.String("langfuse.observation.metadata.verdict_source", "parser_fallthrough"),
	)

	s.Metrics.RecordEvaluation(ctx, "parser")

	// Store in cache for future scans
	if s.Cache != nil {
		s.Cache.Store(pane.Target, content, *verdict)
	}

	return verdict, nil
}
