package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "pane-supervisor"

// Metrics holds all OTEL metric instruments for pane-supervisor.
// All counters are cumulative (monotonic) and safe for concurrent use.
type Metrics struct {
	// Verdict cache counters
	VerdictCacheHits          metric.Int64Counter
	VerdictCacheMisses        metric.Int64Counter
	VerdictCacheInvalidations metric.Int64Counter

	// Evaluation counters (partitioned by source: parser, cache, error)
	Evaluations metric.Int64Counter
}

// NewMetrics creates all metric instruments. Returns no-op instruments
// when no MeterProvider is registered (safe to call unconditionally).
func NewMetrics() (*Metrics, error) {
	meter := otel.Meter(meterName)
	m := &Metrics{}
	var err error

	// --- Verdict cache counters ---

	m.VerdictCacheHits, err = meter.Int64Counter("verdict_cache.hits",
		metric.WithDescription("Number of verdict cache hits (pane content unchanged, reused previous verdict)"))
	if err != nil {
		return nil, err
	}

	m.VerdictCacheMisses, err = meter.Int64Counter("verdict_cache.misses",
		metric.WithDescription("Number of verdict cache misses (content changed, TTL expired, or first evaluation)"))
	if err != nil {
		return nil, err
	}

	m.VerdictCacheInvalidations, err = meter.Int64Counter("verdict_cache.invalidations",
		metric.WithDescription("Number of explicit verdict cache invalidations (e.g. after nudge)"))
	if err != nil {
		return nil, err
	}

	// --- Evaluation counters ---

	m.Evaluations, err = meter.Int64Counter("evaluations.total",
		metric.WithDescription("Total pane evaluations partitioned by source (parser, cache, error)"))
	if err != nil {
		return nil, err
	}

	return m, nil
}

// RecordCacheHit records a verdict cache hit.
func (m *Metrics) RecordCacheHit(ctx context.Context) {
	if m == nil {
		return
	}
	m.VerdictCacheHits.Add(ctx, 1)
}

// RecordCacheMiss records a verdict cache miss.
func (m *Metrics) RecordCacheMiss(ctx context.Context) {
	if m == nil {
		return
	}
	m.VerdictCacheMisses.Add(ctx, 1)
}

// RecordCacheInvalidation records an explicit cache invalidation.
func (m *Metrics) RecordCacheInvalidation(ctx context.Context) {
	if m == nil {
		return
	}
	m.VerdictCacheInvalidations.Add(ctx, 1)
}

// RecordEvaluation records a pane evaluation with the given source.
func (m *Metrics) RecordEvaluation(ctx context.Context, source string) {
	if m == nil {
		return
	}
	m.Evaluations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("evaluation.source", source),
	))
}
