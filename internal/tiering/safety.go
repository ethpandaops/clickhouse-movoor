package tiering

import (
	"context"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

func (c *controller) seedNode(ctx context.Context, client chclient.Client) {
	c.mu.Lock()
	if c.bootTimes == nil {
		c.bootTimes = make(map[string]time.Time)
	}
	c.bootTimes[client.Node.ID] = time.Now().UTC()
	c.mu.Unlock()
	if client.DB == nil {
		return
	}

	if bootObserver, ok := c.observer.(interface {
		CaptureBootTime(context.Context, chclient.Client) (time.Time, error)
	}); ok {
		boot, err := bootObserver.CaptureBootTime(ctx, client)
		if err != nil {
			c.log.WarnContext(ctx, "failed to capture ClickHouse boot reference", slog.String("node_id", client.Node.ID), slog.Any("error", err))
		} else {
			c.mu.Lock()
			c.bootTimes[client.Node.ID] = boot
			c.mu.Unlock()
		}
	}

	seeder, ok := c.observer.(interface {
		SeedMovedBytesToday(context.Context, chclient.Client, []EffectiveWatch) (uint64, error)
	})
	if !ok {
		return
	}
	bytes, err := seeder.SeedMovedBytesToday(ctx, client, c.cfg.Watches)
	if err != nil {
		c.log.WarnContext(ctx, "failed to seed daily moved-byte budget", slog.String("node_id", client.Node.ID), slog.Any("error", err))
		return
	}
	c.mu.Lock()
	if c.bytesMovedToday == nil {
		c.bytesMovedToday = make(map[string]uint64)
	}
	if c.budgetDay == nil {
		c.budgetDay = make(map[string]string)
	}
	c.bytesMovedToday[client.Node.ID] = bytes
	c.budgetDay[client.Node.ID] = time.Now().UTC().Format("2006-01-02")
	status := c.store.Status()
	status.BytesMovedToday = c.totalBytesMovedTodayLocked()
	c.store.SetStatus(status)
	c.mu.Unlock()
}

func (c *controller) guardForeignMovers(ctx context.Context, client chclient.Client, table TableObservation) bool {
	observer, ok := c.observer.(interface {
		ObserveForeignMoves(context.Context, chclient.Client, TableObservation, string, time.Time) (ForeignMoveObservation, error)
	})
	if !ok {
		return true
	}
	table.InFlightPartNames = c.inFlightPartNames(table)
	key := tableLogKey(client.Node.ID, table.Database, table.Table)
	c.mu.Lock()
	bootTime := c.bootTimes[client.Node.ID]
	since := c.foreignGuardSeen[key]
	c.mu.Unlock()
	if bootTime.IsZero() {
		bootTime = time.Now().UTC()
	}
	if since.IsZero() || since.Before(bootTime) {
		since = bootTime
	}
	scanStartedAt := time.Now().UTC()
	var observation ForeignMoveObservation
	var err error
	if observerSince, hasObserveSince := c.observer.(interface {
		ObserveForeignMovesSince(context.Context, chclient.Client, TableObservation, string, time.Time, time.Time) (ForeignMoveObservation, error)
	}); hasObserveSince {
		observation, err = observerSince.ObserveForeignMovesSince(ctx, client, table, c.instanceID(), bootTime, since)
	} else {
		observation, err = observer.ObserveForeignMoves(ctx, client, table, c.instanceID(), since)
	}
	if err != nil {
		if isContextCanceled(ctx, err) {
			return false
		}
		c.log.WarnContext(ctx, "foreign-mover guard failed", slog.String("node_id", client.Node.ID), slog.String("database", table.Database), slog.String("table", table.Table), slog.Any("error", err))
		c.store.Pause(PauseReasonForeignMover)
		return false
	}
	c.markForeignGuardScanned(key, scanStartedAt)
	if observation.DuplicateInstance {
		c.store.Pause(PauseReasonDuplicateInstance)
		c.warnRepeatedFailure("foreign_guard:"+key, "duplicate:"+observation.Message, func(attrs []any) {
			attrs = append(attrs, slog.String("node_id", client.Node.ID), slog.String("database", table.Database), slog.String("table", table.Table), slog.String("message", observation.Message))
			c.log.WarnContext(ctx, "tiering paused because another movoor instance is moving parts", attrs...)
		})
		return false
	}
	if observation.ForeignActivity {
		c.store.Pause(PauseReasonForeignMover)
		c.warnRepeatedFailure("foreign_guard:"+key, "foreign:"+observation.Message, func(attrs []any) {
			attrs = append(attrs, slog.String("node_id", client.Node.ID), slog.String("database", table.Database), slog.String("table", table.Table), slog.String("message", observation.Message))
			c.log.WarnContext(ctx, "tiering paused because foreign move activity is visible", attrs...)
		})
		return false
	}
	c.clearRepeatedFailure("foreign_guard:" + key)
	status := c.store.Status()
	if status.PauseReason == PauseReasonForeignMover {
		c.mu.Lock()
		if c.foreignClean == nil {
			c.foreignClean = make(map[string]int)
		}
		c.foreignClean[key]++
		cleanTicks := c.foreignClean[key]
		c.mu.Unlock()
		if cleanTicks >= 2 {
			c.store.Resume()
		}
	} else {
		c.mu.Lock()
		if c.foreignClean != nil {
			c.foreignClean[key] = 0
		}
		c.mu.Unlock()
	}
	return true
}

func (c *controller) markForeignGuardScanned(key string, scanStartedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.foreignGuardSeen == nil {
		c.foreignGuardSeen = make(map[string]time.Time)
	}
	if c.foreignGuardSeen[key].Before(scanStartedAt) {
		c.foreignGuardSeen[key] = scanStartedAt
	}
}

func (c *controller) maybeProbeColdPartition(ctx context.Context, client chclient.Client, table TableObservation) []Condition {
	prober, ok := c.observer.(interface {
		ProbeColdPartition(context.Context, chclient.Client, TableObservation, PartitionObservation) error
	})
	if !ok {
		return nil
	}
	key := client.Node.ID + "/" + table.Database + "/" + table.Table
	c.mu.Lock()
	if c.probeLast == nil {
		c.probeLast = make(map[string]time.Time)
	}
	last := c.probeLast[key]
	if !last.IsZero() && time.Since(last) < 24*time.Hour {
		c.mu.Unlock()
		return nil
	}
	c.probeLast[key] = time.Now().UTC()
	c.mu.Unlock()

	candidates := make([]PartitionObservation, 0, len(table.Partitions))
	for _, partition := range table.Partitions {
		if !hasDisk(partition.Disks, table.Settings.TargetDisk) {
			continue
		}
		candidates = append(candidates, partition)
	}
	if len(candidates) == 0 {
		return nil
	}
	partition := candidates[probeCandidateIndex(key, time.Now().UTC(), len(candidates))]
	if err := prober.ProbeColdPartition(ctx, client, table, partition); err != nil {
		if c.instrumenter != nil {
			c.instrumenter.RecordProbeFailure(ctx, client.Node.ID, table.Database, table.Table)
		}
		return []Condition{NewCondition(ConditionSeverityWarning, "cold_read_probe_failed", err.Error(), client.Node.ID, table.Database, table.Table, partition.Partition, partition.PartitionID)}
	}
	return nil
}

func probeCandidateIndex(key string, now time.Time, candidates int) int {
	if candidates <= 1 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key + "/" + now.UTC().Format("2006-01-02")))
	//nolint:gosec // modulo by positive int bounds the value to candidates-1.
	return int(h.Sum64() % uint64(candidates))
}

