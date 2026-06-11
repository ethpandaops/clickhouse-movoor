package tiering

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

type fakeRefreshObserver struct {
	partitions []PartitionObservation
	err        error
	calls      int
}

type fakeMoveObserver struct {
	*fakeRefreshObserver
	mutationErr error
	freeErr     error
}

func (f *fakeRefreshObserver) ObserveTable(context.Context, chclient.Client, EffectiveWatch) (TableObservation, error) {
	return TableObservation{}, errors.New("not used")
}

func (f *fakeRefreshObserver) RefreshPartition(context.Context, chclient.Client, TableObservation, string) (PartitionObservation, error) {
	f.calls++
	if f.err != nil {
		return PartitionObservation{}, f.err
	}
	if len(f.partitions) == 0 {
		return PartitionObservation{}, sql.ErrNoRows
	}
	if f.calls <= len(f.partitions) {
		return withTestHashes(f.partitions[f.calls-1]), nil
	}
	return withTestHashes(f.partitions[len(f.partitions)-1]), nil
}

func withTestHashes(partition PartitionObservation) PartitionObservation {
	if len(partition.Hashes) > 0 || partition.ActiveParts == 0 {
		return partition
	}
	var index uint64
	for _, disk := range partition.Disks {
		for range disk.Parts {
			suffix := strconv.FormatUint(index, 10)
			partition.Hashes = append(partition.Hashes, PartHash{
				Name: "part-" + suffix,
				Hash: "hash-" + suffix,
				Disk: disk.Disk,
			})
			index++
		}
	}
	if len(partition.Hashes) == 0 {
		partition.Hashes = append(partition.Hashes, PartHash{Name: "part-a", Hash: "hash-a", Disk: ""})
	}
	return partition
}

func (f fakeMoveObserver) CheckMutationsClear(context.Context, chclient.Client, TableObservation) error {
	return f.mutationErr
}

func (f fakeMoveObserver) CheckOptimizeFreeSpace(context.Context, chclient.Client, TableObservation, Verdict) error {
	return f.freeErr
}

func TestExecutorApplyTierSuccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	// The tier leg is move-only: no OPTIMIZE statement may be dispatched.
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid' TO DISK 's3_cache'")).WillReturnResult(sqlmock.NewResult(0, 0))

	observer := &fakeRefreshObserver{partitions: []PartitionObservation{
		{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
		{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}},
	}}
	executor := NewExecutor(slog.New(slog.DiscardHandler), NewStore(10), observer, "instance")
	executor.PollInterval = time.Millisecond
	table := executorTable(true)
	verdict := executorVerdict(DecisionTier, 1, 100)
	entry := executor.Apply(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db}, table, verdict)
	require.Equal(t, "success", entry.Outcome)
	require.NotEmpty(t, entry.AttemptID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutorApplyOneStatementPerLeg(t *testing.T) {
	for _, tt := range []struct {
		name       string
		decision   Decision
		side       OptimizeSide
		exec       string
		direction  string
		partitions []PartitionObservation
	}{
		{
			name:      "append",
			decision:  DecisionAppend,
			exec:      "ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid' TO DISK 's3_cache'",
			direction: "up",
			partitions: []PartitionObservation{
				{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}},
				{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}},
			},
		},
		{
			name:      "consolidate toward hot",
			decision:  DecisionConsolidate,
			exec:      "ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid' TO VOLUME 'hot'",
			direction: "down",
			partitions: []PartitionObservation{
				{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}},
				{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
			},
		},
		{
			name:      "consolidate toward cold",
			decision:  DecisionConsolidate,
			side:      OptimizeOnCold,
			exec:      "ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid' TO DISK 's3_cache'",
			direction: "up",
			partitions: []PartitionObservation{
				{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
				{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}},
			},
		},
		{
			name:     "optimize",
			decision: DecisionOptimize,
			exec:     "OPTIMIZE TABLE `db`.`tbl` PARTITION ID 'pid' FINAL",
			partitions: []PartitionObservation{
				{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectExec(regexp.QuoteMeta(tt.exec)).WillReturnResult(sqlmock.NewResult(0, 0))
			observer := &fakeRefreshObserver{partitions: tt.partitions}
			executor := NewExecutor(slog.New(slog.DiscardHandler), NewStore(10), observer, "instance")
			executor.PollInterval = time.Millisecond
			table := executorTable(false)
			if tt.side != "" {
				table.Settings.OptimizeOn = tt.side
			}
			entry := executor.Apply(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db}, table, executorVerdict(tt.decision, 1, 10))
			require.Equal(t, "success", entry.Outcome)
			require.Equal(t, tt.direction, entry.Direction)
			// Exactly one statement per leg — the single ExpectExec above must
			// be consumed and nothing else dispatched.
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestExecutorApplyErrorsAndTimeouts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	executor := NewExecutor(slog.New(slog.DiscardHandler), NewStore(10), &fakeRefreshObserver{}, "instance")
	require.Equal(t, 2*time.Second, executor.pollInterval())
	entry := executor.Apply(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db}, executorTable(true), executorVerdict(DecisionKeep, 1, 1))
	require.Equal(t, "error", entry.Outcome)
	require.Contains(t, entry.Error, "not actionable")
	err = executor.apply(t.Context(), chclient.Client{}, executorTable(true), executorVerdict(Decision("unexpected"), 1, 1), "attempt")
	require.ErrorContains(t, err, "not actionable")

	noHot := executorTable(true)
	noHot.HotVolume = ""
	entry = executor.Apply(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db}, noHot, executorVerdict(DecisionConsolidate, 1, 1))
	require.Equal(t, "error", entry.Outcome)
	require.Contains(t, entry.Error, "hot volume")

	mock.ExpectExec("OPTIMIZE").WillReturnResult(sqlmock.NewResult(0, 0))
	timeoutExec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 2}}}, "instance")
	timeoutExec.PollInterval = time.Millisecond
	table := executorTable(true)
	table.Settings.OptimizeStallAfter = Duration{Duration: time.Millisecond}
	err = timeoutExec.optimize(t.Context(), chclient.Client{DB: db}, table, executorVerdict(DecisionOptimize, 2, 10), "attempt")
	require.ErrorContains(t, err, "stalled")
	require.NoError(t, mock.ExpectationsWereMet())

	moveTimeout := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}}}}, "instance")
	moveTimeout.PollInterval = time.Millisecond
	table.Settings.OptimizeStallAfter = Duration{Duration: time.Millisecond}
	err = moveTimeout.waitForVerifiedDisk(t.Context(), chclient.Client{}, table, "pid", onColdDisk, []PartHash{{Name: "part-0", Hash: "hash-0", Disk: "default"}}, time.Millisecond)
	require.ErrorIs(t, err, errVerifyNotOnTarget)

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectExec("ALTER TABLE").WillReturnError(&clickhouse.Exception{Code: 548, Message: "No such volume"})
	volExec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}}}}, "instance")
	volExec.PollInterval = time.Millisecond
	entry = volExec.Apply(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db2}, executorTable(true), executorVerdict(DecisionConsolidate, 1, 1))
	require.Equal(t, "error", entry.Outcome)
	require.Contains(t, entry.Error, "volume")
	require.NoError(t, mock2.ExpectationsWereMet())
}

// onColdDisk is the move-destination predicate used by cold-bound legs.
func onColdDisk(disk string) bool { return disk == "s3_cache" }

