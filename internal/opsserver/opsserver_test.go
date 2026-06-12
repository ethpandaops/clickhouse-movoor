package opsserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"testing"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

func TestServerHealthMetricsAndInstrumentation(t *testing.T) {
	store := tiering.NewStore(10)
	store.Publish(tiering.TablePlan{
		NodeID:       "node-a",
		Database:     "db",
		Table:        "tbl",
		ReconciledAt: time.Now().UTC(),
		Verdicts: []tiering.Verdict{{
			NodeID:      "node-a",
			Database:    "db",
			Table:       "tbl",
			PartitionID: "p1",
			Status:      tiering.StatusReady,
			Decision:    tiering.DecisionTier,
		}},
	})
	store.Pause(tiering.PauseReasonOperator)

	srv, err := New(slog.New(slog.DiscardHandler), Config{
		MetricsAddr:     "127.0.0.1:0",
		HealthCheckAddr: "127.0.0.1:0",
		Version:         "test",
		InstanceID:      "test-instance",
	})
	require.NoError(t, err)
	srv.SetTieringStore(store)
	srv.SetClickHouseStatus(ClickHouseStatus{
		Status:         ClickHouseReadinessDegraded,
		NodesExpected:  2,
		NodesResponded: 1,
		NodesFailed:    1,
		Warnings: []ClickHouseWarning{{
			Kind:    "reachability",
			Code:    "node_unreachable",
			Message: "connection refused",
			NodeID:  "node-b",
		}},
	})

	instrumenter := srv.TieringInstrumenter()
	instrumenter.RecordReconcile(t.Context(), "node-a", "db", "tbl", "success", time.Second)
	instrumenter.RecordAction(t.Context(), tiering.HistoryEntry{
		NodeID:    "node-a",
		Database:  "db",
		Table:     "tbl",
		Action:    tiering.DecisionTier,
		Outcome:   "success",
		Duration:  2 * time.Second,
		Bytes:     1024,
		Time:      time.Now().UTC(),
		AttemptID: "attempt",
	})
	// A failed leg moved nothing: it must increment the action counter (with
	// its outcome label) but never the byte counters — entry.Bytes is set
	// before execution.
	instrumenter.RecordAction(t.Context(), tiering.HistoryEntry{
		NodeID:   "node-a",
		Database: "db",
		Table:    "tbl",
		Action:   tiering.DecisionTier,
		Outcome:  "error",
		Bytes:    4096,
		Time:     time.Now().UTC(),
	})
	instrumenter.RecordRetry(t.Context(), "node-a", "db", "tbl", tiering.DecisionTier)
	instrumenter.RecordProbeFailure(t.Context(), "node-a", "db", "tbl")
	instrumenter.RecordSideMerge(t.Context(), "node-a", "db", "tbl", 1)

	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	require.Len(t, srv.listeners, 2)
	metricsBody := getBody(t, "http://"+srv.listeners[0].Addr().String()+"/metrics")
	require.Contains(t, metricsBody, "movoor_tiering_actions_total")
	require.Contains(t, metricsBody, "movoor_tiering_reconciles_total")
	require.Contains(t, metricsBody, "movoor_tiering_cold_bytes_total")
	// Outcome gating: only the 1024-byte success leg counts; the 4096-byte
	// failed leg must not inflate the byte counters.
	require.Contains(t, metricsBody, `movoor_tiering_moved_bytes_total{node="node-a",table="db.tbl"} 1024`)
	require.Contains(t, metricsBody, `movoor_tiering_actions_total{action="tier",node="node-a",outcome="error",table="db.tbl"} 1`)
	require.Contains(t, metricsBody, "movoor_tiering_retries_total")
	require.Contains(t, metricsBody, "movoor_tiering_cold_side_merges_total")
	require.Contains(t, metricsBody, "movoor_tiering_probe_failures_total")
	require.Contains(t, metricsBody, "movoor_tiering_partitions")
	require.NotContains(t, metricsBody, "movoor_tiering_partitions_ratio")
	require.Contains(t, metricsBody, "movoor_tiering_dispatch_paused")
	require.NotContains(t, metricsBody, "movoor_tiering_dispatch_paused_ratio")
	require.Contains(t, metricsBody, `reason="operator"`)

	healthBody := getBody(t, "http://"+srv.listeners[1].Addr().String()+"/healthz")
	require.Contains(t, healthBody, `"status":"ok"`)
	require.Contains(t, healthBody, `"readiness":"degraded"`)
	require.Contains(t, healthBody, `"nodesExpected":2`)
	require.Contains(t, healthBody, `"nodesResponded":1`)
	require.Contains(t, healthBody, `"nodesFailed":1`)
	require.Contains(t, healthBody, `"code":"node_unreachable"`)
	require.Contains(t, healthBody, `"mode":"`)
}