func (c *controller) inFlightPartNames(table TableObservation) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inFlight) == 0 {
		return nil
	}
	names := make([]string, 0)
	for _, partition := range table.Partitions {
		key := table.Node.ID + "/" + table.Database + "/" + table.Table + "/" + partition.PartitionID
		if _, ok := c.inFlight[key]; !ok {
			continue
		}
		for _, part := range partition.Hashes {
			names = append(names, part.Name)
		}
	}
	return names
}

func (c *controller) recordColdSideMerges(ctx context.Context, client chclient.Client, table TableObservation) {
	// Tables that optimize on the cold side merge there by policy — counting
	// their own intentional merges as incidents would make the alarm useless.
	// part_log merge events carry no query identity, so per-table exemption is
	// the only attribution available.
	if table.Settings.OptimizeOn == OptimizeOnCold {
		return
	}
	if conditionPresent(table.Conditions, "part_log_unreadable") {
		return
	}
	observer, ok := c.observer.(interface {
		CountColdSideMerges(context.Context, chclient.Client, TableObservation, time.Time) (uint64, error)
	})
	if !ok || c.instrumenter == nil {
		return
	}
	key := tableLogKey(client.Node.ID, table.Database, table.Table)
	c.mu.Lock()
	if c.sideMergeLast == nil {
		c.sideMergeLast = make(map[string]time.Time)
	}
	since := c.sideMergeLast[key]
	c.mu.Unlock()
	if since.IsZero() {
		c.mu.Lock()
		c.sideMergeLast[key] = table.ObservedAt
		c.mu.Unlock()
		return
	}
	count, err := observer.CountColdSideMerges(ctx, client, table, since)
	if err != nil {
		if isContextCanceled(ctx, err) {
			return
		}
		c.warnRepeatedFailure("side_merge:"+key, err.Error(), func(attrs []any) {
			attrs = append(attrs, slog.String("node_id", client.Node.ID), slog.String("database", table.Database), slog.String("table", table.Table), slog.Any("error", err))
			c.log.WarnContext(ctx, "failed to count cold side merges", attrs...)
		})
		return
	}
	c.clearRepeatedFailure("side_merge:" + key)
	c.mu.Lock()
	c.sideMergeLast[key] = table.ObservedAt
	c.mu.Unlock()
	c.instrumenter.RecordSideMerge(ctx, client.Node.ID, table.Database, table.Table, count)
}

func (c *controller) breakerTripped(actionable int, total int) bool {
	limit := c.cfg.Tiering.Safety.DiffBreaker
	if override := limit.Override; override != nil && override.Expires.After(time.Now()) {
		limit.MaxPartitions = override.MaxPartitions
		limit.MaxTableFraction = override.MaxTableFraction
	}
	if actionable > limit.MaxPartitions {
		return true
	}
	if total > 0 && float64(actionable)/float64(total) > limit.MaxTableFraction {
		return true
	}
	return false
}
