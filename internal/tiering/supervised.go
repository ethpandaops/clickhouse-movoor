package tiering

import (
	"context"
	"fmt"
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
	tableObs, err := c.observer.ObserveTable(ctx, client, watch)
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
		result := c.executor.Apply(ctx, client, tableObs, verdict)
		c.unmarkSupervised(verdict)
		if result.Outcome != "success" {
			c.markStalled(verdict, result)
			c.republishTable(ctx, client, watch)
			return result, actionFailedError(result)
		}
		c.clearStalled(nodeID, database, table, partitionID)
		// Republish this node×table immediately so the plan store (and UI)
		// reflect the supervised action without waiting for the next reconcile
		// tick — otherwise the applied partition shows its pre-action state for
		// up to a full interval.
		c.republishTable(ctx, client, watch)
		return result, nil
	}
	return HistoryEntry{}, fmt.Errorf("%w: %s was not observed", ErrPartitionNotFound, partitionID)
}

func actionFailedError(entry HistoryEntry) error {
	if entry.Error != "" {
		return fmt.Errorf("%w: %s", ErrActionFailed, entry.Error)
	}
	if entry.Outcome != "" {
		return fmt.Errorf("%w: outcome %q", ErrActionFailed, entry.Outcome)
	}
	return ErrActionFailed
}
