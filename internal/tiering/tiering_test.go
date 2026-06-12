//nolint:modernize // Pointer helpers keep controller fixtures readable.
package tiering

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

type fakeTableObserver struct {
	table TableObservation
	err   error
}

type fakeActuator struct {
	calls chan Verdict
	entry HistoryEntry
}

type countingInstrumenter struct {
	noopInstrumenter
	sideMerges uint64
}

func (f fakeActuator) Apply(_ context.Context, _ chclient.Client, _ TableObservation, verdict Verdict) HistoryEntry {
	if f.calls != nil {
		f.calls <- verdict
	}
	if f.entry.Outcome != "" {
		return f.entry
	}
	return HistoryEntry{Outcome: "success"}
}

func (c *countingInstrumenter) RecordSideMerge(_ context.Context, _ string, _ string, _ string, count uint64) {
	c.sideMerges += count
}

func (f fakeTableObserver) ObserveTable(context.Context, chclient.Client, EffectiveWatch) (TableObservation, error) {
	return f.table, f.err
}

func (f fakeTableObserver) RefreshPartition(context.Context, chclient.Client, TableObservation, string) (PartitionObservation, error) {
	if len(f.table.Partitions) == 0 {
		return PartitionObservation{}, sql.ErrNoRows
	}
	return withTestHashes(f.table.Partitions[0]), nil
}

func TestControllerBasicsAndReconcile(t *testing.T) {
	cfg := ControllerConfig{Tiering: DefaultConfig(), Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: ptrSettings(frontierSettings())}}, QueryTimeout: time.Second}
	ctrl := New(slog.New(slog.DiscardHandler), []chclient.Client{{Node: chclient.Node{ID: "n1"}}}, cfg)
	c, ok := ctrl.(*controller)
	require.True(t, ok)
	require.NotNil(t, c.Store())
	require.Equal(t, PauseReasonOperator, c.Pause(PauseReasonOperator).PauseReason)
	require.Equal(t, PauseRunning, c.Resume().PauseState)
	client, ok := c.client("n1")
	require.True(t, ok)
	require.Equal(t, "n1", client.Node.ID)
	_, ok = c.client("missing")
	require.False(t, ok)
	watch, ok := c.watch("db", "tbl")
	require.True(t, ok)
	require.Equal(t, "tbl", watch.Table)
	_, ok = c.watch("db", "missing")
	require.False(t, ok)
	require.Len(t, actionableVerdicts([]Verdict{{Decision: DecisionTier}, {Decision: DecisionKeep}}), 1)
	require.True(t, isActionable(DecisionAppend))
	require.False(t, isActionable(DecisionKeep))

	unpaused := &controller{store: NewStore(1)}
	unpaused.store.SetStatus(StatusSnapshot{PauseState: PauseStopped})
	require.ErrorContains(t, unpaused.ensureNotPaused(), string(PauseReasonOperator))

	obs := frontierObservation(time.Now().UTC())
	obs.Node = chclient.Node{ID: "n1"}
	obs.Database = "db"
	obs.Table = "tbl"
	c.observer = fakeTableObserver{table: obs}
	c.reconcileOnce(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, cfg.Watches[0])
	require.NotEmpty(t, c.store.Snapshot().Tables[0].Verdicts)

	c.observer = fakeTableObserver{err: errors.New("observe failed")}
	c.reconcileOnce(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, cfg.Watches[0])
	require.Equal(t, "observe failed", c.store.Snapshot().Tables[0].LastError)
}

func TestControllerStartStopAndErrors(t *testing.T) {
	offCtrl := New(slog.New(slog.DiscardHandler), []chclient.Client{{Node: chclient.Node{ID: "n1"}}}, ControllerConfig{Tiering: Config{Mode: ModeOff}})
	off, ok := offCtrl.(*controller)
	require.True(t, ok)
	require.NoError(t, off.Start(t.Context()))
	require.NoError(t, off.Stop(t.Context()))

	cfg := DefaultConfig()
	cfg.Interval = Duration{Duration: time.Millisecond}
	noClientsCtrl := New(slog.New(slog.DiscardHandler), nil, ControllerConfig{Tiering: cfg})
	noClients, ok := noClientsCtrl.(*controller)
	require.True(t, ok)
	require.ErrorContains(t, noClients.Start(t.Context()), "at least one")

	settings := frontierSettings()
	obs := frontierObservation(time.Now().UTC())
	obs.Node = chclient.Node{ID: "n1"}
	c := &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: cfg, Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: &settings}}},
		clients:  []chclient.Client{{Node: chclient.Node{ID: "n1"}}},
		observer: fakeTableObserver{table: obs},
		store:    NewStore(10),
		inFlight: make(map[string]InFlightLeg),
	}
	c.executor = NewExecutor(slog.New(slog.DiscardHandler), c.store, fakeTableObserver{table: obs}, "instance")
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, c.Start(ctx))
	time.Sleep(3 * time.Millisecond)
	cancel()
	require.NoError(t, c.Stop(t.Context()))

	ctxDone, cancelDone := context.WithCancel(t.Context())
	cancelDone()
	require.Error(t, c.Stop(ctxDone))
}

