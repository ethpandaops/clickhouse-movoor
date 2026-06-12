package tiering

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// ensureNotPaused rejects supervised writes while dispatch is paused — the
// pause control means "no new tiering writes", regardless of who asks.
func (c *controller) ensureNotPaused() error {
	status := c.store.Status()
	if status.PauseState != PauseRunning {
		reason := status.PauseReason
		if reason == "" {
			reason = PauseReasonOperator
		}
		return fmt.Errorf("%w (%s)", ErrTieringPaused, reason)
	}
	return nil
}

func (c *controller) Retry(ctx context.Context, nodeID string, database string, table string, partitionID string, stateToken string) (HistoryEntry, error) {
	if err := c.ensureNotPaused(); err != nil {
		return HistoryEntry{}, err
	}
	planned, ok := c.store.FindVerdict(nodeID, database, table, partitionID)
	if !ok {
		return HistoryEntry{}, fmt.Errorf("%w: %s", ErrPartitionNotFound, partitionID)
	}
	if err := CheckToken(planned, stateToken); err != nil {
		return HistoryEntry{}, err
	}
	return c.Apply(ctx, nodeID, database, table, partitionID, stateToken)
}

func (c *controller) Apply(ctx context.Context, nodeID string, database string, table string, partitionID string, stateToken string) (HistoryEntry, error) {
	if err := c.ensureNotPaused(); err != nil {
		return HistoryEntry{}, err
	}
	client, ok := c.client(nodeID)
	if !ok {
		return HistoryEntry{}, fmt.Errorf("%w: %q", ErrNodeNotConfigured, nodeID)
	}
	watch, ok := c.watch(database, table)
	if !ok || watch.Settings == nil {
		return HistoryEntry{}, fmt.Errorf("%w: %s.%s", ErrWatchNotConfigured, database, table)
	}
	planned, ok := c.store.FindVerdict(nodeID, database, table, partitionID)
	if !ok {
		return HistoryEntry{}, fmt.Errorf("%w: %s", ErrPartitionNotFound, partitionID)
	}
	if err := CheckToken(planned, stateToken); err != nil {
		return HistoryEntry{}, err
	}
	tableObs, err := c.observeTable(ctx, client, watch)
	if err != nil {
		return HistoryEntry{}, err
	}
	for _, verdict := range DecideTable(tableObs, decisionTime(tableObs)) {
		if verdict.PartitionID != partitionID {
			continue
		}
		// CAS against the FRESH verdict, not just the cached plan: the
		// partition may have advanced between the plan the operator saw and
		// this apply, and the re-derived decision can be a DIFFERENT action.
		// Tokens are content hashes, so unchanged state re-derives the same
		// token; drift must fail as stale-token (client refetches), checked
		// before actionability so the error says "refetch", and before
		// markSupervised so a stale request never burns a concurrency slot.
		if tokenErr := CheckToken(verdict, stateToken); tokenErr != nil {
			return HistoryEntry{}, tokenErr
		}
		if !isActionable(verdict.Decision) {
			return HistoryEntry{}, fmt.Errorf("%w: decision %q", ErrNotActionable, verdict.Decision)
		}
		if !c.markSupervised(verdict) {
			return HistoryEntry{}, fmt.Errorf("%w: partition %s", ErrLegInFlight, verdict.PartitionID)
		}
		// The leg runs detached from the request context: a closed browser tab
		// or client timeout must not orphan supervision of a merge/move that is
		// already running inside ClickHouse. The in-flight marker stays set for
		// the leg's whole window, so re-applies fail with ErrLegInFlight and
		// autonomous dispatch cannot double-run the partition. Validation above
		// stays on the request context on purpose — a disconnect before this
		// point aborts cleanly with nothing started. Only cancellation detaches:
		// the request's span context carries over so the leg's spans land in the
		// operator's API request trace.
		legCtx := trace.ContextWithSpanContext(c.legContext(), trace.SpanContextFromContext(ctx))
		c.wg.Go(func() {
			result := c.executor.Apply(legCtx, client, tableObs, verdict)
			c.unmarkSupervised(verdict)
			if result.Outcome != "success" {
				c.markStalled(verdict, result)
			} else {
				c.clearStalled(nodeID, database, table, partitionID)
			}
			// Republish this node×table immediately so the plan store (and UI)
			// reflect the supervised action without waiting for the next
			// reconcile tick — otherwise the applied partition shows its
			// pre-action state for up to a full interval.
			c.republishTable(legCtx, client, watch)
		})
		return startedEntry(verdict), nil
	}
	return HistoryEntry{}, fmt.Errorf("%w: %s was not observed", ErrPartitionNotFound, partitionID)
}

// startedEntry acknowledges an admitted supervised leg. Outcome "started"
// means the leg was dispatched, not that it converged — completion lands in
// history, and the leg is visible via InFlight until then.
func startedEntry(verdict Verdict) HistoryEntry {
	return HistoryEntry{
		Time:        time.Now().UTC(),
		NodeID:      verdict.NodeID,
		Database:    verdict.Database,
		Table:       verdict.Table,
		Partition:   verdict.Partition,
		PartitionID: verdict.PartitionID,
		Action:      verdict.Decision,
		Bytes:       verdict.BytesOnDisk,
		Outcome:     "started",
	}
}
