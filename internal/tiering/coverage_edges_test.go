//nolint:modernize // Pointer helpers keep edge-case fixtures readable.
package tiering

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

//nolint:funlen // Edge coverage stays compact so related decision/layout fixtures remain visible together.
func TestDecisionAndLayoutEdgeCoverage(t *testing.T) {
	now := time.Now()
	obs := frontierObservation(now)
	part := obs.Partitions[2]
	delete(obs.Heads, "mainnet")
	ok, reason := ageEligible(obs, part, now)
	require.False(t, ok)
	require.Contains(t, reason, "unavailable")

	obs = frontierObservation(now)
	obs.Settings.Age.KeepLast = uint64(math.MaxInt64) + 1
	ok, reason = ageEligible(obs, obs.Partitions[3], now)
	require.False(t, ok)
	require.Contains(t, reason, "supported int64")

	obs = frontierObservation(now)
	obs.Heads["mainnet"] = 10
	for i := range obs.Partitions {
		obs.Partitions[i].LatestNewPart = ptrTime(now.Add(-time.Minute))
	}
	ok, reason = ageEligible(obs, obs.Partitions[3], now)
	require.False(t, ok)
	require.Contains(t, reason, "keepLast")

	obs.Settings.Age.Basis = ""
	ok, reason = ageEligible(obs, obs.Partitions[3], now)
	require.False(t, ok)
	require.Contains(t, reason, "not configured")

	v := DecidePartition(obs, obs.Partitions[3], now)
	require.Equal(t, DecisionKeep, v.Decision)
	obs = frontierObservation(now)
	obs.Heads["mainnet"] = 1000
	obs.Partitions[2].LatestNewPart = ptrTime(now.Add(-time.Second))
	v = DecidePartition(obs, obs.Partitions[2], now)
	require.Equal(t, DecisionHold, v.Decision)

	layout := TableLayout{Basis: AgeBasisFrontier, GroupColumns: []string{"network_id"}, FrontierDivisor: 100}
	_, err := layout.ParsePartition("(")
	require.Error(t, err)
	_, err = layout.ParsePartition("1")
	require.ErrorContains(t, err, "expects 2")
	layout.GroupColumns = nil
	_, err = layout.ParsePartition("not-int")
	require.ErrorContains(t, err, "not an integer")
	_, err = parseTupleLiteral("()")
	require.ErrorContains(t, err, "must not be empty")
	_, err = parseTupleLiteral("(a,b)")
	require.NoError(t, err)
	_, err = parseTupleLiteral("(a,)")
	require.ErrorContains(t, err, "trailing comma")
	_, err = parseTupleLiteral("(,a)")
	require.ErrorContains(t, err, "empty unquoted")
	_, err = parseTupleLiteral("('a'x,1)")
	require.ErrorContains(t, err, "unexpected tail")
	_, err = splitTopLevel("a, 'unterminated")
	require.Error(t, err)
	_, err = splitTopLevel("a)")
	require.ErrorContains(t, err, "unbalanced")
	require.Equal(t, -1, topLevelComma("(a,b)"))
	require.Equal(t, 3, topLevelComma("(a),b"))
	require.Equal(t, 6, topLevelComma("'a\\'b',c"))
	require.False(t, isBareIdentifier(""))
	frozen := frontierObservation(now)
	for i := range frozen.Partitions {
		frozen.Partitions[i].LatestNewPart = nil
		frozen.Partitions[i].MaxModificationTime = now.Add(-2 * frozen.Settings.TierFrozenAfter.Duration)
	}
	require.True(t, groupFrozen(frozen, "mainnet", now))
	frozen.Heads["mainnet"] = 620
	eligible, reason := ageEligible(frozen, frozen.Partitions[3], now)
	require.True(t, eligible)
	require.Contains(t, reason, "frozen")
	frozen.Partitions = append([]PartitionObservation{{GroupKey: "other"}}, frozen.Partitions...)
	require.True(t, groupFrozen(frozen, "mainnet", now))

	timeObs := timeObservation(now, "toYYYYMM", "202606")
	timeObs.Partitions[0].MaxModificationTime = now.Add(-2 * timeObs.Settings.TierFrozenAfter.Duration)
	eligible, reason = ageEligible(timeObs, timeObs.Partitions[0], now)
	require.True(t, eligible)
	require.Contains(t, reason, "frozen")
	require.False(t, groupFrozen(frontierObservation(now), "mainnet", now))

	_, _, err = readQuotedTupleString("'dangling\\")
	require.Error(t, err)
	quoted, _, err := readQuotedTupleString("'a\\nb\\tc\\r'")
	require.NoError(t, err)
	require.Equal(t, "a\nb\tc\r", quoted)
	_, err = splitTopLevel("a, 'b\\'c'")
	require.NoError(t, err)
}