func TestControllerEnforcePhaseOffset(t *testing.T) {
	cfg := DefaultConfig()
	c := &controller{cfg: ControllerConfig{Tiering: cfg}}
	settings := frontierSettings()
	require.False(t, c.usesEnforceMode(EffectiveWatch{Settings: &settings}))
	cfg.Mode = ModeEnforce
	c.cfg.Tiering = cfg
	require.True(t, c.usesEnforceMode(EffectiveWatch{Settings: &settings}))
	settings.Mode = ModePlan
	require.False(t, c.usesEnforceMode(EffectiveWatch{Settings: &settings}))
	settings.Mode = ModeEnforce
	require.True(t, c.usesEnforceMode(EffectiveWatch{Settings: &settings}))

	node := chclient.Node{ID: "node-a", Shard: "1", Replica: "2"}
	offset := replicaPhaseOffset(node, time.Minute)
	require.GreaterOrEqual(t, offset, time.Duration(0))
	require.Less(t, offset, time.Minute)
	require.Equal(t, offset, replicaPhaseOffset(node, time.Minute))
	require.Zero(t, replicaPhaseOffset(node, 0))
}

// cases share one controller fixture; splitting them would duplicate it.
//
//nolint:funlen // The async apply/retry happy paths and the validation error
func TestControllerApply(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid' TO DISK 's3_cache'")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid' TO DISK 's3_cache'")).WillReturnResult(sqlmock.NewResult(0, 0))

	settings := frontierSettings()
	settings.SkipOptimize = true
	settings.Mode = ModePlan
	settings.QuietFor = Duration{Duration: time.Minute}
	now := time.Now().UTC()
	table := frontierObservation(now)
	table.Database = "db"
	table.Table = "tbl"
	table.Node = chclient.Node{ID: "n1"}
	table.Settings = settings
	table.Partitions = []PartitionObservation{{Partition: "('mainnet',2)", PartitionID: "pid", GroupKey: "mainnet", AgeInteger: 2, ActiveParts: 1, MaxModificationTime: now.Add(-time.Hour), Disks: []DiskPart{{Disk: "default", Parts: 1}}}}
	table.Heads = map[string]int64{"mainnet": 500}
	table.PartLogMinTime = ptrTime(now.Add(-time.Hour))
	verdict := DecidePartition(table, table.Partitions[0], now)
	store := NewStore(10)
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "tbl", Verdicts: []Verdict{verdict}})
	c := &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: DefaultConfig(), Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: &settings}}},
		clients:  []chclient.Client{{Node: chclient.Node{ID: "n1"}, DB: db}},
		observer: fakeTableObserver{table: table},
		store:    store,
		inFlight: make(map[string]InFlightLeg),
	}
	targetTable := table
	targetTable.Partitions = append([]PartitionObservation(nil), table.Partitions...)
	targetTable.Partitions[0].Disks = []DiskPart{{Disk: "s3_cache", Parts: 1}}
	exec := NewExecutor(slog.New(slog.DiscardHandler), store, fakeTableObserver{table: targetTable}, "instance")
	exec.PollInterval = time.Millisecond
	c.executor = exec

	entry, err := c.Apply(t.Context(), "n1", "db", "tbl", "pid", verdict.Token)
	require.NoError(t, err)
	require.Equal(t, "started", entry.Outcome)
	c.wg.Wait()
	require.Empty(t, c.InFlight())
	history := store.History()
	require.NotEmpty(t, history)
	require.Equal(t, "success", history[len(history)-1].Outcome)
	c.stalled = map[string]stalledPartition{flightKey(verdict): {Token: verdict.Token, Until: time.Now().Add(time.Hour), Reason: "failed"}}
	entry, err = c.Retry(t.Context(), "n1", "db", "tbl", "pid", verdict.Token)
	require.NoError(t, err)
	require.Equal(t, "started", entry.Outcome)
	c.wg.Wait()
	require.Empty(t, c.stalled)
	_, err = c.Retry(t.Context(), "n1", "db", "tbl", "pid", "bad")
	require.ErrorContains(t, err, "token")
	require.NoError(t, mock.ExpectationsWereMet())
	_, err = c.Retry(t.Context(), "missing", "db", "tbl", "pid", verdict.Token)
	require.ErrorContains(t, err, "current plan")
	_, err = c.Apply(t.Context(), "missing", "db", "tbl", "pid", verdict.Token)
	require.ErrorContains(t, err, "node")
	_, err = c.Apply(t.Context(), "n1", "db", "missing", "pid", verdict.Token)
	require.ErrorContains(t, err, "watch")
	_, err = c.Apply(t.Context(), "n1", "db", "tbl", "missing", verdict.Token)
	require.ErrorContains(t, err, "current plan")
	_, err = c.Apply(t.Context(), "n1", "db", "tbl", "pid", "bad")
	require.ErrorContains(t, err, "token")
}

