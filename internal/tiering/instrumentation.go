package tiering

import (
	"context"
	"time"
)

type Instrumenter interface {
	RecordReconcile(ctx context.Context, nodeID string, database string, table string, result string, duration time.Duration)
	RecordAction(ctx context.Context, entry HistoryEntry)
	RecordRetry(ctx context.Context, nodeID string, database string, table string, action Decision)
	RecordProbeFailure(ctx context.Context, nodeID string, database string, table string)
	RecordSideMerge(ctx context.Context, nodeID string, database string, table string, count uint64)
}

type noopInstrumenter struct{}

func (noopInstrumenter) RecordReconcile(ctx context.Context, _ string, _ string, _ string, _ string, _ time.Duration) {
	_ = ctx
}

func (noopInstrumenter) RecordAction(ctx context.Context, _ HistoryEntry) {
	_ = ctx
}

func (noopInstrumenter) RecordRetry(ctx context.Context, _ string, _ string, _ string, _ Decision) {
	_ = ctx
}

func (noopInstrumenter) RecordProbeFailure(ctx context.Context, _ string, _ string, _ string) {
	_ = ctx
}

func (noopInstrumenter) RecordSideMerge(ctx context.Context, _ string, _ string, _ string, _ uint64) {
	_ = ctx
}