//nolint:funlen // Edge coverage stays compact so executor fixture sequencing remains visible together.
func TestExecutorEdgeCoverage(t *testing.T) {
	require.NotNil(t, NewExecutor(nil, nil, nil, "instance"))

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("OPTIMIZE").WillReturnError(&clickhouse.Exception{Code: 388, Message: "already merged"})
	exec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1}}}, "instance")
	exec.PollInterval = time.Millisecond
	require.NoError(t, exec.optimize(t.Context(), chclient.Client{DB: db}, executorTable(true), executorVerdict(DecisionTier, 2, 1), "attempt"))
	require.NoError(t, mock.ExpectationsWereMet())

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectExec("OPTIMIZE").WillReturnError(errors.New("boom"))
	err = exec.optimize(t.Context(), chclient.Client{DB: db2}, executorTable(true), executorVerdict(DecisionTier, 2, 1), "attempt")
	require.ErrorContains(t, err, "boom")

	err = NewExecutor(slog.New(slog.DiscardHandler), nil, fakeSafetyObserver{identity: errors.New("identity")}, "instance").
		optimize(t.Context(), chclient.Client{}, executorTable(true), executorVerdict(DecisionTier, 2, 1), "attempt")
	require.ErrorContains(t, err, "identity")

	db3, mock3, err := sqlmock.New()
	require.NoError(t, err)
	defer db3.Close()
	table := executorTable(true)
	table.Settings.SkipOptimize = true
	mock3.ExpectExec("ALTER TABLE").WillReturnError(errors.New("move boom"))
	entry := exec.Apply(t.Context(), chclient.Client{DB: db3}, table, executorVerdict(DecisionTier, 2, 1))
	require.Equal(t, "error", entry.Outcome)
	require.Contains(t, entry.Error, "move boom")

	db4, mock4, err := sqlmock.New()
	require.NoError(t, err)
	defer db4.Close()
	mock4.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock4.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	retryObserver := &fakeRefreshObserver{partitions: []PartitionObservation{
		{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
		{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
		{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}},
	}}
	retryExec := NewExecutor(slog.New(slog.DiscardHandler), nil, retryObserver, "instance")
	retryExec.PollInterval = time.Millisecond
	table.Settings.OptimizeStallAfter = Duration{Duration: time.Millisecond}
	require.NoError(t, retryExec.moveToCold(t.Context(), chclient.Client{DB: db4}, table, executorVerdict(DecisionTier, 1, 1), "attempt"))
	require.NoError(t, mock4.ExpectationsWereMet())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = retryExec.waitForPartCount(ctx, chclient.Client{}, table, "pid", 0, time.Second)
	require.ErrorIs(t, err, context.Canceled)
	err = retryExec.waitForVerifiedDisk(t.Context(), chclient.Client{}, table, "pid", onColdDisk, []PartHash{{Name: "part-0", Hash: "hash-0", Disk: "s3_cache"}}, time.Second)
	require.NoError(t, err)
	err = (&Executor{Observer: &fakeRefreshObserver{err: errors.New("refresh")}}).waitForVerifiedDisk(t.Context(), chclient.Client{}, table, "pid", onColdDisk, []PartHash{{Name: "part-0", Hash: "hash-0", Disk: "s3_cache"}}, time.Second)
	require.ErrorContains(t, err, "refresh")

	db5, mock5, err := sqlmock.New()
	require.NoError(t, err)
	defer db5.Close()
	mock5.ExpectExec("ALTER TABLE").WillReturnError(&clickhouse.Exception{Code: 479, Message: "already on volume 'hot'"})
	hotObserver := &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}}}}
	hotExec := NewExecutor(slog.New(slog.DiscardHandler), nil, hotObserver, "instance")
	hotExec.PollInterval = time.Millisecond
	require.NoError(t, hotExec.moveToHot(t.Context(), chclient.Client{DB: db5}, table, executorVerdict(DecisionConsolidate, 1, 1), "attempt"))

	db7, mock7, err := sqlmock.New()
	require.NoError(t, err)
	defer db7.Close()
	for range 3 {
		mock7.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	stuckExec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}}}}, "instance")
	stuckExec.PollInterval = time.Millisecond
	table.Settings.OptimizeStallAfter = Duration{Duration: time.Millisecond}
	err = stuckExec.moveToCold(t.Context(), chclient.Client{DB: db7}, table, executorVerdict(DecisionTier, 1, 1), "attempt")
	require.ErrorContains(t, err, "did not converge")

	db8, mock8, err := sqlmock.New()
	require.NoError(t, err)
	defer db8.Close()
	mock8.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	errExec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{err: errors.New("refresh")}, "instance")
	errExec.PollInterval = time.Millisecond
	err = errExec.moveToCold(t.Context(), chclient.Client{DB: db8}, table, executorVerdict(DecisionTier, 1, 1), "attempt")
	require.ErrorContains(t, err, "refresh")

	err = NewExecutor(slog.New(slog.DiscardHandler), nil, fakeSafetyObserver{identity: errors.New("move identity")}, "instance").
		moveToHot(t.Context(), chclient.Client{}, table, executorVerdict(DecisionConsolidate, 1, 1), "attempt")
	require.ErrorContains(t, err, "move identity")

	db10, mock10, err := sqlmock.New()
	require.NoError(t, err)
	defer db10.Close()
	mock10.ExpectExec("OPTIMIZE").WillReturnError(errors.New("optimize boom"))
	err = NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{}, "instance").
		apply(t.Context(), chclient.Client{DB: db10}, executorTable(true), executorVerdict(DecisionOptimize, 2, 1), "attempt")
	require.ErrorContains(t, err, "optimize boom")

	ctxMove, cancelMove := context.WithCancel(t.Context())
	cancelMove()
	err = stuckExec.waitForVerifiedDisk(ctxMove, chclient.Client{}, table, "pid", onColdDisk, []PartHash{{Name: "part-0", Hash: "hash-0", Disk: "default"}}, time.Second)
	require.ErrorIs(t, err, context.Canceled)

	tickExec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{
		{PartitionID: "pid", ActiveParts: 2, Disks: []DiskPart{{Disk: "default", Parts: 2}}},
		{PartitionID: "pid", ActiveParts: 1, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
	}}, "instance")
	tickExec.PollInterval = time.Nanosecond
	require.NoError(t, tickExec.waitForPartCount(t.Context(), chclient.Client{}, table, "pid", 1, time.Second))

	tickMoveExec := NewExecutor(slog.New(slog.DiscardHandler), nil, &fakeRefreshObserver{partitions: []PartitionObservation{
		{PartitionID: "pid", ActiveParts: 1, Hashes: []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}},
		{PartitionID: "pid", ActiveParts: 1, Hashes: []PartHash{{Name: "p1", Hash: "h1", Disk: "s3_cache"}}},
	}}, "instance")
	tickMoveExec.PollInterval = time.Nanosecond
	require.NoError(t, tickMoveExec.waitForVerifiedDisk(t.Context(), chclient.Client{}, table, "pid", onColdDisk, []PartHash{{Name: "p1", Hash: "h1", Disk: "default"}}, time.Second))
}

