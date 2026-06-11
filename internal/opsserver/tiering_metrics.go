package opsserver

import (
	"context"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

var newTieringMetricsFn = NewTieringMetrics

type TieringMetrics struct {
	store             func() *tiering.Store
	reconciles        otelmetric.Int64Counter
	reconcileDuration otelmetric.Float64Histogram
	actions           otelmetric.Int64Counter
	actionDuration    otelmetric.Float64Histogram
	movedBytes        otelmetric.Int64Counter
	retries           otelmetric.Int64Counter
	coldBytes         otelmetric.Int64Counter
	sideMerges        otelmetric.Int64Counter
	probeFailures     otelmetric.Int64Counter
}

func NewTieringMetrics(meter otelmetric.Meter, store func() *tiering.Store) (*TieringMetrics, error) {
	reconciles, err := meter.Int64Counter("movoor.tiering.reconciles", otelmetric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	reconcileDuration, err := meter.Float64Histogram("movoor.tiering.reconcile.duration", otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60))
	if err != nil {
		return nil, err
	}
	actions, err := meter.Int64Counter("movoor.tiering.actions", otelmetric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	actionDuration, err := meter.Float64Histogram("movoor.tiering.action.duration", otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(1, 10, 60, 300, 900, 1800, 3600, 7200, 14400, 28800))
	if err != nil {
		return nil, err
	}
	movedBytes, err := meter.Int64Counter("movoor.tiering.moved.bytes", otelmetric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	retries, err := meter.Int64Counter("movoor.tiering.retries", otelmetric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	coldBytes, err := meter.Int64Counter("movoor.tiering.cold.bytes", otelmetric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	sideMerges, err := meter.Int64Counter("movoor.tiering.cold.side_merges", otelmetric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	probeFailures, err := meter.Int64Counter("movoor.tiering.probe.failures", otelmetric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	out := &TieringMetrics{
		store:             store,
		reconciles:        reconciles,
		reconcileDuration: reconcileDuration,
		actions:           actions,
		actionDuration:    actionDuration,
		movedBytes:        movedBytes,
		retries:           retries,
		coldBytes:         coldBytes,
		sideMerges:        sideMerges,
		probeFailures:     probeFailures,
	}
	if _, gaugeErr := meter.Int64ObservableGauge("movoor.tiering.partitions", otelmetric.WithInt64Callback(out.observePartitions)); gaugeErr != nil {
		return nil, gaugeErr
	}
	if _, gaugeErr := meter.Int64ObservableGauge("movoor.tiering.dispatch.paused", otelmetric.WithInt64Callback(out.observePaused)); gaugeErr != nil {
		return nil, gaugeErr
	}
	if _, gaugeErr := meter.Int64ObservableGauge("movoor.tiering.stuck", otelmetric.WithInt64Callback(out.observeStuck)); gaugeErr != nil {
		return nil, gaugeErr
	}
	return out, nil
}

func (m *TieringMetrics) RecordReconcile(ctx context.Context, nodeID string, database string, table string, result string, duration time.Duration) {
	attrs := tableAttrs(nodeID, database, table, attribute.String("result", result))
	m.reconciles.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
	m.reconcileDuration.Record(ctx, duration.Seconds(), otelmetric.WithAttributes(tableAttrs(nodeID, database, table)...))
}

func (m *TieringMetrics) RecordAction(ctx context.Context, entry tiering.HistoryEntry) {
	attrs := tableAttrs(entry.NodeID, entry.Database, entry.Table, attribute.String("action", string(entry.Action)), attribute.String("outcome", entry.Outcome))
	m.actions.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
	m.actionDuration.Record(ctx, entry.Duration.Seconds(), otelmetric.WithAttributes(tableAttrs(entry.NodeID, entry.Database, entry.Table, attribute.String("action", string(entry.Action)))...))
	// The optimize leg merges in place: no bytes move between disks and no
	// cold-tier traffic is generated, so only move legs feed the byte
	// counters — and only legs that actually succeeded: entry.Bytes is set
	// before execution, so a failed leg moved nothing.
	if entry.Outcome == "success" && entry.Bytes > 0 && entry.Action != tiering.DecisionOptimize {
		bytes := safeInt64(entry.Bytes)
		m.movedBytes.Add(ctx, bytes, otelmetric.WithAttributes(tableAttrs(entry.NodeID, entry.Database, entry.Table)...))
		m.coldBytes.Add(ctx, bytes, otelmetric.WithAttributes(tableAttrs(entry.NodeID, entry.Database, entry.Table, attribute.String("direction", direction(entry)))...))
	}
}

func (m *TieringMetrics) RecordRetry(ctx context.Context, nodeID string, database string, table string, action tiering.Decision) {
	m.retries.Add(ctx, 1, otelmetric.WithAttributes(tableAttrs(nodeID, database, table, attribute.String("action", string(action)))...))
}

func (m *TieringMetrics) RecordProbeFailure(ctx context.Context, nodeID string, database string, table string) {
	m.probeFailures.Add(ctx, 1, otelmetric.WithAttributes(tableAttrs(nodeID, database, table)...))
}

func (m *TieringMetrics) RecordSideMerge(ctx context.Context, nodeID string, database string, table string, count uint64) {
	m.sideMerges.Add(ctx, safeInt64(count), otelmetric.WithAttributes(tableAttrs(nodeID, database, table)...))
}

func (m *TieringMetrics) observePartitions(_ context.Context, observer otelmetric.Int64Observer) error {
	store := m.store()
	if store == nil {
		return nil
	}
	counts := make(map[[4]string]int64)
	for _, table := range store.Snapshot().Tables {
		for _, verdict := range table.Verdicts {
			key := [4]string{table.NodeID, table.Database + "." + table.Table, string(verdict.Status), string(verdict.Decision)}
			counts[key]++
		}
	}
	for key, count := range counts {
		observer.Observe(count, otelmetric.WithAttributes(
			attribute.String("node", key[0]),
			attribute.String("table", key[1]),
			attribute.String("status", key[2]),
			attribute.String("decision", key[3]),
		))
	}
	return nil
}

func (m *TieringMetrics) observePaused(_ context.Context, observer otelmetric.Int64Observer) error {
	store := m.store()
	if store == nil {
		return nil
	}
	status := store.Status()
	value := int64(0)
	if status.PauseState != tiering.PauseRunning {
		value = 1
	}
	reason := string(status.PauseReason)
	if reason == "" {
		reason = "none"
	}
	observer.Observe(value, otelmetric.WithAttributes(attribute.String("reason", reason)))
	return nil
}

func (m *TieringMetrics) observeStuck(_ context.Context, observer otelmetric.Int64Observer) error {
	store := m.store()
	if store == nil {
		return nil
	}
	counts := make(map[[2]string]int64)
	for _, table := range store.Snapshot().Tables {
		for _, verdict := range table.Verdicts {
			if verdict.Status == tiering.StatusStalled {
				counts[[2]string{table.NodeID, table.Database + "." + table.Table}]++
			}
		}
	}
	for key, count := range counts {
		observer.Observe(count, otelmetric.WithAttributes(attribute.String("node", key[0]), attribute.String("table", key[1])))
	}
	return nil
}

func tableAttrs(nodeID string, database string, table string, extra ...attribute.KeyValue) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("node", nodeID),
		attribute.String("table", database+"."+table),
	}
	return append(attrs, extra...)
}

func safeInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}

// direction labels a move leg's byte flow. The executor records it on the
// history entry (the consolidate leg's direction depends on table policy);
// "up" is the safe fallback for entries without one.
func direction(entry tiering.HistoryEntry) string {
	if entry.Direction != "" {
		return entry.Direction
	}
	return "up"
}