func TestControllerApplyReturnsExecutorFailure(t *testing.T) {
	settings := frontierSettings()
	settings.SkipOptimize = true
	settings.Mode = ModePlan
	settings.QuietFor = Duration{Duration: time.Minute}
	now := time.Now().UTC()
	table := frontierObservation(now)
	table.Database = "db"
	table.Table = "tbl"
	table.Node = chclient.Node{ID: "n1"}
	table.Settings = settings
	table.Partitions = []PartitionObservation{{
		Partition:           "('mainnet',2)",
		PartitionID:         "pid",
		GroupKey:            "mainnet",
		AgeInteger:          2,
		ActiveParts:         1,
		MaxModificationTime: now.Add(-time.Hour),
		Disks:               []DiskPart{{Disk: "default", Parts: 1}},
	}}
	table.Heads = map[string]int64{"mainnet": 500}
	table.PartLogMinTime = ptrTime(now.Add(-time.Hour))
	verdict := DecidePartition(table, table.Partitions[0], now)
	store := NewStore(10)
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "tbl", Verdicts: []Verdict{verdict}})
	c := &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: DefaultConfig(), Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: &settings}}},
		clients:  []chclient.Client{{Node: chclient.Node{ID: "n1"}}},
		observer: fakeTableObserver{table: table},
		store:    store,
		inFlight: make(map[string]InFlightLeg),
		executor: fakeActuator{entry: HistoryEntry{Outcome: "error", Error: "move failed"}},
	}

	// The apply is acknowledged before the executor runs; the failure surfaces
	// asynchronously as a stall mark plus the republished hold verdict.
	entry, err := c.Apply(t.Context(), "n1", "db", "tbl", "pid", verdict.Token)
	require.NoError(t, err)
	require.Equal(t, "started", entry.Outcome)
	c.wg.Wait()
	require.Empty(t, c.InFlight())
	require.Contains(t, c.stalled, flightKey(verdict))
	require.Equal(t, "move failed", c.stalled[flightKey(verdict)].Reason)

	snapshot := c.store.Snapshot()
	require.Len(t, snapshot.Tables, 1)
	require.Len(t, snapshot.Tables[0].Verdicts, 1)
	require.Equal(t, StatusStalled, snapshot.Tables[0].Verdicts[0].Status)
	require.Equal(t, DecisionHold, snapshot.Tables[0].Verdicts[0].Decision)
}