func TestMoveAlreadyMovedAndClassifiers(t *testing.T) {
	require.True(t, isNoopOptimize(&clickhouse.Exception{Code: 388, Message: "already merged"}))
	require.False(t, isNoopOptimize(errors.New("plain")))
	require.True(t, isAlreadyMoved(&clickhouse.Exception{Code: 479, Message: "All parts are already on disk 's3_cache'"}))
	require.True(t, isAlreadyMoved(&clickhouse.Exception{Code: 479, Message: "All parts are already on volume 'cold'"}))
	require.False(t, isAlreadyMoved(errors.New("plain")))
	for _, tt := range []struct {
		err  error
		want string
	}{
		{&clickhouse.Exception{Code: 479, Message: "No such disk"}, "disk"},
		{&clickhouse.Exception{Code: 548, Message: "No such volume"}, "volume"},
		{&clickhouse.Exception{Code: 384, Message: "locked"}, "locked"},
		{&clickhouse.Exception{Code: 243, Message: "space"}, "space"},
		{&clickhouse.Exception{Code: 236, Message: "disabled"}, "disabled"},
		{&clickhouse.Exception{Code: 84, Message: "exists"}, "directory"},
		{&clickhouse.Exception{Code: 439, Message: "schedule"}, "schedule"},
		{&clickhouse.Exception{Code: 159, Message: "timeout"}, "max_execution_time"},
		{&clickhouse.Exception{Code: 999, Message: "other"}, "code 999"},
		{errors.New("plain"), "plain"},
	} {
		require.ErrorContains(t, classifyClickHouseError("move", tt.err), tt.want)
	}

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("ALTER TABLE").WillReturnError(&clickhouse.Exception{Code: 479, Message: "All parts are already on disk 's3_cache'"})
	executor := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}}}}, "instance")
	executor.PollInterval = time.Millisecond
	require.NoError(t, executor.moveToCold(t.Context(), chclient.Client{DB: db}, executorTable(true), executorVerdict(DecisionTier, 1, 1), "attempt"))
	require.NoError(t, mock.ExpectationsWereMet())
}