func TestServerNilAndHelpers(t *testing.T) {
	var srv *Server
	require.Nil(t, srv.TieringInstrumenter())
	srv.SetTieringStore(tiering.NewStore(1))
	srv.SetClickHouseStatus(ClickHouseStatus{Status: ClickHouseReadinessOK})
	require.NoError(t, srv.Start(t.Context()))
	require.NoError(t, srv.Stop(t.Context()))

	emptyStatusServer := &Server{}
	require.Equal(t, ClickHouseReadinessUnknown, emptyStatusServer.ClickHouseStatus().Status)
	emptyStatusServer.SetClickHouseStatus(ClickHouseStatus{})
	require.Equal(t, ClickHouseReadinessUnknown, emptyStatusServer.ClickHouseStatus().Status)

	warnings := []ClickHouseWarning{{Code: "warn"}}
	emptyStatusServer.SetClickHouseStatus(ClickHouseStatus{Status: ClickHouseReadinessDegraded, Warnings: warnings})
	warnings[0].Code = "mutated"
	status := emptyStatusServer.ClickHouseStatus()
	require.Equal(t, ClickHouseReadinessDegraded, status.Status)
	require.Equal(t, "warn", status.Warnings[0].Code)
	status.Warnings[0].Code = "mutated-again"
	require.Equal(t, "warn", emptyStatusServer.ClickHouseStatus().Warnings[0].Code)

	require.Equal(t, int64(math.MaxInt64), safeInt64(math.MaxUint64))
	require.Equal(t, int64(10), safeInt64(10))
	require.Equal(t, "down", direction(tiering.HistoryEntry{Direction: "down"}))
	require.Equal(t, "up", direction(tiering.HistoryEntry{Direction: "up"}))
	require.Equal(t, "up", direction(tiering.HistoryEntry{}))
	attrs := tableAttrs("n", "db", "tbl")
	require.Equal(t, "n", attrs[0].Value.AsString())
	require.Equal(t, "db.tbl", attrs[1].Value.AsString())
}

