package opsserver

import (
	"context"
	"fmt"

	promclient "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	newPrometheusReader = func(registry *promclient.Registry) (sdkmetric.Reader, error) {
		return otelprom.New(
			otelprom.WithRegisterer(registry),
			otelprom.WithoutScopeInfo(),
		)
	}
	newOTLPTraceExporter = func(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
		return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	}
	startRuntimeMetrics = runtime.Start
)

type shutdowner interface {
	Shutdown(context.Context) error
}

type telemetryBundle struct {
	registry *promclient.Registry
	provider shutdowner
	tracer   shutdowner
	meter    otelmetric.Meter
}

func newTelemetry(cfg Config) (telemetryBundle, error) {
	registry := promclient.NewRegistry()
	exporter, err := newPrometheusReader(registry)
	if err != nil {
		return telemetryBundle{}, fmt.Errorf("create prometheus exporter: %w", err)
	}
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("movoor"),
		semconv.ServiceVersion(cfg.Version),
		semconv.ServiceInstanceID(cfg.InstanceID),
	)
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	)
	tracerProvider, err := newTracerProvider(cfg, res)
	if err != nil {
		return telemetryBundle{}, err
	}
	var tracerShutdown shutdowner
	if tracerProvider != nil {
		tracerShutdown = tracerProvider
	}
	if runtimeErr := startRuntimeMetrics(runtime.WithMeterProvider(provider)); runtimeErr != nil {
		return telemetryBundle{}, fmt.Errorf("start runtime metrics: %w", runtimeErr)
	}
	return telemetryBundle{
		registry: registry,
		provider: provider,
		tracer:   tracerShutdown,
		meter:    provider.Meter("github.com/ethpandaops/clickhouse-movoor/internal/tiering"),
	}, nil
}

func newTracerProvider(cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	if cfg.TracingEndpoint == "" {
		return nil, nil
	}
	// Clamp to the valid range only: an explicit 0 is a legitimate "record
	// no samples" and must not be coerced back to sampling everything.
	ratio := min(max(cfg.TraceSampleRatio, 0), 1)
	exporter, err := newOTLPTraceExporter(context.Background(), cfg.TracingEndpoint)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(newSampler(ratio, cfg.AlwaysSampleSpans)),
	)
	otel.SetTracerProvider(provider)
	return provider, nil
}

// newSampler builds the trace sampler: parent-based ratio sampling, with the
// alwaysSample span names forced through both as roots and as children of
// unsampled parents (a dispatched action must not vanish because its
// reconcile tick lost the dice roll). Ratio zero means "record no samples"
// and wins over the allowlist.
func newSampler(ratio float64, alwaysSample []string) sdktrace.Sampler {
	base := sdktrace.TraceIDRatioBased(ratio)
	if ratio <= 0 || len(alwaysSample) == 0 {
		return sdktrace.ParentBased(base)
	}
	names := make(map[string]struct{}, len(alwaysSample))
	for _, name := range alwaysSample {
		names[name] = struct{}{}
	}
	return sdktrace.ParentBased(
		spanNameSampler{names: names, fallback: base},
		sdktrace.WithLocalParentNotSampled(spanNameSampler{names: names, fallback: sdktrace.NeverSample()}),
	)
}

// spanNameSampler samples spans on its name allowlist and defers everything
// else to the wrapped sampler.
type spanNameSampler struct {
	names    map[string]struct{}
	fallback sdktrace.Sampler
}

func (s spanNameSampler) ShouldSample(params sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if _, ok := s.names[params.Name]; ok {
		return sdktrace.SamplingResult{
			Decision:   sdktrace.RecordAndSample,
			Tracestate: trace.SpanContextFromContext(params.ParentContext).TraceState(),
		}
	}
	return s.fallback.ShouldSample(params)
}

func (s spanNameSampler) Description() string {
	return fmt.Sprintf("SpanNameOverride(%d names, fallback %s)", len(s.names), s.fallback.Description())
}
