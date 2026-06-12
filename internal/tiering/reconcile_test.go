package tiering

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

type countingSideMergeObserver struct {
	fakeTableObserver
	calls int
	count uint64
	err   error
}

type recordingForeignObserver struct {
	fakeTableObserver
	bootTimes []time.Time
	since     []time.Time
	move      ForeignMoveObservation
	err       error
}

type cancelingReconcileObserver struct {
	table  TableObservation
	cancel context.CancelFunc
}

func (o *countingSideMergeObserver) CountColdSideMerges(context.Context, chclient.Client, TableObservation, time.Time) (uint64, error) {
	o.calls++
	return o.count, o.err
}

func (o *recordingForeignObserver) ObserveForeignMoves(context.Context, chclient.Client, TableObservation, string, time.Time) (ForeignMoveObservation, error) {
	return o.move, o.err
}

func (o *recordingForeignObserver) ObserveForeignMovesSince(_ context.Context, _ chclient.Client, _ TableObservation, _ string, bootTime time.Time, since time.Time) (ForeignMoveObservation, error) {
	o.bootTimes = append(o.bootTimes, bootTime)
	o.since = append(o.since, since)
	return o.move, o.err
}

func (o cancelingReconcileObserver) ObserveTable(context.Context, chclient.Client, EffectiveWatch) (TableObservation, error) {
	o.cancel()
	return o.table, nil
}

func (o cancelingReconcileObserver) RefreshPartition(context.Context, chclient.Client, TableObservation, string) (PartitionObservation, error) {
	return PartitionObservation{}, errors.New("not used")
}

func TestReconcileBackoffFailureDelayAndReset(t *testing.T) {
	t.Parallel()

	require.Equal(t, DefaultInterval, newReconcileBackoff("node/db/tbl", 0).normal)
	require.Equal(t, time.Duration(0), jitterBackoff(0, "node/db/tbl", 1))

	backoff := newReconcileBackoff("node/db/tbl", 500*time.Millisecond)
	first := backoff.next(false)
	require.GreaterOrEqual(t, first, 800*time.Millisecond)
	require.LessOrEqual(t, first, 1200*time.Millisecond)

	second := backoff.next(false)
	require.GreaterOrEqual(t, second, 1600*time.Millisecond)
	require.LessOrEqual(t, second, 2400*time.Millisecond)

	require.Equal(t, 500*time.Millisecond, backoff.next(true))

	afterReset := backoff.next(false)
	require.GreaterOrEqual(t, afterReset, 800*time.Millisecond)
	require.LessOrEqual(t, afterReset, 1200*time.Millisecond)

	defaultInterval := newReconcileBackoff("node/db/tbl", 5*time.Minute)
	require.LessOrEqual(t, defaultInterval.next(false), 5*time.Minute)

	capped := newReconcileBackoff("node/db/tbl", time.Minute)
	for range 10 {
		_ = capped.next(false)
	}
	require.LessOrEqual(t, capped.next(false), maxObserveFailureBackoff)
}

func TestRepeatedFailureLogState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	c := &controller{}

	shouldLog, suppressed := c.updateFailureLog("observe:node/db/tbl", "boom", now)
	require.True(t, shouldLog)
	require.Zero(t, suppressed)

	shouldLog, suppressed = c.updateFailureLog("observe:node/db/tbl", "boom", now.Add(time.Minute))
	require.False(t, shouldLog)
	require.Zero(t, suppressed)

	shouldLog, suppressed = c.updateFailureLog("observe:node/db/tbl", "boom", now.Add(repeatedFailureLogEvery+time.Second))
	require.True(t, shouldLog)
	require.Equal(t, uint64(1), suppressed)

	shouldLog, suppressed = c.updateFailureLog("observe:node/db/tbl", "different", now.Add(repeatedFailureLogEvery+2*time.Second))
	require.True(t, shouldLog)
	require.Zero(t, suppressed)

	c.clearRepeatedFailure("observe:node/db/tbl")
	shouldLog, suppressed = c.updateFailureLog("observe:node/db/tbl", "different", now.Add(repeatedFailureLogEvery+3*time.Second))
	require.True(t, shouldLog)
	require.Zero(t, suppressed)

	c.warnRepeatedFailure("nil", "ignored", nil)
	logged := 0
	c.warnRepeatedFailure("observe:node/db/tbl", "same", func([]any) { logged++ })
	c.warnRepeatedFailure("observe:node/db/tbl", "same", func([]any) { logged++ })
	require.Equal(t, 1, logged)

	c.failureLogs["observe:node/db/tbl"] = failureLogState{message: "same", lastLogged: now.Add(-repeatedFailureLogEvery), suppressed: 2}
	c.warnRepeatedFailure("observe:node/db/tbl", "same", func(attrs []any) {
		logged++
		require.Contains(t, attrs, slog.Uint64("suppressed_repeats", 2))
	})
	require.Equal(t, 2, logged)
}