//nolint:funlen // Edge coverage stays compact so SQL observer fixture sequencing remains visible together.
func TestObserverEdgeCoverage(t *testing.T) {
	observer := NewSQLObserver(time.Second)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("FROM system.storage_policies").WillReturnRows(sqlmock.NewRows([]string{"policy_name", "volume_name", "volume_priority", "disks_csv", "move_factor"}).
		AddRow("policy", "hot", uint64(1), "default", 0.0).RowError(0, errors.New("row bad")))
	require.Error(t, observer.collectStoragePolicy(t.Context(), db, &TableObservation{StoragePolicy: "policy", Settings: frontierSettings()}))

	db1b, mock1b, err := sqlmock.New()
	require.NoError(t, err)
	defer db1b.Close()
	mock1b.ExpectQuery("FROM system.storage_policies").WillReturnRows(sqlmock.NewRows([]string{"policy_name", "volume_name", "volume_priority", "disks_csv", "move_factor"}).
		AddRow("policy", "hot", "bad", "default", 0.0))
	require.Error(t, observer.collectStoragePolicy(t.Context(), db1b, &TableObservation{StoragePolicy: "policy", Settings: frontierSettings()}))

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectQuery(regexp.QuoteMeta("SELECT '' AS group_key")).WillReturnRows(sqlmock.NewRows([]string{"group_key", "head"}).AddRow("", int64(1)))
	heads, err := observer.collectFrontierHeads(t.Context(), db2, TableLayout{Database: "db", Table: "tbl", AgeField: "block_number"})
	require.NoError(t, err)
	require.Equal(t, int64(1), heads[""])

	db3, mock3, err := sqlmock.New()
	require.NoError(t, err)
	defer db3.Close()
	mock3.ExpectQuery(regexp.QuoteMeta("SELECT '' AS group_key")).WillReturnRows(sqlmock.NewRows([]string{"group_key", "head"}).AddRow("bad", "not-int"))
	_, err = observer.collectFrontierHeads(t.Context(), db3, TableLayout{Database: "db", Table: "tbl", AgeField: "block_number"})
	require.Error(t, err)

	db3b, mock3b, err := sqlmock.New()
	require.NoError(t, err)
	defer db3b.Close()
	mock3b.ExpectQuery(regexp.QuoteMeta("SELECT concat(toString(`network_id`), '\\x00', toString(`bucket`)) AS group_key")).WillReturnRows(sqlmock.NewRows([]string{"group_key", "head"}).AddRow("mainnet\x000", int64(1)))
	heads, err = observer.collectFrontierHeads(t.Context(), db3b, TableLayout{Database: "db", Table: "tbl", AgeField: "block_number", GroupColumns: []string{"network_id", "bucket"}})
	require.NoError(t, err)
	require.Equal(t, int64(1), heads["mainnet\x000"])

	require.NoError(t, func() error {
		replica, replicaErr := observer.collectReplica(t.Context(), nil, "db", "tbl", false)
		require.Nil(t, replica)
		return replicaErr
	}())

	db4, mock4, err := sqlmock.New()
	require.NoError(t, err)
	defer db4.Close()
	mock4.ExpectQuery("FROM system.replicas").WillReturnRows(sqlmock.NewRows([]string{"is_readonly", "is_session_expired", "queue_size", "absolute_delay"}).AddRow(false, false, uint64(0), uint64(0)))
	mock4.ExpectQuery("FROM system.replication_queue").WillReturnError(errors.New("queue missing"))
	replica, err := observer.collectReplica(t.Context(), db4, "db", "tbl", true)
	require.NoError(t, err)
	require.NotNil(t, replica)

	db5, mock5, err := sqlmock.New()
	require.NoError(t, err)
	defer db5.Close()
	mock5.ExpectQuery("FROM system.replicas").WillReturnRows(sqlmock.NewRows([]string{"is_readonly", "is_session_expired", "queue_size", "absolute_delay"}).AddRow(false, false, uint64(0), uint64(0)))
	mock5.ExpectQuery("FROM system.replication_queue").WillReturnRows(sqlmock.NewRows([]string{"parts_to_merge_csv"}).AddRow("p1").RowError(0, errors.New("queue row")))
	replica, err = observer.collectReplica(t.Context(), db5, "db", "tbl", true)
	require.NoError(t, err)
	require.Empty(t, replica.MergeQueue)

	db5scan, mock5scan, err := sqlmock.New()
	require.NoError(t, err)
	defer db5scan.Close()
	mock5scan.ExpectQuery("FROM system.replicas").WillReturnRows(sqlmock.NewRows([]string{"is_readonly", "is_session_expired", "queue_size", "absolute_delay"}).AddRow(false, false, uint64(0), uint64(0)))
	mock5scan.ExpectQuery("FROM system.replication_queue").WillReturnRows(sqlmock.NewRows([]string{"parts_to_merge_csv", "extra"}).AddRow("p1", "extra"))
	replica, err = observer.collectReplica(t.Context(), db5scan, "db", "tbl", true)
	require.NoError(t, err)
	require.Empty(t, replica.MergeQueue)

	db5b, mock5b, err := sqlmock.New()
	require.NoError(t, err)
	defer db5b.Close()
	mock5b.ExpectQuery("FROM system.replicas").WillReturnRows(sqlmock.NewRows([]string{"is_readonly", "is_session_expired", "queue_size", "absolute_delay"}).AddRow(false, false, uint64(0), uint64(0)))
	mock5b.ExpectQuery("FROM system.replication_queue").WillReturnRows(sqlmock.NewRows([]string{"parts_to_merge_csv"}).AddRow("p1,p2"))
	replica, err = observer.collectReplica(t.Context(), db5b, "db", "tbl", true)
	require.NoError(t, err)
	require.Equal(t, []string{"p1", "p2"}, replica.MergeQueue)

	db6, mock6, err := sqlmock.New()
	require.NoError(t, err)
	defer db6.Close()
	mock6.ExpectQuery("FROM system.mutations").WillReturnRows(sqlmock.NewRows([]string{"mutation_id", "command", "is_done", "parts_to_do", "parts_to_do_names_csv", "latest_failed_part", "latest_fail_reason"}).
		AddRow("m1", "DELETE", false, uint64(1), "part", "failed", "reason"))
	mutations, err := observer.collectMutations(t.Context(), db6, "db", "tbl")
	require.NoError(t, err)
	require.Equal(t, "m1", mutations[0].MutationID)

	db7, mock7, err := sqlmock.New()
	require.NoError(t, err)
	defer db7.Close()
	mock7.ExpectQuery("FROM system.mutations").WillReturnRows(sqlmock.NewRows([]string{"mutation_id", "command", "is_done", "parts_to_do", "parts_to_do_names_csv", "latest_failed_part", "latest_fail_reason"}).
		AddRow("m1", "DELETE", false, uint64(1), "part", "failed", "reason").RowError(0, errors.New("mutation row")))
	_, err = observer.collectMutations(t.Context(), db7, "db", "tbl")
	require.Error(t, err)

	db7scan, mock7scan, err := sqlmock.New()
	require.NoError(t, err)
	defer db7scan.Close()
	mock7scan.ExpectQuery("FROM system.mutations").WillReturnRows(sqlmock.NewRows([]string{"mutation_id", "command", "is_done", "parts_to_do", "parts_to_do_names_csv", "latest_failed_part", "latest_fail_reason"}).
		AddRow("m1", "DELETE", false, "bad", "part", "failed", "reason"))
	_, err = observer.collectMutations(t.Context(), db7scan, "db", "tbl")
	require.Error(t, err)

	db8, mock8, err := sqlmock.New()
	require.NoError(t, err)
	defer db8.Close()
	mock8.ExpectQuery("FROM system.parts").WillReturnRows(sqlmock.NewRows([]string{"partition", "partition_id", "name", "hash_of_all_files", "disk_name", "rows", "bytes_on_disk", "modification_time"}).
		AddRow("bad", "pid", "name", "hash", "default", uint64(1), uint64(1), time.Now()).RowError(0, errors.New("part row")))
	_, err = observer.collectPartitionRollup(t.Context(), db8, TableObservation{Database: "db", Table: "tbl", Layout: TableLayout{Basis: AgeBasisFrontier, FrontierDivisor: 100}})
	require.Error(t, err)

	db9, mock9, err := sqlmock.New()
	require.NoError(t, err)
	defer db9.Close()
	now := time.Now()
	mock9.ExpectQuery("FROM system.parts").WillReturnRows(sqlmock.NewRows([]string{"partition", "partition_id", "name", "hash_of_all_files", "disk_name", "rows", "bytes_on_disk", "modification_time"}).
		AddRow("1", "pid", "p1", "h1", "default", uint64(1), uint64(1), now).
		AddRow("1", "pid", "p2", "h2", "default", uint64(1), uint64(1), now))
	parts, err := observer.collectPartitionRollup(t.Context(), db9, TableObservation{Database: "db", Table: "tbl", Layout: TableLayout{Basis: AgeBasisFrontier, FrontierDivisor: 100}})
	require.NoError(t, err)
	require.Equal(t, uint64(2), parts[0].Disks[0].Parts)

	db10, mock10, err := sqlmock.New()
	require.NoError(t, err)
	defer db10.Close()
	mock10.ExpectQuery("FROM system.part_log").WillReturnRows(sqlmock.NewRows([]string{"partition_id", "latest_new_part", "latest_any", "min_event_time"}).
		AddRow("pid", now, now, now).RowError(0, errors.New("partlog row")))
	_, _, err = observer.collectPartLogEvidence(t.Context(), db10, "db", "tbl", 92)
	require.Error(t, err)

	db10scan, mock10scan, err := sqlmock.New()
	require.NoError(t, err)
	defer db10scan.Close()
	mock10scan.ExpectQuery("FROM system.part_log").WillReturnRows(sqlmock.NewRows([]string{"partition_id", "latest_new_part", "latest_any", "min_event_time"}).
		AddRow("pid", "bad", now, now))
	_, _, err = observer.collectPartLogEvidence(t.Context(), db10scan, "db", "tbl", 92)
	require.Error(t, err)

	db11, mock11, err := sqlmock.New()
	require.NoError(t, err)
	defer db11.Close()
	mock11.ExpectQuery("FROM system.parts").WillReturnRows(sqlmock.NewRows([]string{"partition", "partition_id", "name", "hash_of_all_files", "disk_name", "rows", "bytes_on_disk", "modification_time"}).
		AddRow("1", "pid", "p1", "h1", "default", "bad", uint64(1), now))
	_, err = observer.collectPartitionRollup(t.Context(), db11, TableObservation{Database: "db", Table: "tbl", Layout: TableLayout{Basis: AgeBasisFrontier, FrontierDivisor: 100}})
	require.Error(t, err)

	db12, mock12, err := sqlmock.New()
	require.NoError(t, err)
	defer db12.Close()
	since := time.Date(2026, 6, 1, 0, 0, 0, 123456000, time.UTC)
	mock12.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", "pid", dateTime64MicrosParam(since)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(uint64(3)))
	count, err := observer.CountInsertsSince(t.Context(), chclient.Client{DB: db12}, TableObservation{Database: "db", Table: "tbl"}, "pid", since)
	require.NoError(t, err)
	require.Equal(t, uint64(3), count)

	db13, mock13, err := sqlmock.New()
	require.NoError(t, err)
	defer db13.Close()
	mock13.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", "pid", dateTime64MicrosParam(since)).WillReturnError(errors.New("part_log down"))
	_, err = observer.CountInsertsSince(t.Context(), chclient.Client{DB: db13}, TableObservation{Database: "db", Table: "tbl"}, "pid", since)
	require.ErrorContains(t, err, "count fresh inserts")

	db14, mock14, err := sqlmock.New()
	require.NoError(t, err)
	defer db14.Close()
	flushObserver := NewSQLObserver(time.Second)
	mock14.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	client := chclient.Client{Node: chclient.Node{ID: "node-a"}, DB: db14}
	require.NoError(t, flushObserver.flushGuardLogs(t.Context(), client))
	require.NoError(t, flushObserver.flushGuardLogs(t.Context(), client))
	require.NoError(t, mock14.ExpectationsWereMet())

	db15, mock15, err := sqlmock.New()
	require.NoError(t, err)
	defer db15.Close()
	mock15.ExpectQuery("FROM system.merges").
		WillReturnRows(sqlmock.NewRows([]string{"partition_id"}).AddRow(nil))
	_, err = observer.collectRunningMerges(t.Context(), db15, "db", "tbl")
	require.Error(t, err)

	// Watches without settings are skipped, leaving nothing to seed — the
	// nil DB proves no query is issued.
	seeded, err := observer.SeedMovedBytesToday(t.Context(), chclient.Client{},
		[]EffectiveWatch{{Database: "db", Table: "skip"}})
	require.NoError(t, err)
	require.Zero(t, seeded)
}

