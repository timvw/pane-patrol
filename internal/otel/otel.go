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
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "pane-supervisor"

// Version is set by the caller (from the linker-injected cmd.Version).
// Defaults to "dev" if not set.
var Version = "dev"

// OTELConfig holds the configuration needed by the OTEL init.
type OTELConfig struct {
	Endpoint string // OTLP base URL, e.g. "http://localhost:3000/api/public/otel"
	Headers  string // Comma-separated key=value pairs, e.g. "Authorization=Basic abc123"
}

// Telemetry holds the OTEL providers and metric instruments.
type Telemetry struct {
	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider

	Tracer  trace.Tracer
	Metrics *Metrics
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
			semconv.ServiceVersion(Version),
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

	// Create tracer (works even without exporters — just no-ops)
	t.Tracer = otel.Tracer(serviceName)

	// Create metric instruments (no-op when no MeterProvider is registered)
	metrics, err := NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("otel metrics: %w", err)
	}
	t.Metrics = metrics

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