func TestReconcileCancellationIsQuiet(t *testing.T) {
	t.Parallel()

	settings := frontierSettings()
	c := &controller{
		log:          slog.New(slog.DiscardHandler),
		store:        NewStore(10),
		observer:     fakeTableObserver{err: context.Canceled},
		instrumenter: noopInstrumenter{},
	}
	ok := c.reconcileOnce(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})
	require.True(t, ok)
	require.Empty(t, c.store.Snapshot().Tables)
	require.Empty(t, c.failureLogs)

	c = &controller{
		log:       slog.New(slog.DiscardHandler),
		cfg:       ControllerConfig{InstanceID: "mine"},
		observer:  fakeSafetyObserver{err: context.Canceled},
		store:     NewStore(10),
		bootTimes: map[string]time.Time{"node-a": time.Now().UTC()},
	}
	require.False(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, TableObservation{Database: "db", Table: "tbl"}))
	require.Equal(t, PauseRunning, c.store.Status().PauseState)
	require.Empty(t, c.failureLogs)

	sideObserver := &countingSideMergeObserver{err: context.Canceled}
	sideTable := TableObservation{Node: chclient.Node{ID: "node-a"}, Database: "db", Table: "tbl", Settings: settings, ObservedAt: time.Now().UTC()}
	key := tableLogKey("node-a", "db", "tbl")
	since := sideTable.ObservedAt.Add(-time.Hour)
	c = &controller{
		log:           slog.New(slog.DiscardHandler),
		observer:      sideObserver,
		instrumenter:  noopInstrumenter{},
		sideMergeLast: map[string]time.Time{key: since},
	}
	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, sideTable)
	require.Equal(t, 1, sideObserver.calls)
	require.Equal(t, since, c.sideMergeLast[key])
	require.Empty(t, c.failureLogs)
}

func TestReconcileKickPaths(t *testing.T) {
	t.Parallel()

	kick := make(chan struct{}, 1)
	kick <- struct{}{}
	require.True(t, waitForNextReconcile(t.Context(), time.Hour, kick))

	c := &controller{}
	first := c.registerKick("node-a", "db", "tbl")
	require.Equal(t, first, c.registerKick("node-a", "db", "tbl"))
	c.kickReconcile("node-a", "db", "tbl")
	require.Len(t, first, 1)
	c.kickReconcile("node-a", "db", "tbl")
	require.Len(t, first, 1)
}

func TestReconcileLoopWaitsReplicaPhaseOffset(t *testing.T) {
	t.Parallel()

	interval := 2 * time.Nanosecond
	node := chclient.Node{ID: "node-a", Shard: "s1", Replica: "r1"}
	for i := range 10 {
		if replicaPhaseOffset(node, interval) > 0 {
			break
		}
		node.ID = "node-" + strconv.Itoa(i)
	}
	require.Greater(t, replicaPhaseOffset(node, interval), time.Duration(0))

	cfg := DefaultConfig()
	cfg.Mode = ModeEnforce
	cfg.Interval = Duration{Duration: interval}
	settings := frontierSettings()
	settings.Mode = ModeEnforce
	table := frontierObservation(time.Now().UTC())
	table.Node = node

	ctx, cancel := context.WithCancel(t.Context())
	c := &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: cfg},
		store:    NewStore(10),
		observer: cancelingReconcileObserver{table: table, cancel: cancel},
	}
	c.reconcileLoop(ctx, chclient.Client{Node: node}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})
}

func TestRecordColdSideMergesSkipsUnreadablePartLogAndPreservesCheckpoint(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	settings := frontierSettings()
	table := TableObservation{
		Node:       chclient.Node{ID: "node-a"},
		Database:   "db",
		Table:      "tbl",
		Settings:   settings,
		ObservedAt: now,
		Conditions: []Condition{NewCondition(ConditionSeverityWarning, "part_log_unreadable", "missing", "node-a", "db", "tbl", "", "")},
	}
	key := tableLogKey("node-a", "db", "tbl")
	observer := &countingSideMergeObserver{}
	instrumenter := &countingInstrumenter{}
	c := &controller{
		log:           slog.New(slog.DiscardHandler),
		observer:      observer,
		instrumenter:  instrumenter,
		sideMergeLast: map[string]time.Time{key: now.Add(-time.Hour)},
	}

	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, table)
	require.Zero(t, observer.calls)

	table.Conditions = nil
	table.Settings.OptimizeOn = OptimizeOnCold
	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, table)
	require.Zero(t, observer.calls)

	table.Settings.OptimizeOn = OptimizeOnHot
	observer.err = errors.New("part_log unavailable")
	since := now.Add(-time.Hour)
	c.sideMergeLast[key] = since
	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, table)
	require.Equal(t, 1, observer.calls)
	require.Equal(t, since, c.sideMergeLast[key])
	require.Contains(t, c.failureLogs, "side_merge:"+key)

	observer.err = nil
	observer.count = 3
	table.ObservedAt = now.Add(time.Minute)
	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, table)
	require.Equal(t, 2, observer.calls)
	require.Equal(t, table.ObservedAt, c.sideMergeLast[key])
	require.Equal(t, uint64(3), instrumenter.sideMerges)
	require.NotContains(t, c.failureLogs, "side_merge:"+key)
}

func TestForeignGuardAdvancesHistoricalScanCursor(t *testing.T) {
	boot := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	observer := &recordingForeignObserver{move: ForeignMoveObservation{ForeignActivity: true, Message: "manual"}}
	c := &controller{
		log:       slog.New(slog.DiscardHandler),
		cfg:       ControllerConfig{InstanceID: "mine"},
		observer:  observer,
		store:     NewStore(10),
		bootTimes: map[string]time.Time{"node-a": boot},
	}
	table := TableObservation{Database: "db", Table: "tbl"}
	key := tableLogKey("node-a", "db", "tbl")

	require.False(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, table))
	require.Len(t, observer.since, 1)
	require.Equal(t, boot, observer.bootTimes[0])
	require.Equal(t, boot, observer.since[0])
	require.True(t, c.foreignGuardSeen[key].After(boot))

	observer.move = ForeignMoveObservation{}
	c.store.Resume()
	cursorBeforeSecondScan := c.foreignGuardSeen[key]
	require.True(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "node-a"}}, table))
	require.Len(t, observer.since, 2)
	require.Equal(t, boot, observer.bootTimes[1])
	require.Equal(t, cursorBeforeSecondScan, observer.since[1])
	require.True(t, c.foreignGuardSeen[key].After(cursorBeforeSecondScan))
}