func TestReconcileLoopPublishesBeforePhaseOffset(t *testing.T) {
	t.Parallel()

	settings := frontierSettings()
	settings.Mode = ModeEnforce
	now := time.Now().UTC()
	table := frontierObservation(now)
	table.Database = "db"
	table.Table = "tbl"
	watch := EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings}

	cfg := DefaultConfig()
	cfg.Mode = ModeEnforce
	cfg.Interval = Duration{Duration: time.Hour}
	node := chclient.Node{ID: "n1", Shard: "1", Replica: "2"}
	// The warm-up path only exists for nodes with a non-zero phase offset.
	require.Positive(t, replicaPhaseOffset(node, time.Hour))

	c := &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: cfg, Watches: []EffectiveWatch{watch}},
		observer: fakeTableObserver{table: table},
		store:    NewStore(10),
		inFlight: make(map[string]InFlightLeg),
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		c.reconcileLoop(ctx, chclient.Client{Node: node}, watch)
		close(done)
	}()

	// The plan must appear well before the hour-scale phase offset elapses:
	// the warm-up publishes immediately, only dispatch waits.
	require.Eventually(t, func() bool {
		return len(c.store.Snapshot().Tables) == 1
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	<-done
}

func TestControllerApplyRespectsPause(t *testing.T) {
	// An operator pause gates supervised writes too — both entry points, and
	// before any node/watch/plan lookups run.
	c := &controller{cfg: ControllerConfig{}, store: NewStore(10)}
	c.store.Pause(PauseReasonOperator)
	_, err := c.Apply(t.Context(), "n1", "db", "tbl", "pid", "tok")
	require.ErrorIs(t, err, ErrTieringPaused)
	require.ErrorContains(t, err, "operator")
	_, err = c.Retry(t.Context(), "n1", "db", "tbl", "pid", "tok")
	require.ErrorIs(t, err, ErrTieringPaused)
}

func TestControllerDispatchGates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeEnforce
	cfg.MaxConcurrentPartitions = 1
	cfg.Safety.MaxMovesPerCycle = 1
	cfg.Safety.MaxBytesInFlight = Bytes{Value: 10}
	cfg.Safety.DiffBreaker.MaxPartitions = 1
	cfg.Safety.DiffBreaker.MaxTableFraction = 0.5
	c := &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10), inFlight: make(map[string]InFlightLeg), log: slog.New(slog.DiscardHandler)}
	v := Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p1", Decision: DecisionTier, BytesOnDisk: 5}
	require.True(t, c.tryStart(v))
	require.False(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p2", BytesOnDisk: 5}))
	c.finish(v, HistoryEntry{Outcome: "success"})
	require.True(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p3", BytesOnDisk: 5}))
	c.finish(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p3", BytesOnDisk: 50}, HistoryEntry{Outcome: "success"})
	require.False(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p4", BytesOnDisk: 50}))
	require.True(t, c.breakerTripped(2, 4))
	cfg.Safety.DiffBreaker.Override = &DiffBreakerOverride{MaxPartitions: 10, MaxTableFraction: 1, Expires: time.Now().Add(time.Hour)}
	c.cfg.Tiering = cfg
	require.False(t, c.breakerTripped(2, 4))
	cfg.Safety.DiffBreaker.Override.Expires = time.Now().Add(-time.Hour)
	c.cfg.Tiering = cfg
	require.True(t, c.breakerTripped(2, 4))
	require.Equal(t, "n1/db/tbl/p1", flightKey(v))

	none := &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10), inFlight: map[string]InFlightLeg{"n/db/t/p": {}}}
	none.finish(Verdict{NodeID: "n", Database: "db", Table: "t", PartitionID: "p", BytesOnDisk: 5}, HistoryEntry{Outcome: "error", Error: "failed"})
	require.Zero(t, none.store.Status().BytesInFlight)
}

func TestControllerDispatchBranches(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeEnforce
	cfg.MaxConcurrentPartitions = 2
	cfg.Safety.MaxMovesPerCycle = 1
	cfg.Safety.MaxBytesInFlight = Bytes{Value: 100}
	cfg.Safety.DiffBreaker.MaxPartitions = 10
	cfg.Safety.DiffBreaker.MaxTableFraction = 1
	calls := make(chan Verdict, 2)
	c := &controller{
		log:          slog.New(slog.DiscardHandler),
		cfg:          ControllerConfig{Tiering: cfg},
		store:        NewStore(10),
		inFlight:     make(map[string]InFlightLeg),
		executor:     fakeActuator{calls: calls},
		instrumenter: noopInstrumenter{},
	}
	table := TableObservation{EffectiveMode: ModePlan, Database: "db", Table: "tbl"}
	c.dispatch(t.Context(), chclient.Client{}, table, []Verdict{{Decision: DecisionTier}})
	require.Empty(t, calls)

	table.EffectiveMode = ModeEnforce
	c.store.Pause(PauseReasonOperator)
	c.dispatch(t.Context(), chclient.Client{}, table, []Verdict{{Decision: DecisionTier}})
	require.Empty(t, calls)
	c.store.Resume()
	c.dispatch(t.Context(), chclient.Client{}, table, []Verdict{{Decision: DecisionKeep}})
	require.Empty(t, calls)

	c.cfg.Tiering.Safety.DiffBreaker.MaxPartitions = 0
	c.dispatch(t.Context(), chclient.Client{}, table, []Verdict{{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p", Decision: DecisionTier, BytesOnDisk: 1}})
	require.Empty(t, calls)

	c.cfg.Tiering.Safety.DiffBreaker.MaxPartitions = 10
	c.dispatch(t.Context(), chclient.Client{}, table, []Verdict{
		{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p1", Decision: DecisionTier, BytesOnDisk: 1},
		{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p2", Decision: DecisionTier, BytesOnDisk: 1},
	})
	c.wg.Wait()
	require.Equal(t, "p1", (<-calls).PartitionID)
	require.Empty(t, calls)
}

func TestControllerFinishTrainingWheels(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Safety.PauseAfterActions = 1
	c := &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10), inFlight: map[string]InFlightLeg{"n/db/t/p": {}}, bytesInFlight: 1}
	c.finish(Verdict{NodeID: "n", Database: "db", Table: "t", PartitionID: "p", BytesOnDisk: 1}, HistoryEntry{Outcome: "success"})
	status := c.store.Status()
	require.Equal(t, PauseStopped, status.PauseState)
	require.Equal(t, PauseReasonTrainingWheels, status.PauseReason)
}

func TestControllerStalledBackoffOverlayAndRetry(t *testing.T) {
	cfg := DefaultConfig()
	c := &controller{
		cfg:      ControllerConfig{Tiering: cfg},
		store:    NewStore(10),
		inFlight: map[string]InFlightLeg{"n/db/t/p": {}},
		stalled:  make(map[string]stalledPartition),
	}
	verdict := Verdict{NodeID: "n", Database: "db", Table: "t", PartitionID: "p", Decision: DecisionTier, Token: "tok", BytesOnDisk: 1}
	c.finish(verdict, HistoryEntry{Outcome: "error", Error: "verify failed"})
	stalled := c.stalled[flightKey(verdict)]
	require.Equal(t, 1, stalled.Failures)
	require.Equal(t, "verify failed", stalled.Reason)
	require.Equal(t, initialStallBackoff, stallBackoff(1))
	require.Equal(t, 12*time.Hour, stallBackoff(2))
	require.Equal(t, maxStallBackoff, stallBackoff(10))
	require.Equal(t, initialStallBackoff, stallBackoff(0))
	require.Equal(t, 48*time.Hour, stallBackoff(4))

	overlaid := c.overlayStalled([]Verdict{verdict}, time.Now().UTC())
	require.Equal(t, StatusStalled, overlaid[0].Status)
	require.Equal(t, DecisionHold, overlaid[0].Decision)
	require.Equal(t, "tok", overlaid[0].Token)
	require.Equal(t, "action_stalled", overlaid[0].Conditions[0].Code)

	changed := verdict
	changed.Token = "new-token"
	overlaid = c.overlayStalled([]Verdict{changed}, time.Now().UTC())
	require.Equal(t, DecisionTier, overlaid[0].Decision)
	require.Empty(t, c.stalled)

	c.stalled[flightKey(verdict)] = stalledPartition{Token: verdict.Token, Until: time.Now().Add(-time.Second), Reason: "old"}
	overlaid = c.overlayStalled([]Verdict{verdict}, time.Now().UTC())
	require.Equal(t, DecisionTier, overlaid[0].Decision)

	c.clearStalled("n", "db", "t", "p")
	require.Empty(t, c.stalled)
	(&controller{}).clearStalled("n", "db", "t", "p")

	c.stalled = map[string]stalledPartition{flightKey(verdict): stalled}
	c.inFlight = map[string]InFlightLeg{flightKey(verdict): {}}
	c.bytesMovedToday = nil
	c.finish(verdict, HistoryEntry{Outcome: "success"})
	require.Empty(t, c.stalled)

	c = &controller{store: NewStore(10), inFlight: make(map[string]InFlightLeg)}
	c.markStalledLocked(verdict, HistoryEntry{Outcome: "error"})
	require.Equal(t, "action failed", c.stalled[flightKey(verdict)].Reason)
}

func TestControllerAdaptiveResplitQuiet(t *testing.T) {
	cfg := DefaultConfig()
	c := &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10)}
	verdict := Verdict{NodeID: "n", Database: "db", Table: "t", PartitionID: "p", Decision: DecisionAppend, Token: "tok", BytesOnDisk: 1}
	c.finish(verdict, HistoryEntry{Outcome: "success"})
	require.Equal(t, 1, c.resplitFlaps[flightKey(verdict)])
	require.Equal(t, 2*time.Hour, adaptiveResplitQuiet(time.Hour, 1))
	require.Equal(t, maxResplitQuiet, adaptiveResplitQuiet(60*24*time.Hour, 1))
	require.Equal(t, time.Hour, adaptiveResplitQuiet(time.Hour, 0))
	require.Zero(t, adaptiveResplitQuiet(0, 2))

	obs := TableObservation{
		Node:     chclient.Node{ID: "n"},
		Database: "db",
		Table:    "t",
		Settings: func() TierSettings {
			settings := frontierSettings()
			settings.Resplit.QuietFor = Duration{Duration: time.Hour}
			return settings
		}(),
		Partitions: []PartitionObservation{{
			PartitionID: "p",
			Disks:       []DiskPart{{Disk: "default", Parts: 1}, {Disk: "s3_cache", Parts: 1}},
		}, {
			PartitionID: "hot",
			Disks:       []DiskPart{{Disk: "default", Parts: 1}},
		}},
	}
	c.applyAdaptiveResplitQuiet(&obs)
	require.Equal(t, 2*time.Hour, obs.ResplitQuiet["p"])
	require.NotContains(t, obs.ResplitQuiet, "hot")
}

func TestNoopInstrumenter(t *testing.T) {
	var instrumenter noopInstrumenter
	instrumenter.RecordReconcile(t.Context(), "node", "db", "tbl", "success", time.Second)
	instrumenter.RecordAction(t.Context(), HistoryEntry{Action: DecisionTier, Outcome: "success"})
	instrumenter.RecordRetry(t.Context(), "node", "db", "tbl", DecisionTier)
	instrumenter.RecordProbeFailure(t.Context(), "node", "db", "tbl")
	instrumenter.RecordSideMerge(t.Context(), "node", "db", "tbl", 1)
}

func TestControllerInFlightTracking(t *testing.T) {
	c := &controller{cfg: ControllerConfig{}, store: NewStore(10), inFlight: make(map[string]InFlightLeg)}
	v := Verdict{NodeID: "n1", Database: "db", Table: "tbl", Partition: "('m',1)", PartitionID: "pid", Decision: DecisionTier, BytesOnDisk: 7}
	require.True(t, c.markSupervised(v))
	// A second mark for the same partition is the double-run guard: another
	// operator's click or an autonomous dispatch must not overlap the leg.
	require.False(t, c.markSupervised(v))
	legs := c.InFlight()
	require.Len(t, legs, 1)
	require.Equal(t, "supervised", legs[0].Source)
	require.Equal(t, DecisionTier, legs[0].Action)
	require.Equal(t, uint64(7), legs[0].Bytes)
	require.False(t, legs[0].StartedAt.IsZero())
	c.unmarkSupervised(v)
	require.Empty(t, c.InFlight())
}

func TestTryStartCapIsPerNode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConcurrentPartitions = 1
	cfg.Safety.MaxBytesInFlight = Bytes{Value: 100}
	cfg.Safety.MaxBytesPerDay = Bytes{Value: 100}
	c := &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10), inFlight: make(map[string]InFlightLeg), bytesMovedToday: map[string]uint64{}, budgetDay: map[string]string{}}

	require.True(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p1", Decision: DecisionTier, BytesOnDisk: 1}))
	// n1's slot is taken; n1 is gated but n2 dispatches freely — nodes own
	// independent disks and must not starve each other.
	require.False(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p2", BytesOnDisk: 1}))
	require.True(t, c.tryStart(Verdict{NodeID: "n2", Database: "db", Table: "tbl", PartitionID: "p2", BytesOnDisk: 1}))
	require.False(t, c.tryStart(Verdict{NodeID: "n2", Database: "db", Table: "tbl", PartitionID: "p3", BytesOnDisk: 1}))
}

func TestTryStartDailyBudgetIsPerNode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConcurrentPartitions = 2
	cfg.Safety.MaxBytesInFlight = Bytes{Value: 1000}
	cfg.Safety.MaxBytesPerDay = Bytes{Value: 100}
	c := &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10), inFlight: make(map[string]InFlightLeg), bytesMovedToday: map[string]uint64{}, budgetDay: map[string]string{}}

	// n1 fills most of its own daily budget with an in-flight leg.
	require.True(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p1", BytesOnDisk: 90}))
	// n1 itself is over budget for another 90 bytes...
	require.False(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p2", BytesOnDisk: 90}))
	// ...but n2's daily headroom is its own; n1's in-flight bytes must not
	// count against it.
	require.True(t, c.tryStart(Verdict{NodeID: "n2", Database: "db", Table: "tbl", PartitionID: "p2", BytesOnDisk: 90}))
}

func TestControllerApplyRejectsDriftedState(t *testing.T) {
	now := time.Now().UTC()
	cached := frontierObservation(now)
	cached.Database = "db"
	cached.Table = "tbl"
	cached.Node = chclient.Node{ID: "n1"}
	cachedVerdict := DecidePartition(cached, cached.Partitions[2], now) // hot-old: actionable

	// The partition advances between the plan the operator saw and the apply:
	// the re-observed state has fewer parts (a merge landed). The cached token
	// still matches the plan store, but the fresh verdict re-derives a
	// different token — the apply must fail as stale, not execute the fresh
	// action under the old click.
	drifted := cached
	drifted.Partitions = append([]PartitionObservation(nil), cached.Partitions...)
	drifted.Partitions[2].ActiveParts = 1
	drifted.Partitions[2].BytesOnDisk = cached.Partitions[2].BytesOnDisk / 2

	settings := cached.Settings
	store := NewStore(10)
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "tbl", Verdicts: []Verdict{cachedVerdict}})
	c := &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: DefaultConfig(), Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: &settings}}},
		clients:  []chclient.Client{{Node: chclient.Node{ID: "n1"}}},
		observer: fakeTableObserver{table: drifted},
		store:    store,
		inFlight: make(map[string]InFlightLeg),
		executor: fakeActuator{},
	}

	_, err := c.Apply(t.Context(), "n1", "db", "tbl", cachedVerdict.PartitionID, cachedVerdict.Token)
	require.ErrorIs(t, err, ErrStateTokenMismatch)
	require.Empty(t, c.InFlight(), "a stale apply must not burn a concurrency slot")
}

