package tiering

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

func (c *controller) dispatch(ctx context.Context, client chclient.Client, table TableObservation, verdicts []Verdict) {
	if table.EffectiveMode != ModeEnforce {
		return
	}
	if !c.guardForeignMovers(ctx, client, table) {
		return
	}
	if c.store.Status().PauseState != PauseRunning {
		return
	}
	actionable := actionableVerdicts(verdicts)
	if len(actionable) == 0 {
		return
	}
	orderOldestFirst(table, actionable)
	if c.breakerTripped(len(actionable), len(verdicts)) {
		c.log.WarnContext(ctx, "tiering diff breaker tripped", slog.String("node_id", client.Node.ID), slog.String("database", table.Database), slog.String("table", table.Table), slog.Int("actionable", len(actionable)))
		return
	}
	moves := 0
	for _, verdict := range actionable {
		if moves >= c.cfg.Tiering.Safety.MaxMovesPerCycle {
			return
		}
		if !c.tryStart(verdict) {
			continue
		}
		moves++
		c.wg.Go(func() {
			entry := c.executor.Apply(ctx, client, table, verdict)
			c.finish(verdict, entry)
			if entry.Outcome == "success" {
				c.kickReconcile(verdict.NodeID, verdict.Database, verdict.Table)
			}
		})
	}
}

func (c *controller) tryStart(verdict Verdict) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	// The partition cap is per node (as plan/design.md specifies): nodes own
	// independent disks, so one node's hour-long move must not starve the
	// other nodes' dispatch. maxBytesInFlight below stays the aggregate
	// ceiling across all nodes.
	nodeInFlight := 0
	var nodeBytesInFlight uint64
	for _, leg := range c.inFlight {
		if leg.NodeID == verdict.NodeID {
			nodeInFlight++
			nodeBytesInFlight += leg.Bytes
		}
	}
	if nodeInFlight >= c.cfg.Tiering.MaxConcurrentPartitions {
		return false
	}
	if c.bytesInFlight+verdict.BytesOnDisk > c.cfg.Tiering.Safety.MaxBytesInFlight.Value {
		return false
	}
	status := c.store.Status()
	c.rollDailyBudgetLocked(verdict.NodeID, time.Now())
	movedToday := c.bytesMovedTodayForNode(verdict.NodeID)
	// The daily budget is per node, like movedToday itself: only THIS node's
	// in-flight bytes count against its headroom — node B's transfers must
	// not block node A's admission. (maxBytesInFlight above remains the
	// aggregate cross-node ceiling.)
	if movedToday+nodeBytesInFlight+verdict.BytesOnDisk > c.cfg.Tiering.Safety.MaxBytesPerDay.Value {
		return false
	}
	key := flightKey(verdict)
	if _, exists := c.inFlight[key]; exists {
		return false
	}
	c.inFlight[key] = newInFlightLeg(verdict, "dispatch")
	c.bytesInFlight += verdict.BytesOnDisk
	status.BytesInFlight = c.bytesInFlight
	c.store.SetStatus(status)
	return true
}

func newInFlightLeg(verdict Verdict, source string) InFlightLeg {
	return InFlightLeg{
		NodeID:      verdict.NodeID,
		Database:    verdict.Database,
		Table:       verdict.Table,
		Partition:   verdict.Partition,
		PartitionID: verdict.PartitionID,
		Action:      verdict.Decision,
		Bytes:       verdict.BytesOnDisk,
		StartedAt:   time.Now().UTC(),
		Source:      source,
	}
}

// InFlight snapshots the currently-executing legs, oldest first.
func (c *controller) InFlight() []InFlightLeg {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]InFlightLeg, 0, len(c.inFlight))
	for _, leg := range c.inFlight {
		out = append(out, leg)
	}
	slices.SortFunc(out, func(a InFlightLeg, b InFlightLeg) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.PartitionID, b.PartitionID))
	})
	return out
}

