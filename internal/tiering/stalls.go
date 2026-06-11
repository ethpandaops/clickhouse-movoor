package tiering

import (
	"fmt"
	"time"
)

type stalledPartition struct {
	Until        time.Time
	Failures     int
	Reason       string
	Token        string
	TransitionAt time.Time
}

const (
	initialStallBackoff = 6 * time.Hour
	maxStallBackoff     = 72 * time.Hour
	maxResplitQuiet     = 90 * 24 * time.Hour
)

func (c *controller) markStalledLocked(verdict Verdict, entry HistoryEntry) {
	if c.stalled == nil {
		c.stalled = make(map[string]stalledPartition)
	}
	key := flightKey(verdict)
	previous := c.stalled[key]
	failures := previous.Failures + 1
	delay := stallBackoff(failures)
	reason := entry.Error
	if reason == "" {
		reason = "action failed"
	}
	c.stalled[key] = stalledPartition{
		Until:        time.Now().UTC().Add(delay),
		Failures:     failures,
		Reason:       reason,
		Token:        verdict.Token,
		TransitionAt: time.Now().UTC(),
	}
}

func (c *controller) markStalled(verdict Verdict, entry HistoryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.markStalledLocked(verdict, entry)
}

func stallBackoff(failures int) time.Duration {
	if failures <= 1 {
		return initialStallBackoff
	}
	delay := initialStallBackoff
	for i := 1; i < failures; i++ {
		if delay >= maxStallBackoff/2 {
			return maxStallBackoff
		}
		delay *= 2
	}
	return delay
}

func (c *controller) overlayStalled(verdicts []Verdict, now time.Time) []Verdict {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range verdicts {
		key := flightKey(verdicts[i])
		stalled, ok := c.stalled[key]
		if !ok {
			continue
		}
		if stalled.Token != verdicts[i].Token {
			delete(c.stalled, key)
			continue
		}
		if !now.Before(stalled.Until) {
			continue
		}
		verdicts[i].Status = StatusStalled
		verdicts[i].Decision = DecisionHold
		verdicts[i].Reason = "action stalled: " + stalled.Reason
		retryAt := stalled.Until
		verdicts[i].Hold = &HoldDetail{Gate: "stalled", RetryAt: &retryAt, Failures: stalled.Failures}
		verdicts[i].Conditions = append(verdicts[i].Conditions, NewCondition(
			ConditionSeverityWarning,
			"action_stalled",
			fmt.Sprintf("retry after %s: %s", stalled.Until.Format(time.RFC3339), stalled.Reason),
			verdicts[i].NodeID,
			verdicts[i].Database,
			verdicts[i].Table,
			verdicts[i].Partition,
			verdicts[i].PartitionID,
		))
	}
	return verdicts
}

func (c *controller) applyAdaptiveResplitQuiet(obs *TableObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, partition := range obs.Partitions {
		if observedStatus(obs.Settings.TargetDisk, partition) != StatusSplit {
			continue
		}
		key := obs.Node.ID + "/" + obs.Database + "/" + obs.Table + "/" + partition.PartitionID
		flaps := c.resplitFlaps[key]
		if flaps <= 0 {
			continue
		}
		if obs.ResplitQuiet == nil {
			obs.ResplitQuiet = make(map[string]time.Duration)
		}
		obs.ResplitQuiet[partition.PartitionID] = adaptiveResplitQuiet(obs.Settings.Resplit.QuietFor.Duration, flaps)
	}
}

func adaptiveResplitQuiet(base time.Duration, flaps int) time.Duration {
	if flaps <= 0 || base <= 0 {
		return base
	}
	out := base
	for range flaps {
		if out >= maxResplitQuiet/2 {
			return maxResplitQuiet
		}
		out *= 2
	}
	return out
}

func (c *controller) clearStalled(nodeID string, database string, table string, partitionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stalled == nil {
		return
	}
	delete(c.stalled, nodeID+"/"+database+"/"+table+"/"+partitionID)
}