func TestControllerRetryPreservesStallOnPreExecutionFailure(t *testing.T) {
	now := time.Now().UTC()
	cached := frontierObservation(now)
	cached.Database = "db"
	cached.Table = "tbl"
	cached.Node = chclient.Node{ID: "n1"}
	cachedVerdict := DecidePartition(cached, cached.Partitions[2], now)

	drifted := cached
	drifted.Partitions = append([]PartitionObservation(nil), cached.Partitions...)
	drifted.Partitions[2].ActiveParts = 1
	drifted.Partitions[2].BytesOnDisk = cached.Partitions[2].BytesOnDisk / 2

	settings := cached.Settings
	store := NewStore(10)
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "tbl", Verdicts: []Verdict{cachedVerdict}})
	stalled := stalledPartition{
		Token:    cachedVerdict.Token,
		Until:    now.Add(time.Hour),
		Failures: 2,
		Reason:   "previous move failed",
	}
	c := &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: DefaultConfig(), Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: &settings}}},
		clients:  []chclient.Client{{Node: chclient.Node{ID: "n1"}}},
		observer: fakeTableObserver{table: drifted},
		store:    store,
		inFlight: make(map[string]InFlightLeg),
		stalled:  map[string]stalledPartition{flightKey(cachedVerdict): stalled},
		executor: fakeActuator{},
	}

	_, err := c.Retry(t.Context(), "n1", "db", "tbl", cachedVerdict.PartitionID, cachedVerdict.Token)
	require.ErrorIs(t, err, ErrStateTokenMismatch)
	require.Equal(t, stalled, c.stalled[flightKey(cachedVerdict)])
}
