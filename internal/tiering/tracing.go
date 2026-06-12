package tiering

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation scope for every tiering span.
const tracerName = "github.com/ethpandaops/clickhouse-movoor/internal/tiering"

func tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// withoutQuerySpans strips the current span from ctx so driver-level
// statement spans are not created underneath it. Convergence polls re-query
// part state every couple of seconds for up to hours; a span per poll query
// would drown the action trace. Cancellation and other values survive.
func withoutQuerySpans(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.SpanContext{})
}