func TestServerDefaultsAndErrorPaths(t *testing.T) {
	srv, err := New(nil, Config{})
	require.NoError(t, err)
	require.Equal(t, "default", srv.cfg.InstanceID)
	require.NotNil(t, srv.log)
	require.NoError(t, srv.Start(t.Context()))
	require.NoError(t, srv.Stop(t.Context()))

	srv, err = New(slog.New(slog.DiscardHandler), Config{MetricsAddr: "127.0.0.1:-1"})
	require.NoError(t, err)
	require.ErrorContains(t, srv.Start(t.Context()), "listen")

	var lc net.ListenConfig
	occupied, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	srv, err = New(slog.New(slog.DiscardHandler), Config{
		MetricsAddr:     "127.0.0.1:0",
		HealthCheckAddr: occupied.Addr().String(),
	})
	require.NoError(t, err)
	require.ErrorContains(t, srv.Start(t.Context()), "listen")
	require.NoError(t, srv.Stop(t.Context()))

	srv, err = New(slog.New(slog.DiscardHandler), Config{MetricsAddr: "127.0.0.1:0"})
	require.NoError(t, err)
	require.NoError(t, srv.Start(t.Context()))
	require.NoError(t, srv.Stop(t.Context()))

	srv, err = New(slog.New(slog.DiscardHandler), Config{})
	require.NoError(t, err)
	require.NoError(t, srv.startHTTP(t.Context(), "127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}), &srv.metrics, "metrics"))
	require.NoError(t, srv.listeners[0].Close())

	srv.handleHealth(failingResponseWriter{}, newHealthRequest(t))
}

func TestServerConstructorInjectedErrors(t *testing.T) {
	oldPrometheusReader := newPrometheusReader
	oldTraceExporter := newOTLPTraceExporter
	oldTieringMetrics := newTieringMetricsFn
	oldRuntimeStart := startRuntimeMetrics
	t.Cleanup(func() {
		newPrometheusReader = oldPrometheusReader
		newOTLPTraceExporter = oldTraceExporter
		newTieringMetricsFn = oldTieringMetrics
		startRuntimeMetrics = oldRuntimeStart
	})

	newPrometheusReader = func(*promclient.Registry) (sdkmetric.Reader, error) {
		return nil, errors.New("prometheus failed")
	}
	_, err := New(slog.New(slog.DiscardHandler), Config{})
	require.ErrorContains(t, err, "prometheus failed")
	newPrometheusReader = oldPrometheusReader

	newOTLPTraceExporter = func(context.Context, string) (sdktrace.SpanExporter, error) {
		return nil, errors.New("trace failed")
	}
	_, err = New(slog.New(slog.DiscardHandler), Config{TracingEndpoint: "127.0.0.1:4317", TraceSampleRatio: 1})
	require.ErrorContains(t, err, "trace failed")
	newOTLPTraceExporter = oldTraceExporter

	newTieringMetricsFn = func(otelmetric.Meter, func() *tiering.Store) (*TieringMetrics, error) {
		return nil, errors.New("metrics failed")
	}
	_, err = New(slog.New(slog.DiscardHandler), Config{})
	require.ErrorContains(t, err, "metrics failed")
	newTieringMetricsFn = oldTieringMetrics

	startRuntimeMetrics = func(...otelruntime.Option) error {
		return errors.New("runtime failed")
	}
	_, err = New(slog.New(slog.DiscardHandler), Config{})
	require.ErrorContains(t, err, "runtime failed")
}

func TestServerNewWithTracingAndStoreCallback(t *testing.T) {
	oldTraceExporter := newOTLPTraceExporter
	oldTieringMetrics := newTieringMetricsFn
	oldRuntimeStart := startRuntimeMetrics
	t.Cleanup(func() {
		newOTLPTraceExporter = oldTraceExporter
		newTieringMetricsFn = oldTieringMetrics
		startRuntimeMetrics = oldRuntimeStart
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	})

	newOTLPTraceExporter = func(context.Context, string) (sdktrace.SpanExporter, error) {
		return noopSpanExporter{}, nil
	}
	newTieringMetricsFn = func(_ otelmetric.Meter, store func() *tiering.Store) (*TieringMetrics, error) {
		require.Nil(t, store())
		return NewTieringMetrics(noopmetric.NewMeterProvider().Meter("test-new"), store)
	}
	startRuntimeMetrics = func(...otelruntime.Option) error {
		return nil
	}

	srv, err := New(slog.New(slog.DiscardHandler), Config{TracingEndpoint: "127.0.0.1:4317", TraceSampleRatio: 1})
	require.NoError(t, err)
	require.NotNil(t, srv.tracer)
	require.NoError(t, srv.Stop(t.Context()))
}

func TestServerStopInjectedErrors(t *testing.T) {
	oldShutdownHTTPServer := shutdownHTTPServer
	t.Cleanup(func() { shutdownHTTPServer = oldShutdownHTTPServer })

	shutdownHTTPServer = func(context.Context, *http.Server) error {
		return errors.New("http shutdown failed")
	}
	require.ErrorContains(t, (&Server{metrics: &http.Server{ReadHeaderTimeout: readHeaderTimeout}}).Stop(t.Context()), "http shutdown failed")
	shutdownHTTPServer = oldShutdownHTTPServer

	require.ErrorContains(t, (&Server{provider: errShutdowner{err: errors.New("provider shutdown failed")}}).Stop(t.Context()), "provider shutdown failed")
	require.ErrorContains(t, (&Server{tracer: errShutdowner{err: errors.New("tracer shutdown failed")}}).Stop(t.Context()), "tracer shutdown failed")
}

func TestTracingProvider(t *testing.T) {
	t.Cleanup(func() {
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	})
	provider, err := newTracerProvider(Config{TracingEndpoint: "127.0.0.1:1", TraceSampleRatio: 2}, nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.NoError(t, provider.Shutdown(t.Context()))

	provider, err = newTracerProvider(Config{}, nil)
	require.NoError(t, err)
	require.Nil(t, provider)
}

func TestTieringMetricsCreationErrorsAndNilStores(t *testing.T) {
	names := []string{
		"movoor.tiering.reconciles",
		"movoor.tiering.reconcile.duration",
		"movoor.tiering.actions",
		"movoor.tiering.action.duration",
		"movoor.tiering.moved.bytes",
		"movoor.tiering.retries",
		"movoor.tiering.cold.bytes",
		"movoor.tiering.cold.side_merges",
		"movoor.tiering.probe.failures",
		"movoor.tiering.partitions",
		"movoor.tiering.dispatch.paused",
		"movoor.tiering.stuck",
	}
	for _, name := range names {
		_, err := NewTieringMetrics(errMeter{failName: name}, nil)
		require.ErrorContains(t, err, name)
	}

	metrics, err := NewTieringMetrics(noopmetric.NewMeterProvider().Meter("test"), func() *tiering.Store { return nil })
	require.NoError(t, err)
	require.NoError(t, metrics.observePartitions(t.Context(), noopmetric.Int64Observer{}))
	require.NoError(t, metrics.observePaused(t.Context(), noopmetric.Int64Observer{}))
	require.NoError(t, metrics.observeStuck(t.Context(), noopmetric.Int64Observer{}))

	store := tiering.NewStore(1)
	store.SetStatus(tiering.StatusSnapshot{PauseState: tiering.PauseRunning})
	store.Publish(tiering.TablePlan{
		NodeID:   "node-a",
		Database: "db",
		Table:    "tbl",
		Verdicts: []tiering.Verdict{{
			Status: tiering.StatusStalled,
		}},
	})
	metrics.store = func() *tiering.Store { return store }
	require.NoError(t, metrics.observePaused(t.Context(), noopmetric.Int64Observer{}))
	require.NoError(t, metrics.observeStuck(t.Context(), noopmetric.Int64Observer{}))
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return string(body)
}

type errMeter struct {
	noopmetric.Meter
	failName string
}

type errShutdowner struct {
	err error
}

func (s errShutdowner) Shutdown(context.Context) error {
	return s.err
}

type noopSpanExporter struct{}

func (noopSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}

func (noopSpanExporter) Shutdown(context.Context) error {
	return nil
}

func (m errMeter) Int64Counter(name string, opts ...otelmetric.Int64CounterOption) (otelmetric.Int64Counter, error) {
	if name == m.failName {
		return nil, errors.New(name)
	}
	return m.Meter.Int64Counter(name, opts...)
}

func (m errMeter) Float64Histogram(name string, opts ...otelmetric.Float64HistogramOption) (otelmetric.Float64Histogram, error) {
	if name == m.failName {
		return nil, errors.New(name)
	}
	return m.Meter.Float64Histogram(name, opts...)
}

func (m errMeter) Int64ObservableGauge(name string, opts ...otelmetric.Int64ObservableGaugeOption) (otelmetric.Int64ObservableGauge, error) {
	if name == m.failName {
		return nil, errors.New(name)
	}
	return m.Meter.Int64ObservableGauge(name, opts...)
}

type failingResponseWriter struct{}

func (failingResponseWriter) Header() http.Header {
	return http.Header{}
}

func (failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func (failingResponseWriter) WriteHeader(int) {}

func newHealthRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	require.NoError(t, err)
	return req
}