//nolint:funlen // Verify edge cases stay together to keep the convergence invariants visible.
func TestVerifyPartHashes(t *testing.T) {
	baseline := []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}
	cold := onColdDisk
	require.NoError(t, verifyPartHashes(baseline, []PartHash{{Name: "p1", Hash: "h1", Disk: "s3_cache"}}, cold))
	require.ErrorIs(t, verifyPartHashes(nil, []PartHash{{Name: "p1", Hash: "h1", Disk: "s3_cache"}}, cold), errVerifyNoParts)
	require.ErrorIs(t, verifyPartHashes(baseline, nil, cold), errVerifyNoParts)
	require.ErrorIs(t, verifyPartHashes(baseline, []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}, cold), errVerifyNotOnTarget)
	require.ErrorIs(t, verifyPartHashes(baseline, []PartHash{{Name: "p1", Hash: "changed", Disk: "s3_cache"}}, cold), errVerifyHashMismatch)
	require.ErrorIs(t, verifyPartHashes(baseline, []PartHash{{Name: "p2", Hash: "h2", Disk: "s3_cache"}}, cold), errVerifyBaselineDrift)
	require.ErrorIs(t, verifyPartHashes(baseline, []PartHash{{Name: "p1", Hash: "h1", Disk: "s3_cache"}, {Name: "p2", Hash: "h2", Disk: "s3_cache"}}, cold), errVerifyBaselineDrift)
	// The move-to-hot predicate accepts any disk except the cold target.
	notCold := func(disk string) bool { return disk != "s3_cache" }
	require.NoError(t, verifyPartHashes([]PartHash{{Name: "p1", Hash: "h1", Disk: "s3_cache"}}, []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}, notCold))
	require.ErrorIs(t, verifyPartHashes([]PartHash{{Name: "p1", Hash: "h1", Disk: "s3_cache"}}, []PartHash{{Name: "p1", Hash: "h1", Disk: "s3_cache"}}, notCold), errVerifyNotOnTarget)
	require.Equal(t, baseline, copyPartHashes(baseline))

	exec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 0}}}, "instance")
	_, err := exec.captureMoveBaseline(t.Context(), chclient.Client{}, executorTable(true), "pid")
	require.ErrorIs(t, err, errVerifyNoParts)

	require.Equal(t, 2*time.Second, (&Executor{}).pollInterval())
	require.NoError(t, (&Executor{Observer: fakeMoveObserver{fakeRefreshObserver: &fakeRefreshObserver{}}}).checkMutationsClear(t.Context(), chclient.Client{}, executorTable(true)))
	require.NoError(t, (&Executor{Observer: &fakeRefreshObserver{}}).checkOptimizeFreeSpace(t.Context(), chclient.Client{}, executorTable(true), executorVerdict(DecisionTier, 1, 1)))
	err = (&Executor{Observer: fakeMoveObserver{fakeRefreshObserver: &fakeRefreshObserver{}, freeErr: errors.New("space gate")}}).checkOptimizeFreeSpace(t.Context(), chclient.Client{}, executorTable(true), executorVerdict(DecisionTier, 1, 1))
	require.ErrorContains(t, err, "space gate")
	err = (&Executor{Observer: fakeMoveObserver{fakeRefreshObserver: &fakeRefreshObserver{}, freeErr: errors.New("space gate")}}).optimize(t.Context(), chclient.Client{}, executorTable(true), executorVerdict(DecisionTier, 2, 1), "attempt")
	require.ErrorContains(t, err, "space gate")

	noRows := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{err: sql.ErrNoRows}, "instance")
	noRows.PollInterval = time.Millisecond
	err = noRows.waitForVerifiedDisk(t.Context(), chclient.Client{}, executorTable(true), "pid", cold, baseline, time.Millisecond)
	require.ErrorIs(t, err, errVerifyNoParts)

	refreshErr := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{err: errors.New("refresh failed")}, "instance")
	refreshErr.PollInterval = time.Millisecond
	err = refreshErr.waitForVerifiedDisk(t.Context(), chclient.Client{}, executorTable(true), "pid", cold, baseline, time.Second)
	require.ErrorContains(t, err, "refresh failed")

	hashMismatch := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Hashes: []PartHash{{Name: "p1", Hash: "changed", Disk: "s3_cache"}}}}}, "instance")
	err = hashMismatch.waitForVerifiedDisk(t.Context(), chclient.Client{}, executorTable(true), "pid", cold, baseline, time.Second)
	require.ErrorIs(t, err, errVerifyHashMismatch)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	moveHashMismatch := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{
		{PartitionID: "pid", ActiveParts: 1, Hashes: []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}},
		{PartitionID: "pid", ActiveParts: 1, Hashes: []PartHash{{Name: "p1", Hash: "changed", Disk: "s3_cache"}}},
	}}, "instance")
	err = moveHashMismatch.moveToCold(t.Context(), chclient.Client{DB: db}, executorTable(true), executorVerdict(DecisionTier, 1, 1), "attempt")
	require.ErrorIs(t, err, errVerifyHashMismatch)
	require.NoError(t, mock.ExpectationsWereMet())

	mutationExec := NewExecutor(slog.New(slog.DiscardHandler), nil, fakeMoveObserver{
		fakeRefreshObserver: &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}}}},
		mutationErr:         errors.New("mutation blocks move"),
	}, "instance")
	err = mutationExec.moveToCold(t.Context(), chclient.Client{}, executorTable(true), executorVerdict(DecisionTier, 1, 1), "attempt")
	require.ErrorContains(t, err, "mutation blocks move")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cancelWait := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Hashes: []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}}}}, "instance")
	err = cancelWait.waitForVerifiedDisk(ctx, chclient.Client{}, executorTable(true), "pid", cold, []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}, time.Second)
	require.ErrorIs(t, err, context.Canceled)
}

func executorTable(replicated bool) TableObservation {
	settings := DefaultTierSettings()
	settings.Age = AgeSettings{Basis: AgeBasisFrontier, Field: "block_number", KeepLast: 1}
	settings.OptimizeStallAfter = Duration{Duration: time.Second}
	return TableObservation{
		Node:         chclient.Node{ID: "n1"},
		Database:     "db",
		Table:        "tbl",
		IsReplicated: replicated,
		HotVolume:    "hot",
		Settings:     settings,
	}
}

func executorVerdict(decision Decision, parts uint64, bytes uint64) Verdict {
	return Verdict{NodeID: "n1", Database: "db", Table: "tbl", Partition: "p", PartitionID: "pid", Decision: decision, ActiveParts: parts, BytesOnDisk: bytes}
}