// markSupervised registers a human-triggered leg in the single-flight map so
// it is visible as in-flight AND so autonomous dispatch cannot double-run the
// same partition while the apply executes. It takes no budget bookkeeping —
// the human is the budget.
func (c *controller) markSupervised(verdict Verdict) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := flightKey(verdict)
	if _, exists := c.inFlight[key]; exists {
		return false
	}
	c.inFlight[key] = newInFlightLeg(verdict, "supervised")
	return true
}

func (c *controller) unmarkSupervised(verdict Verdict) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inFlight, flightKey(verdict))
}

func (c *controller) finish(verdict Verdict, entry HistoryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inFlight, flightKey(verdict))
	if c.bytesInFlight >= verdict.BytesOnDisk {
		c.bytesInFlight -= verdict.BytesOnDisk
	} else {
		c.bytesInFlight = 0
	}
	status := c.store.Status()
	status.BytesInFlight = c.bytesInFlight
	if entry.Outcome == "success" {
		c.finishSuccessLocked(verdict, &status)
	}
	if entry.Outcome == "success" && c.cfg.Tiering.Safety.PauseAfterActions > 0 {
		c.cfg.Tiering.Safety.PauseAfterActions--
		if c.cfg.Tiering.Safety.PauseAfterActions == 0 {
			status.PauseState = PauseStopped
			status.PauseReason = PauseReasonTrainingWheels
		}
	}
	if entry.Outcome != "success" {
		c.markStalledLocked(verdict, entry)
	}
	c.store.SetStatus(status)
}

func (c *controller) finishSuccessLocked(verdict Verdict, status *StatusSnapshot) {
	if c.stalled != nil {
		delete(c.stalled, flightKey(verdict))
	}
	// Append and consolidate both mark the start of a split repair; repeated
	// repairs of the same partition widen its adaptive resplit quiet window.
	if verdict.Decision == DecisionAppend || verdict.Decision == DecisionConsolidate {
		if c.resplitFlaps == nil {
			c.resplitFlaps = make(map[string]int)
		}
		c.resplitFlaps[flightKey(verdict)]++
	}
	if c.bytesMovedToday == nil {
		c.bytesMovedToday = make(map[string]uint64)
	}
	c.rollDailyBudgetLocked(verdict.NodeID, time.Now())
	// The optimize leg merges in place. It is not a MOVE leg, so it never
	// charges the daily movement budget (consistent with the part_log
	// MovePart boot seed, which it would not appear in either).
	if verdict.Decision != DecisionOptimize {
		c.bytesMovedToday[verdict.NodeID] += verdict.BytesOnDisk
	}
	status.BytesMovedToday = c.totalBytesMovedTodayLocked()
}

// rollDailyBudgetLocked resets a node's per-day moved-byte tally when the
// calendar day changes, so maxBytesPerDay is a true daily budget rather than a
// lifetime one. The boot seed (system.part_log MovePart events for today)
// remains the primary source until the first rollover. Caller must hold c.mu.
func (c *controller) rollDailyBudgetLocked(nodeID string, now time.Time) {
	day := now.UTC().Format("2006-01-02")
	if c.budgetDay == nil {
		c.budgetDay = make(map[string]string)
	}
	if c.budgetDay[nodeID] == day {
		return
	}
	c.budgetDay[nodeID] = day
	if c.bytesMovedToday != nil {
		c.bytesMovedToday[nodeID] = 0
	}
}

func (c *controller) bytesMovedTodayForNode(nodeID string) uint64 {
	if c.bytesMovedToday == nil {
		return 0
	}
	return c.bytesMovedToday[nodeID]
}

func (c *controller) totalBytesMovedTodayLocked() uint64 {
	var total uint64
	for _, bytes := range c.bytesMovedToday {
		total += bytes
	}
	return total
}

func flightKey(verdict Verdict) string {
	return verdict.NodeID + "/" + verdict.Database + "/" + verdict.Table + "/" + verdict.PartitionID
}