func TestControllerEdgeCoverage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Interval = Duration{Duration: time.Hour}
	ctrl := New(nil, []chclient.Client{{Node: chclient.Node{ID: "n1"}}}, ControllerConfig{Tiering: cfg, Watches: []EffectiveWatch{{Database: "db", Table: "tbl"}}})
	c, ok := ctrl.(*controller)
	require.True(t, ok)
	require.NoError(t, c.Start(t.Context()))
	legCtx := c.legContext()
	require.NoError(t, c.Stop(t.Context()))
	// legContext must hand out the run context, which Stop cancels — the
	// Background fallback would never be cancelled.
	require.ErrorIs(t, legCtx.Err(), context.Canceled)
	require.NotNil(t, NewStore(0))

	settings := frontierSettings()
	store := NewStore(10)
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "tbl", Verdicts: []Verdict{{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p", Token: "tok", Decision: DecisionKeep}}})
	c = &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: cfg, Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: &settings}}},
		clients:  []chclient.Client{{Node: chclient.Node{ID: "n1"}}},
		observer: fakeTableObserver{err: errors.New("observe")},
		store:    store,
		executor: fakeActuator{},
	}
	_, err := c.Apply(t.Context(), "n1", "db", "tbl", "p", "tok")
	require.ErrorContains(t, err, "observe")

	c.observer = fakeTableObserver{table: TableObservation{Database: "db", Table: "tbl", Settings: settings, Partitions: []PartitionObservation{{PartitionID: "other"}}}}
	_, err = c.Apply(t.Context(), "n1", "db", "tbl", "p", "tok")
	require.ErrorContains(t, err, "not observed")

	young := frontierObservation(time.Now())
	young.Database = "db"
	young.Table = "tbl"
	young.Partitions = []PartitionObservation{{Partition: "('mainnet',5)", PartitionID: "p", GroupKey: "mainnet", AgeInteger: 5, ActiveParts: 1, MaxModificationTime: time.Now().Add(-2 * time.Hour), Disks: []DiskPart{{Disk: "default", Parts: 1}}}}
	c.observer = fakeTableObserver{table: young}
	// The fresh observation re-derives the verdict; a token from a stale plan
	// must fail the CAS before actionability is even considered.
	_, err = c.Apply(t.Context(), "n1", "db", "tbl", "p", "tok")
	require.ErrorIs(t, err, ErrStateTokenMismatch)
	// With the real current token the CAS passes and the keep decision is
	// rejected as not actionable.
	fresh := DecidePartition(young, young.Partitions[0], time.Now())
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "tbl", Verdicts: []Verdict{fresh}})
	_, err = c.Apply(t.Context(), "n1", "db", "tbl", "p", fresh.Token)
	require.ErrorContains(t, err, "not actionable")

	actionable := young
	actionable.Heads = map[string]int64{"mainnet": 1000}
	fresh = DecidePartition(actionable, actionable.Partitions[0], time.Now())
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "tbl", Verdicts: []Verdict{fresh}})
	c.observer = fakeTableObserver{table: actionable}
	c.inFlight = map[string]InFlightLeg{flightKey(fresh): {}}
	_, err = c.Apply(t.Context(), "n1", "db", "tbl", "p", fresh.Token)
	require.ErrorIs(t, err, ErrLegInFlight)

	c.observer = fakeTableObserver{err: errors.New("republish failed")}
	c.republishTable(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, EffectiveWatch{Database: "db", Table: "tbl"})
	require.Equal(t, "republish failed", c.store.Snapshot().Tables[0].LastError)

	c = &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10), inFlight: make(map[string]InFlightLeg)}
	v := Verdict{NodeID: "n", Database: "db", Table: "tbl", PartitionID: "p", BytesOnDisk: 1}
	require.True(t, c.tryStart(v))
	require.False(t, c.tryStart(v))
	require.False(t, c.breakerTripped(0, 0))

	c.inFlight = map[string]InFlightLeg{
		"late":  {PartitionID: "late", StartedAt: time.Date(2026, 6, 1, 0, 2, 0, 0, time.UTC)},
		"early": {PartitionID: "early", StartedAt: time.Date(2026, 6, 1, 0, 1, 0, 0, time.UTC)},
	}
	legs := c.InFlight()
	require.Equal(t, []string{"early", "late"}, []string{legs[0].PartitionID, legs[1].PartitionID})
}

func TestErrorHelperEdgeCoverage(t *testing.T) {
	t.Parallel()

	require.False(t, isContextCanceled(nil, errors.New("boom"))) //nolint:staticcheck // Intentionally covers the helper's nil-context guard.
}