type fakeInsertObserver struct {
	*fakeRefreshObserver
	insertCount uint64
	insertErr   error
}

func (f fakeInsertObserver) CountInsertsSince(context.Context, chclient.Client, TableObservation, string, time.Time) (uint64, error) {
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	return f.insertCount, nil
}

func TestExecutorAbortsOnFreshInserts(t *testing.T) {
	observer := fakeInsertObserver{
		fakeRefreshObserver: &fakeRefreshObserver{partitions: []PartitionObservation{
			{PartitionID: "pid", ActiveParts: 2, Disks: []DiskPart{{Disk: "default", Parts: 2}}},
		}},
		insertCount: 1,
	}
	executor := NewExecutor(slog.New(slog.DiscardHandler), NewStore(10), observer, "instance")
	executor.PollInterval = time.Millisecond
	table := executorTable(true)
	table.ObservedAt = time.Now().UTC()
	// Fresh NewPart evidence since the decision-time observation aborts the
	// attempt before any SQL is dispatched (client.DB is nil, so reaching a
	// statement would panic).
	entry := executor.Apply(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, table, executorVerdict(DecisionTier, 2, 100))
	require.Equal(t, "error", entry.Outcome)
	require.Contains(t, entry.Error, "fresh inserts")

	clean := NewExecutor(slog.New(slog.DiscardHandler), nil, fakeInsertObserver{fakeRefreshObserver: &fakeRefreshObserver{}}, "instance")
	require.NoError(t, clean.guardFreshInserts(t.Context(), chclient.Client{}, table, "pid"))

	erring := NewExecutor(slog.New(slog.DiscardHandler), nil, fakeInsertObserver{fakeRefreshObserver: &fakeRefreshObserver{}, insertErr: errors.New("part_log failed")}, "instance")
	require.ErrorContains(t, erring.optimize(t.Context(), chclient.Client{}, table, executorVerdict(DecisionOptimize, 2, 100), "attempt"), "part_log failed")
}

func TestOrderOldestFirst(t *testing.T) {
	frontier := TableObservation{
		Layout: TableLayout{Basis: AgeBasisFrontier},
		Partitions: []PartitionObservation{
			{PartitionID: "new", AgeInteger: 5},
			{PartitionID: "old", AgeInteger: 1},
			{PartitionID: "mid", AgeInteger: 3},
		},
	}
	verdicts := []Verdict{{PartitionID: "new"}, {PartitionID: "old"}, {PartitionID: "mid"}}
	orderOldestFirst(frontier, verdicts)
	require.Equal(t, []string{"old", "mid", "new"}, partitionIDOrder(verdicts))

	monthly := TableObservation{
		Layout: TableLayout{Basis: AgeBasisPartitionTime, TimeFunction: "toYYYYMM"},
		Partitions: []PartitionObservation{
			{PartitionID: "p2026-04", AgeString: "202604"},
			{PartitionID: "p2026-01", AgeString: "202601"},
		},
	}
	monthlyVerdicts := []Verdict{{PartitionID: "p2026-04"}, {PartitionID: "p2026-01"}}
	orderOldestFirst(monthly, monthlyVerdicts)
	require.Equal(t, []string{"p2026-01", "p2026-04"}, partitionIDOrder(monthlyVerdicts))
	require.Zero(t, agePartitionRank(TableLayout{Basis: AgeBasisPartitionTime, TimeZone: "No/SuchZone"}, PartitionObservation{AgeString: "202601"}))
	require.Zero(t, agePartitionRank(TableLayout{Basis: AgeBasisPartitionTime, TimeFunction: "toYYYYMM"}, PartitionObservation{AgeString: "bad"}))
	require.Empty(t, legDirection(frontierSettings(), Decision("unknown")))
}

func partitionIDOrder(verdicts []Verdict) []string {
	out := make([]string, 0, len(verdicts))
	for _, verdict := range verdicts {
		out = append(out, verdict.PartitionID)
	}
	return out
}
