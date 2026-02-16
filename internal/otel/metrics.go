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
	// LLM token counters (partitioned by provider + model via attributes)
	InputTokens         metric.Int64Counter
	OutputTokens        metric.Int64Counter
	CacheReadTokens     metric.Int64Counter
	CacheCreationTokens metric.Int64Counter

	// Verdict cache counters
	VerdictCacheHits          metric.Int64Counter
	VerdictCacheMisses        metric.Int64Counter
	VerdictCacheInvalidations metric.Int64Counter

	// Evaluation counters (partitioned by source: parser, llm, cache, error)
	Evaluations metric.Int64Counter
}

// NewMetrics creates all metric instruments. Returns no-op instruments
// when no MeterProvider is registered (safe to call unconditionally).
func NewMetrics() (*Metrics, error) {
	meter := otel.Meter(meterName)
	m := &Metrics{}
	var err error

	// --- LLM token counters ---

	m.InputTokens, err = meter.Int64Counter("llm.tokens.input",
		metric.WithDescription("Total LLM input tokens consumed"),
		metric.WithUnit("{token}"))
	if err != nil {
		return nil, err
	}

	m.OutputTokens, err = meter.Int64Counter("llm.tokens.output",
		metric.WithDescription("Total LLM output tokens consumed"),
		metric.WithUnit("{token}"))
	if err != nil {
		return nil, err
	}

	m.CacheReadTokens, err = meter.Int64Counter("llm.tokens.cache_read",
		metric.WithDescription("Total input tokens served from provider prompt cache"),
		metric.WithUnit("{token}"))
	if err != nil {
		return nil, err
	}

	m.CacheCreationTokens, err = meter.Int64Counter("llm.tokens.cache_creation",
		metric.WithDescription("Total input tokens used to create provider prompt cache entries"),
		metric.WithUnit("{token}"))
	if err != nil {
		return nil, err
	}

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
		metric.WithDescription("Total pane evaluations partitioned by source (parser, llm, cache, error)"))
	if err != nil {
		return nil, err
	}

	return m, nil
}

// RecordTokens records LLM token usage on the metric counters.
func (m *Metrics) RecordTokens(ctx context.Context, provider, model string, input, output, cacheRead, cacheCreation int64) {
	if m == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("llm.provider", provider),
		attribute.String("llm.model", model),
	)
	m.InputTokens.Add(ctx, input, attrs)
	m.OutputTokens.Add(ctx, output, attrs)
	if cacheRead > 0 {
		m.CacheReadTokens.Add(ctx, cacheRead, attrs)
	}
	if cacheCreation > 0 {
		m.CacheCreationTokens.Add(ctx, cacheCreation, attrs)
	}
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
