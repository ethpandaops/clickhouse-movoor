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
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	otel.SetTracerProvider(provider)
	return provider, nil
}
