// Package otel provides OpenTelemetry initialization for pane-supervisor.
//
// Exports traces and metrics to an OTLP endpoint (configurable via config
// file, OTEL_EXPORTER_OTLP_ENDPOINT, or standard OTEL env vars).
// If no endpoint is set, telemetry is a no-op.
//
// Supports custom headers (e.g. for Langfuse authentication) via config
// file or OTEL_EXPORTER_OTLP_HEADERS env var.
package otel

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	serviceName    = "pane-supervisor"
	serviceVersion = "0.1.0"
)

// OTELConfig holds the configuration needed by the OTEL init.
type OTELConfig struct {
	Endpoint string // OTLP base URL, e.g. "http://localhost:3000/api/public/otel"
	Headers  string // Comma-separated key=value pairs, e.g. "Authorization=Basic abc123"
}

// Telemetry holds the OTEL providers and instruments.
type Telemetry struct {
	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider

	// Pre-created instruments for hot paths
	Tracer       trace.Tracer
	ScanDuration metric.Float64Histogram
	EvalDuration metric.Float64Histogram
	TokensInput  metric.Int64Counter
	TokensOutput metric.Int64Counter
	CacheHits    metric.Int64Counter
	CacheMisses  metric.Int64Counter
	PanesScanned metric.Int64Counter
	PanesBlocked metric.Int64Counter
}

// parseHeaders parses a comma-separated "key=value,key2=value2" string into a map.
// This matches the OTEL_EXPORTER_OTLP_HEADERS format.
func parseHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	if raw == "" {
		return headers
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if idx := strings.IndexByte(pair, '='); idx > 0 {
			key := strings.TrimSpace(pair[:idx])
			val := strings.TrimSpace(pair[idx+1:])
			if key != "" {
				headers[key] = val
			}
		}
	}
	return headers
}

// Init initializes OTEL with OTLP HTTP exporters.
// If cfg.Endpoint is empty, returns a no-op Telemetry
// (tracer and meters still work, they just don't export anywhere).
func Init(ctx context.Context, cfg OTELConfig) (*Telemetry, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	t := &Telemetry{}

	// Only set up real exporters if an endpoint is configured
	if cfg.Endpoint != "" {
		headers := parseHeaders(cfg.Headers)

		// Parse the endpoint URL to extract host and path components.
		// We use WithEndpoint (host:port) + WithURLPath so the SDK
		// appends the standard signal suffixes (/v1/traces, /v1/metrics).
		u, err := url.Parse(cfg.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("otel: invalid endpoint URL %q: %w", cfg.Endpoint, err)
		}
		host := u.Host // host:port
		basePath := strings.TrimRight(u.Path, "/")

		traceOpts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(host),
			otlptracehttp.WithURLPath(basePath + "/v1/traces"),
		}
		metricOpts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(host),
			otlpmetrichttp.WithURLPath(basePath + "/v1/metrics"),
		}

		// Use insecure transport for http:// endpoints
		if u.Scheme == "http" {
			traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
			metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
		}

		if len(headers) > 0 {
			traceOpts = append(traceOpts, otlptracehttp.WithHeaders(headers))
			metricOpts = append(metricOpts, otlpmetrichttp.WithHeaders(headers))
		}

		// Trace exporter
		traceExp, err := otlptracehttp.New(ctx, traceOpts...)
		if err != nil {
			return nil, fmt.Errorf("otel trace exporter: %w", err)
		}
		t.tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithResource(res),
		)

		// Metric exporter
		metricExp, err := otlpmetrichttp.New(ctx, metricOpts...)
		if err != nil {
			return nil, fmt.Errorf("otel metric exporter: %w", err)
		}
		t.mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
				sdkmetric.WithInterval(15*time.Second))),
			sdkmetric.WithResource(res),
		)

		otel.SetTracerProvider(t.tp)
		otel.SetMeterProvider(t.mp)
	}

	// Create tracer and meter (works even without exporters — just no-ops)
	t.Tracer = otel.Tracer(serviceName)
	meter := otel.Meter(serviceName)

	// Pre-create metric instruments
	t.ScanDuration, _ = meter.Float64Histogram("scan.duration",
		metric.WithDescription("Time for a full scan cycle in seconds"),
		metric.WithUnit("s"))
	t.EvalDuration, _ = meter.Float64Histogram("eval.duration",
		metric.WithDescription("Time for a single pane evaluation in seconds"),
		metric.WithUnit("s"))
	t.TokensInput, _ = meter.Int64Counter("tokens.input",
		metric.WithDescription("Total LLM input tokens consumed"))
	t.TokensOutput, _ = meter.Int64Counter("tokens.output",
		metric.WithDescription("Total LLM output tokens consumed"))
	t.CacheHits, _ = meter.Int64Counter("cache.hits",
		metric.WithDescription("Number of cache hits (LLM call skipped)"))
	t.CacheMisses, _ = meter.Int64Counter("cache.misses",
		metric.WithDescription("Number of cache misses (LLM call made)"))
	t.PanesScanned, _ = meter.Int64Counter("panes.scanned",
		metric.WithDescription("Total panes scanned"))
	t.PanesBlocked, _ = meter.Int64Counter("panes.blocked",
		metric.WithDescription("Total panes found blocked"))

	return t, nil
}

// Shutdown flushes and shuts down all OTEL providers.
func (t *Telemetry) Shutdown(ctx context.Context) {
	if t.tp != nil {
		_ = t.tp.Shutdown(ctx)
	}
	if t.mp != nil {
		_ = t.mp.Shutdown(ctx)
	}
}

// RecordScan records metrics for a completed scan cycle.
func (t *Telemetry) RecordScan(ctx context.Context, duration time.Duration, panes, blocked, cacheHits int, inputTokens, outputTokens int64) {
	attrs := metric.WithAttributes(
		attribute.Int("panes.total", panes),
		attribute.Int("panes.blocked", blocked),
		attribute.Int("cache.hits", cacheHits),
	)
	t.ScanDuration.Record(ctx, duration.Seconds(), attrs)
	t.PanesScanned.Add(ctx, int64(panes))
	t.PanesBlocked.Add(ctx, int64(blocked))
	t.CacheHits.Add(ctx, int64(cacheHits))
	t.CacheMisses.Add(ctx, int64(panes-cacheHits))
	t.TokensInput.Add(ctx, inputTokens)
	t.TokensOutput.Add(ctx, outputTokens)
}

// RecordEval records metrics for a single pane evaluation.
func (t *Telemetry) RecordEval(ctx context.Context, target string, duration time.Duration, cached bool, inputTokens, outputTokens int64) {
	attrs := metric.WithAttributes(
		attribute.String("pane.target", target),
		attribute.Bool("cache.hit", cached),
	)
	t.EvalDuration.Record(ctx, duration.Seconds(), attrs)
}
