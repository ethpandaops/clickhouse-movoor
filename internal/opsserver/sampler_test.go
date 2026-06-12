package opsserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func samplingParams(name string, parent trace.SpanContext) sdktrace.SamplingParameters {
	traceID := parent.TraceID()
	if !traceID.IsValid() {
		traceID = trace.TraceID{0x01}
	}
	return sdktrace.SamplingParameters{
		ParentContext: trace.ContextWithSpanContext(context.Background(), parent),
		TraceID:       traceID,
		Name:          name,
	}
}

func sampledParent() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01},
		SpanID:     trace.SpanID{0x01},
		TraceFlags: trace.FlagsSampled,
	})
}

func unsampledParent() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x01},
		SpanID:  trace.SpanID{0x01},
	})
}

func TestNewSamplerAlwaysSampleOverride(t *testing.T) {
	sampler := newSampler(0.000001, []string{"tiering.action"})

	tests := []struct {
		name     string
		span     string
		parent   trace.SpanContext
		decision sdktrace.SamplingDecision
	}{
		{
			name:     "root action span is always sampled",
			span:     "tiering.action",
			parent:   trace.SpanContext{},
			decision: sdktrace.RecordAndSample,
		},
		{
			name:     "action under unsampled parent is rescued",
			span:     "tiering.action",
			parent:   unsampledParent(),
			decision: sdktrace.RecordAndSample,
		},
		{
			name:     "other span under unsampled parent stays dropped",
			span:     "tiering.reconcile",
			parent:   unsampledParent(),
			decision: sdktrace.Drop,
		},
		{
			name:     "other span under sampled parent follows the parent",
			span:     "tiering.reconcile",
			parent:   sampledParent(),
			decision: sdktrace.RecordAndSample,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sampler.ShouldSample(samplingParams(tt.span, tt.parent))
			require.Equal(t, tt.decision, result.Decision)
		})
	}
}

func TestSpanNameSamplerDescription(t *testing.T) {
	sampler := spanNameSampler{
		names:    map[string]struct{}{"tiering.action": {}},
		fallback: sdktrace.NeverSample(),
	}
	require.Equal(t, "SpanNameOverride(1 names, fallback AlwaysOffSampler)", sampler.Description())
}

func TestNewSamplerRatioZeroDisablesOverride(t *testing.T) {
	sampler := newSampler(0, []string{"tiering.action"})

	result := sampler.ShouldSample(samplingParams("tiering.action", trace.SpanContext{}))
	require.Equal(t, sdktrace.Drop, result.Decision)
}

func TestNewSamplerNoAllowlistIsPlainParentBased(t *testing.T) {
	sampler := newSampler(1, nil)

	result := sampler.ShouldSample(samplingParams("tiering.action", unsampledParent()))
	require.Equal(t, sdktrace.Drop, result.Decision)

	result = sampler.ShouldSample(samplingParams("anything", sampledParent()))
	require.Equal(t, sdktrace.RecordAndSample, result.Decision)
}
