//nolint:modernize // Pointer helpers keep safety fixtures readable.
package tiering

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

type fakeSafetyObserver struct {
	table      TableObservation
	observeErr error
	boot       time.Time
	seed       uint64
	move       ForeignMoveObservation
	err        error
	seedErr    error
	bootErr    error
	identity   error
	probeErr   error
	sideCount  uint64
	sideErr    error
}

func (f fakeSafetyObserver) ObserveTable(context.Context, chclient.Client, EffectiveWatch) (TableObservation, error) {
	return f.table, f.observeErr
}

func (f fakeSafetyObserver) RefreshPartition(context.Context, chclient.Client, TableObservation, string) (PartitionObservation, error) {
	return PartitionObservation{}, sql.ErrNoRows
}

func (f fakeSafetyObserver) CaptureBootTime(context.Context, chclient.Client) (time.Time, error) {
	return f.boot, f.bootErr
}

func (f fakeSafetyObserver) SeedMovedBytesToday(context.Context, chclient.Client, []EffectiveWatch) (uint64, error) {
	return f.seed, f.seedErr
}

func (f fakeSafetyObserver) ObserveForeignMoves(context.Context, chclient.Client, TableObservation, string, time.Time) (ForeignMoveObservation, error) {
	return f.move, f.err
}

func (f fakeSafetyObserver) CheckTableIdentity(context.Context, chclient.Client, TableObservation) error {
	return f.identity
}

func (f fakeSafetyObserver) ProbeColdPartition(context.Context, chclient.Client, TableObservation, PartitionObservation) error {
	return f.probeErr
}

func (f fakeSafetyObserver) CountColdSideMerges(context.Context, chclient.Client, TableObservation, time.Time) (uint64, error) {
	return f.sideCount, f.sideErr
}

//nolint:funlen // SQL safety fixtures stay together so query/gate sequencing remains visible.
func TestSQLObserverSafetyQueries(t *testing.T) {
	observer := NewSQLObserver(time.Second)
	now := time.Now().UTC()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT now64").WillReturnRows(sqlmock.NewRows([]string{"now64"}).AddRow(now))
	gotBoot, err := observer.CaptureBootTime(t.Context(), chclient.Client{DB: db})
	require.NoError(t, err)
	require.Equal(t, now, gotBoot)
	require.NoError(t, mock.ExpectationsWereMet())

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(uint64(42)))
	bytes, err := observer.SeedMovedBytesToday(t.Context(), chclient.Client{DB: db2}, []EffectiveWatch{{Database: "db", Table: "tbl", Settings: ptrSettings(frontierSettings())}, {Database: "db", Table: "skip"}})
	require.NoError(t, err)
	require.Equal(t, uint64(42), bytes)
	require.NoError(t, mock2.ExpectationsWereMet())

	db3, mock3, err := sqlmock.New()
	require.NoError(t, err)
	defer db3.Close()
	mock3.ExpectQuery("SELECT uuid, partition_key").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"uuid", "partition_key"}).AddRow("uuid", "(network_id, intDiv(block_number, 100))"))
	table := TableObservation{Database: "db", Table: "tbl", UUID: "uuid", Settings: frontierSettings(), Layout: TableLayout{Generation: "(network_id, intDiv(block_number, 100))|frontier"}}
	require.NoError(t, observer.CheckTableIdentity(t.Context(), chclient.Client{DB: db3}, table))
	require.NoError(t, mock3.ExpectationsWereMet())

	db4, mock4, err := sqlmock.New()
	require.NoError(t, err)
	defer db4.Close()
	mock4.ExpectQuery("SELECT uuid, partition_key").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"uuid", "partition_key"}).AddRow("new", "(network_id, intDiv(block_number, 100))"))
	err = observer.CheckTableIdentity(t.Context(), chclient.Client{DB: db4}, table)
	require.ErrorContains(t, err, "uuid")
	require.NoError(t, mock4.ExpectationsWereMet())

	require.NoError(t, observer.CheckTableIdentity(t.Context(), chclient.Client{}, TableObservation{}))

	db5, mock5, err := sqlmock.New()
	require.NoError(t, err)
	defer db5.Close()
	mock5.ExpectQuery("FROM system.columns").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("payload"))
	mock5.ExpectQuery("SELECT toString").WithArgs("part-cold").WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow("value"))
	err = observer.ProbeColdPartition(t.Context(), chclient.Client{DB: db5}, TableObservation{Database: "db", Table: "tbl", Settings: frontierSettings()}, PartitionObservation{Hashes: []PartHash{{Name: "part-hot", Disk: "default"}, {Name: "part-cold", Disk: "s3_cache"}}})
	require.NoError(t, err)
	require.NoError(t, mock5.ExpectationsWereMet())

	dbProbeNoRows, mockProbeNoRows, err := sqlmock.New()
	require.NoError(t, err)
	defer dbProbeNoRows.Close()
	mockProbeNoRows.ExpectQuery("FROM system.columns").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("payload"))
	mockProbeNoRows.ExpectQuery("SELECT toString").WithArgs("empty-cold").WillReturnRows(sqlmock.NewRows([]string{"payload"}))
	mockProbeNoRows.ExpectQuery("SELECT toString").WithArgs("full-cold").WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow("value"))
	err = observer.ProbeColdPartition(t.Context(), chclient.Client{DB: dbProbeNoRows}, TableObservation{Database: "db", Table: "tbl", Settings: frontierSettings()}, PartitionObservation{Hashes: []PartHash{{Name: "empty-cold", Disk: "s3_cache"}, {Name: "full-cold", Disk: "s3_cache"}}})
	require.NoError(t, err)
	require.NoError(t, mockProbeNoRows.ExpectationsWereMet())

	dbProbeEmpty, mockProbeEmpty, err := sqlmock.New()
	require.NoError(t, err)
	defer dbProbeEmpty.Close()
	mockProbeEmpty.ExpectQuery("FROM system.columns").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("payload"))
	mockProbeEmpty.ExpectQuery("SELECT toString").WithArgs("empty-cold").WillReturnRows(sqlmock.NewRows([]string{"payload"}))
	err = observer.ProbeColdPartition(t.Context(), chclient.Client{DB: dbProbeEmpty}, TableObservation{Database: "db", Table: "tbl", Settings: frontierSettings()}, PartitionObservation{Hashes: []PartHash{{Name: "empty-cold", Disk: "s3_cache"}}})
	require.NoError(t, err)
	require.NoError(t, mockProbeEmpty.ExpectationsWereMet())

	err = observer.ProbeColdPartition(t.Context(), chclient.Client{}, TableObservation{Settings: frontierSettings()}, PartitionObservation{Hashes: []PartHash{{Name: "part-hot", Disk: "default"}}})
	require.NoError(t, err)

	db6, mock6, err := sqlmock.New()
	require.NoError(t, err)
	defer db6.Close()
	mock6.ExpectQuery("FROM system.mutations").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"mutation_id", "command", "is_done", "parts_to_do", "parts_to_do_names_csv", "latest_failed_part", "latest_fail_reason"}).
		AddRow("done-ish", "DELETE", false, uint64(0), "", "", ""))
	err = observer.CheckMutationsClear(t.Context(), chclient.Client{DB: db6}, TableObservation{Database: "db", Table: "tbl"})
	require.NoError(t, err)
	require.NoError(t, mock6.ExpectationsWereMet())

	db7, mock7, err := sqlmock.New()
	require.NoError(t, err)
	defer db7.Close()
	mock7.ExpectQuery("FROM system.mutations").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"mutation_id", "command", "is_done", "parts_to_do", "parts_to_do_names_csv", "latest_failed_part", "latest_fail_reason"}).
		AddRow("mut", "DELETE", false, uint64(1), "part", "part", "boom"))
	err = observer.CheckMutationsClear(t.Context(), chclient.Client{DB: db7}, TableObservation{Database: "db", Table: "tbl"})
	require.ErrorContains(t, err, "boom")
	require.NoError(t, mock7.ExpectationsWereMet())

	db8, mock8, err := sqlmock.New()
	require.NoError(t, err)
	defer db8.Close()
	mock8.ExpectQuery("FROM system.mutations").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"mutation_id", "command", "is_done", "parts_to_do", "parts_to_do_names_csv", "latest_failed_part", "latest_fail_reason"}).
		AddRow("mut-no-reason", "DELETE", false, uint64(1), "part", "part", ""))
	err = observer.CheckMutationsClear(t.Context(), chclient.Client{DB: db8}, TableObservation{Database: "db", Table: "tbl"})
	require.ErrorContains(t, err, "mut-no-reason")
	require.NoError(t, mock8.ExpectationsWereMet())

	db9, mock9, err := sqlmock.New()
	require.NoError(t, err)
	defer db9.Close()
	mock9.ExpectQuery("FROM system.mutations").WithArgs("db", "tbl").WillReturnError(errors.New("mutations unavailable"))
	err = observer.CheckMutationsClear(t.Context(), chclient.Client{DB: db9}, TableObservation{Database: "db", Table: "tbl"})
	require.ErrorContains(t, err, "mutations unavailable")
	require.NoError(t, mock9.ExpectationsWereMet())

	dbFree, mockFree, err := sqlmock.New()
	require.NoError(t, err)
	defer dbFree.Close()
	mockFree.ExpectQuery("FROM system.disks").WillReturnRows(sqlmock.NewRows([]string{"name", "free_space"}).AddRow("default", uint64(100)))
	tableFree := TableObservation{Database: "db", Table: "tbl", Settings: frontierSettings()}
	verdictFree := Verdict{Database: "db", Table: "tbl", PartitionID: "pid", ActiveParts: 2, BytesOnDisk: 40, Disks: []DiskPart{{Disk: "default", Parts: 1}, {Disk: "s3_cache", Parts: 1}}}
	err = observer.CheckOptimizeFreeSpace(t.Context(), chclient.Client{DB: dbFree}, tableFree, verdictFree)
	require.NoError(t, err)
	require.NoError(t, mockFree.ExpectationsWereMet())

	dbLow, mockLow, err := sqlmock.New()
	require.NoError(t, err)
	defer dbLow.Close()
	mockLow.ExpectQuery("FROM system.disks").WillReturnRows(sqlmock.NewRows([]string{"name", "free_space"}).AddRow("default", uint64(79)))
	err = observer.CheckOptimizeFreeSpace(t.Context(), chclient.Client{DB: dbLow}, tableFree, verdictFree)
	require.ErrorContains(t, err, "not enough")
	require.NoError(t, mockLow.ExpectationsWereMet())

	dbFreeErr, mockFreeErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbFreeErr.Close()
	mockFreeErr.ExpectQuery("FROM system.disks").WillReturnError(errors.New("disks unavailable"))
	err = observer.CheckOptimizeFreeSpace(t.Context(), chclient.Client{DB: dbFreeErr}, tableFree, verdictFree)
	require.ErrorContains(t, err, "disks unavailable")
	require.NoError(t, mockFreeErr.ExpectationsWereMet())

	// Optimize applicability gating lives in the classifier, not the space
	// pre-flight: shouldOptimize is what keeps these partitions off the
	// optimize leg entirely.
	noOptimize := frontierSettings()
	noOptimize.SkipOptimize = true
	require.False(t, shouldOptimize(noOptimize, PartitionObservation{ActiveParts: 2, BytesOnDisk: 40}))
	require.False(t, shouldOptimize(frontierSettings(), PartitionObservation{ActiveParts: 1, BytesOnDisk: 40}))
	huge := frontierSettings()
	huge.OptimizeSkipAboveBytes = Bytes{Value: 1}
	require.False(t, shouldOptimize(huge, PartitionObservation{ActiveParts: 2, BytesOnDisk: 40}))
	require.True(t, shouldOptimize(frontierSettings(), PartitionObservation{ActiveParts: 2, BytesOnDisk: 40}))
	require.Equal(t, ^uint64(0), saturatingDouble(^uint64(0)))
	require.Equal(t, uint64(10), saturatingDouble(5))

	dbRow, mockRow, err := sqlmock.New()
	require.NoError(t, err)
	defer dbRow.Close()
	mockRow.ExpectQuery("FROM system.disks").WillReturnRows(sqlmock.NewRows([]string{"name", "free_space"}).AddRow("default", "bad"))
	_, err = observer.collectDiskFreeSpace(t.Context(), dbRow)
	require.Error(t, err)
	require.NoError(t, mockRow.ExpectationsWereMet())

	dbSide, mockSide, err := sqlmock.New()
	require.NoError(t, err)
	defer dbSide.Close()
	since := time.Now().Add(-time.Hour)
	mockSide.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", "s3_cache", since).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(uint64(2)))
	count, err := observer.CountColdSideMerges(t.Context(), chclient.Client{DB: dbSide}, tableFree, since)
	require.NoError(t, err)
	require.Equal(t, uint64(2), count)
	require.NoError(t, mockSide.ExpectationsWereMet())

	dbSideErr, mockSideErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbSideErr.Close()
	mockSideErr.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", "s3_cache", since).WillReturnError(errors.New("partlog unavailable"))
	_, err = observer.CountColdSideMerges(t.Context(), chclient.Client{DB: dbSideErr}, tableFree, since)
	require.ErrorContains(t, err, "partlog unavailable")
	require.NoError(t, mockSideErr.ExpectationsWereMet())

	db10, mock10, err := sqlmock.New()
	require.NoError(t, err)
	defer db10.Close()
	layout := TableLayout{Database: "db", Table: "tbl", Basis: AgeBasisPartitionTime, AgeField: "ts"}
	mock10.ExpectQuery("FROM system.columns").WithArgs("db", "tbl", "ts").WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow("DateTime('Asia/Tokyo')"))
	err = observer.applyColumnTimezone(t.Context(), db10, &layout)
	require.NoError(t, err)
	require.Equal(t, "Asia/Tokyo", layout.TimeZone)
	require.NoError(t, mock10.ExpectationsWereMet())

	db11, mock11, err := sqlmock.New()
	require.NoError(t, err)
	defer db11.Close()
	layout = TableLayout{Database: "db", Table: "tbl", Basis: AgeBasisPartitionTime, AgeField: "ts"}
	mock11.ExpectQuery("FROM system.columns").WithArgs("db", "tbl", "ts").WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow("DateTime64(3, 'No/SuchZone')"))
	err = observer.applyColumnTimezone(t.Context(), db11, &layout)
	require.ErrorContains(t, err, "unsupported timezone")
	require.NoError(t, mock11.ExpectationsWereMet())

	db12, mock12, err := sqlmock.New()
	require.NoError(t, err)
	defer db12.Close()
	layout = TableLayout{Database: "db", Table: "tbl", Basis: AgeBasisPartitionTime, AgeField: "ts"}
	mock12.ExpectQuery("FROM system.columns").WithArgs("db", "tbl", "ts").WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow("DateTime"))
	err = observer.applyColumnTimezone(t.Context(), db12, &layout)
	require.NoError(t, err)
	require.Empty(t, layout.TimeZone)
	require.NoError(t, mock12.ExpectationsWereMet())

	layout = TableLayout{Basis: AgeBasisPartitionTime, AgeField: "ts", TimeZone: "UTC"}
	require.NoError(t, observer.applyColumnTimezone(t.Context(), nil, &layout))

	db13, mock13, err := sqlmock.New()
	require.NoError(t, err)
	defer db13.Close()
	layout = TableLayout{Database: "db", Table: "tbl", Basis: AgeBasisPartitionTime, AgeField: "ts"}
	mock13.ExpectQuery("FROM system.columns").WithArgs("db", "tbl", "ts").WillReturnError(errors.New("columns unavailable"))
	err = observer.applyColumnTimezone(t.Context(), db13, &layout)
	require.ErrorContains(t, err, "columns unavailable")
	require.NoError(t, mock13.ExpectationsWereMet())

	require.Equal(t, "UTC", parseColumnTimezone("DateTime64(3, 'UTC')"))
	require.Empty(t, parseColumnTimezone("DateTime"))
	require.Empty(t, parseColumnTimezone("DateTime('unterminated)"))
}

//nolint:funlen // Edge coverage stays compact so foreign-mover SQL fixtures remain visible together.
func TestSQLObserverForeignMoveGuardQueries(t *testing.T) {
	observer := NewSQLObserver(time.Second)
	table := TableObservation{Database: "db", Table: "tbl"}
	boot := time.Now()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}).
		AddRow("movoor-other-1", "movoor:other", "ALTER TABLE db.tbl MOVE PARTITION ID 'p' TO DISK 's3_cache'"))
	obs, err := NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db}, table, "mine", time.Now())
	require.NoError(t, err)
	require.True(t, obs.DuplicateInstance)
	require.NoError(t, mock.ExpectationsWereMet())

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock2.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}).
		AddRow("", "", "ALTER TABLE db.tbl MOVE PARTITION ID 'p' TO DISK 's3_cache'"))
	obs, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db2}, table, "mine", time.Now())
	require.NoError(t, err)
	require.True(t, obs.ForeignActivity)
	require.NoError(t, mock2.ExpectationsWereMet())

	db3, mock3, err := sqlmock.New()
	require.NoError(t, err)
	defer db3.Close()
	mock3.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock3.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}).
		AddRow("movoor-mine-1", "movoor:mine", "ALTER TABLE db.tbl MOVE PARTITION ID 'p' TO DISK 's3_cache'"))
	mock3.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock3.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name", "target_disk_name"}))
	mock3.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}))
	mock3.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", dateTime64MicrosParam(boot)).WillReturnRows(sqlmock.NewRows([]string{"partition_id", "part_name"}))
	obs, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db3}, table, "mine", boot)
	require.NoError(t, err)
	require.False(t, obs.DuplicateInstance)
	require.False(t, obs.ForeignActivity)
	require.NoError(t, mock3.ExpectationsWereMet())

	db4, mock4, err := sqlmock.New()
	require.NoError(t, err)
	defer db4.Close()
	mock4.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock4.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock4.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}).
		AddRow("movoor-other-1", "movoor:other", "ALTER TABLE db.tbl MOVE PARTITION ID 'p' TO DISK 's3_cache'"))
	obs, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db4}, table, "mine", boot)
	require.NoError(t, err)
	require.True(t, obs.DuplicateInstance)
	require.NoError(t, mock4.ExpectationsWereMet())

	db5, mock5, err := sqlmock.New()
	require.NoError(t, err)
	defer db5.Close()
	// Pre-boot statements (a dead predecessor's history, the fixture's own
	// seeding moves) are excluded by the query's boot-time filter — an empty
	// statement layer is a clean pass; only live system.moves evidence gates.
	mock5.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock5.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock5.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock5.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name", "target_disk_name"}))
	mock5.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}))
	mock5.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", dateTime64MicrosParam(boot)).WillReturnRows(sqlmock.NewRows([]string{"partition_id", "part_name"}))
	obs, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db5}, table, "mine", boot)
	require.NoError(t, err)
	require.False(t, obs.DuplicateInstance)
	require.False(t, obs.ForeignActivity)
	require.NoError(t, mock5.ExpectationsWereMet())

	db6, mock6, err := sqlmock.New()
	require.NoError(t, err)
	defer db6.Close()
	mock6.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock6.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock6.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock6.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name", "target_disk_name"}).AddRow("foreign-part", "s3_cache"))
	obs, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db6}, table, "mine", boot)
	require.NoError(t, err)
	require.True(t, obs.ForeignActivity)
	require.Contains(t, obs.Message, "active move")
	require.NoError(t, mock6.ExpectationsWereMet())

	db7, mock7, err := sqlmock.New()
	require.NoError(t, err)
	defer db7.Close()
	inFlightTable := table
	inFlightTable.InFlightPartNames = []string{"own-part"}
	mock7.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock7.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock7.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock7.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name", "target_disk_name"}).AddRow("own-part", "s3_cache"))
	mock7.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}))
	mock7.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", dateTime64MicrosParam(boot)).WillReturnRows(sqlmock.NewRows([]string{"partition_id", "part_name"}).AddRow("anon", "anon-part"))
	obs, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db7}, inFlightTable, "mine", boot)
	require.NoError(t, err)
	require.True(t, obs.ForeignActivity)
	require.Contains(t, obs.Message, "MovePart")
	require.NoError(t, mock7.ExpectationsWereMet())

	db8, mock8, err := sqlmock.New()
	require.NoError(t, err)
	defer db8.Close()
	mock8.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock8.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock8.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock8.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name", "target_disk_name"}))
	mock8.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}).AddRow("ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid''quoted' TO DISK 's3_cache'"))
	mock8.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", dateTime64MicrosParam(boot)).WillReturnRows(sqlmock.NewRows([]string{"partition_id", "part_name"}).AddRow("pid'quoted", "backed-part"))
	obs, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db8}, table, "mine", boot)
	require.NoError(t, err)
	require.False(t, obs.ForeignActivity)
	require.NoError(t, mock8.ExpectationsWereMet())

	require.Equal(t, map[string]struct{}{"pid'quoted": {}}, mustStatementPartitions(t, observer, "ALTER TABLE `db`.`tbl` MOVE PARTITION ID 'pid''quoted' TO DISK 's3_cache'", boot))
	gotID, ok := extractMovePartitionID("ALTER TABLE db.tbl MOVE PARTITION ID 'back\\slash' TO DISK 's3_cache'")
	require.True(t, ok)
	require.Equal(t, "backslash", gotID)
	_, ok = extractMovePartitionID("ALTER TABLE db.tbl MOVE PARTITION p")
	require.False(t, ok)
	_, ok = extractMovePartitionID("ALTER TABLE db.tbl MOVE PARTITION ID p")
	require.False(t, ok)
	_, ok = extractMovePartitionID("ALTER TABLE db.tbl MOVE PARTITION ID 'unterminated")
	require.False(t, ok)
	_, err = readSQLString("'unterminated")
	require.Error(t, err)
	_, err = readSQLString("'dangling\\")
	require.Error(t, err)
}

func mustStatementPartitions(t *testing.T, observer *SQLObserver, query string, boot time.Time) map[string]struct{} {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}).AddRow(query))
	partitions, err := observer.collectRecentMoveStatementPartitions(t.Context(), db, TableObservation{Table: "tbl"}, boot)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	return partitions
}

//nolint:funlen // Error branches are kept together to preserve the guard query sequence.
func TestSQLObserverMoveGuardHelperErrors(t *testing.T) {
	observer := NewSQLObserver(time.Second)
	table := TableObservation{Database: "db", Table: "tbl"}
	boot := time.Now()

	dbActiveErr, mockActiveErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbActiveErr.Close()
	mockActiveErr.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnError(errors.New("moves unavailable"))
	_, err = observer.collectActiveMoveEvents(t.Context(), dbActiveErr, table)
	require.ErrorContains(t, err, "moves unavailable")
	require.NoError(t, mockActiveErr.ExpectationsWereMet())

	dbActiveScan, mockActiveScan, err := sqlmock.New()
	require.NoError(t, err)
	defer dbActiveScan.Close()
	mockActiveScan.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name"}).AddRow("part"))
	_, err = observer.collectActiveMoveEvents(t.Context(), dbActiveScan, table)
	require.Error(t, err)
	require.NoError(t, mockActiveScan.ExpectationsWereMet())

	dbActiveRows, mockActiveRows, err := sqlmock.New()
	require.NoError(t, err)
	defer dbActiveRows.Close()
	mockActiveRows.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name", "target_disk_name"}).AddRow("part", "s3_cache").RowError(0, errors.New("row failed")))
	_, err = observer.collectActiveMoveEvents(t.Context(), dbActiveRows, table)
	require.ErrorContains(t, err, "row failed")
	require.NoError(t, mockActiveRows.ExpectationsWereMet())

	dbStmtErr, mockStmtErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbStmtErr.Close()
	mockStmtErr.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnError(errors.New("query log unavailable"))
	_, err = observer.collectRecentMoveStatementPartitions(t.Context(), dbStmtErr, table, boot)
	require.ErrorContains(t, err, "query log unavailable")
	require.NoError(t, mockStmtErr.ExpectationsWereMet())

	dbStmtScan, mockStmtScan, err := sqlmock.New()
	require.NoError(t, err)
	defer dbStmtScan.Close()
	mockStmtScan.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query", "extra"}).AddRow("query", "extra"))
	_, err = observer.collectRecentMoveStatementPartitions(t.Context(), dbStmtScan, table, boot)
	require.Error(t, err)
	require.NoError(t, mockStmtScan.ExpectationsWereMet())

	dbStmtRows, mockStmtRows, err := sqlmock.New()
	require.NoError(t, err)
	defer dbStmtRows.Close()
	mockStmtRows.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}).AddRow("query").RowError(0, errors.New("statement row failed")))
	_, err = observer.collectRecentMoveStatementPartitions(t.Context(), dbStmtRows, table, boot)
	require.ErrorContains(t, err, "statement row failed")
	require.NoError(t, mockStmtRows.ExpectationsWereMet())

	dbAnonStmtErr, mockAnonStmtErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbAnonStmtErr.Close()
	mockAnonStmtErr.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnError(errors.New("partition statements failed"))
	_, err = observer.collectAnonymousMovePartEvents(t.Context(), dbAnonStmtErr, table, boot)
	require.ErrorContains(t, err, "partition statements failed")
	require.NoError(t, mockAnonStmtErr.ExpectationsWereMet())

	dbAnonQueryErr, mockAnonQueryErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbAnonQueryErr.Close()
	mockAnonQueryErr.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}))
	mockAnonQueryErr.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", dateTime64MicrosParam(boot)).WillReturnError(errors.New("part log unavailable"))
	_, err = observer.collectAnonymousMovePartEvents(t.Context(), dbAnonQueryErr, table, boot)
	require.ErrorContains(t, err, "part log unavailable")
	require.NoError(t, mockAnonQueryErr.ExpectationsWereMet())

	dbAnonScan, mockAnonScan, err := sqlmock.New()
	require.NoError(t, err)
	defer dbAnonScan.Close()
	mockAnonScan.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}))
	mockAnonScan.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", dateTime64MicrosParam(boot)).WillReturnRows(sqlmock.NewRows([]string{"partition_id"}).AddRow("pid"))
	_, err = observer.collectAnonymousMovePartEvents(t.Context(), dbAnonScan, table, boot)
	require.Error(t, err)
	require.NoError(t, mockAnonScan.ExpectationsWereMet())

	dbAnonRows, mockAnonRows, err := sqlmock.New()
	require.NoError(t, err)
	defer dbAnonRows.Close()
	mockAnonRows.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query"}))
	mockAnonRows.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl", dateTime64MicrosParam(boot)).WillReturnRows(sqlmock.NewRows([]string{"partition_id", "part_name"}).AddRow("pid", "part").RowError(0, errors.New("part row failed")))
	_, err = observer.collectAnonymousMovePartEvents(t.Context(), dbAnonRows, table, boot)
	require.ErrorContains(t, err, "part row failed")
	require.NoError(t, mockAnonRows.ExpectationsWereMet())

	dbGuardActiveErr, mockGuardActiveErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbGuardActiveErr.Close()
	mockGuardActiveErr.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mockGuardActiveErr.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mockGuardActiveErr.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mockGuardActiveErr.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnError(errors.New("moves failed"))
	_, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: dbGuardActiveErr}, table, "mine", boot)
	require.ErrorContains(t, err, "moves failed")
	require.NoError(t, mockGuardActiveErr.ExpectationsWereMet())

	dbGuardAnonErr, mockGuardAnonErr, err := sqlmock.New()
	require.NoError(t, err)
	defer dbGuardAnonErr.Close()
	mockGuardAnonErr.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mockGuardAnonErr.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mockGuardAnonErr.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mockGuardAnonErr.ExpectQuery("FROM system.moves").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"part_name", "target_disk_name"}))
	mockGuardAnonErr.ExpectQuery("FROM system.query_log").WithArgs(dateTime64MicrosParam(boot), "tbl").WillReturnError(errors.New("anonymous failed"))
	_, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: dbGuardAnonErr}, table, "mine", boot)
	require.ErrorContains(t, err, "anonymous failed")
	require.NoError(t, mockGuardAnonErr.ExpectationsWereMet())
}

//nolint:funlen // Edge coverage stays compact so observer safety fixtures remain visible together.
func TestSQLObserverSafetyErrorBranches(t *testing.T) {
	observer := NewSQLObserver(time.Second)
	table := TableObservation{Database: "db", Table: "tbl", Settings: frontierSettings(), Layout: TableLayout{Generation: "old|frontier"}}

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT now64").WillReturnError(errors.New("now failed"))
	_, err = observer.CaptureBootTime(t.Context(), chclient.Client{DB: db})
	require.ErrorContains(t, err, "now failed")

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectQuery("FROM system.part_log").WithArgs("db", "tbl").WillReturnError(errors.New("part_log failed"))
	_, err = observer.SeedMovedBytesToday(t.Context(), chclient.Client{DB: db2}, []EffectiveWatch{{Database: "db", Table: "tbl", Settings: ptrSettings(frontierSettings())}})
	require.ErrorContains(t, err, "part_log failed")

	db3, mock3, err := sqlmock.New()
	require.NoError(t, err)
	defer db3.Close()
	mock3.ExpectExec("SYSTEM FLUSH LOGS").WillReturnError(errors.New("flush failed"))
	_, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db3}, table, "mine", time.Now())
	require.ErrorContains(t, err, "flush failed")

	db4, mock4, err := sqlmock.New()
	require.NoError(t, err)
	defer db4.Close()
	mock4.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock4.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnError(errors.New("processes failed"))
	_, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db4}, table, "mine", time.Now())
	require.ErrorContains(t, err, "processes failed")

	db5, mock5, err := sqlmock.New()
	require.NoError(t, err)
	defer db5.Close()
	mock5.ExpectExec("SYSTEM FLUSH LOGS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock5.ExpectQuery("FROM system.processes").WithArgs("tbl").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}))
	mock5.ExpectQuery("FROM system.query_log").WithArgs(sqlmock.AnyArg(), "tbl").WillReturnError(errors.New("query_log failed"))
	_, err = NewSQLObserver(time.Second).ObserveForeignMoves(t.Context(), chclient.Client{DB: db5}, table, "mine", time.Now())
	require.ErrorContains(t, err, "query_log failed")

	db6, mock6, err := sqlmock.New()
	require.NoError(t, err)
	defer db6.Close()
	mock6.ExpectQuery("SELECT uuid, partition_key").WithArgs("db", "tbl").WillReturnError(errors.New("identity failed"))
	err = observer.CheckTableIdentity(t.Context(), chclient.Client{DB: db6}, TableObservation{Database: "db", Table: "tbl", UUID: "uuid"})
	require.ErrorContains(t, err, "identity failed")

	db7, mock7, err := sqlmock.New()
	require.NoError(t, err)
	defer db7.Close()
	mock7.ExpectQuery("SELECT uuid, partition_key").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"uuid", "partition_key"}).AddRow("", "new"))
	err = observer.CheckTableIdentity(t.Context(), chclient.Client{DB: db7}, table)
	require.ErrorContains(t, err, "layout changed")

	db8, mock8, err := sqlmock.New()
	require.NoError(t, err)
	defer db8.Close()
	mock8.ExpectQuery("FROM system.columns").WithArgs("db", "tbl").WillReturnError(errors.New("columns failed"))
	err = observer.ProbeColdPartition(t.Context(), chclient.Client{DB: db8}, table, PartitionObservation{Hashes: []PartHash{{Name: "cold", Disk: "s3_cache"}}})
	require.ErrorContains(t, err, "columns failed")

	db9, mock9, err := sqlmock.New()
	require.NoError(t, err)
	defer db9.Close()
	mock9.ExpectQuery("FROM system.columns").WithArgs("db", "tbl").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("payload"))
	mock9.ExpectQuery("SELECT toString").WithArgs("cold").WillReturnError(errors.New("s3 missing"))
	err = observer.ProbeColdPartition(t.Context(), chclient.Client{DB: db9}, table, PartitionObservation{Hashes: []PartHash{{Name: "cold", Disk: "s3_cache"}}})
	require.ErrorContains(t, err, "s3 missing")

	db10, mock10, err := sqlmock.New()
	require.NoError(t, err)
	defer db10.Close()
	mock10.ExpectQuery("FROM system.parts").WillReturnError(errors.New("parts failed"))
	_, err = observer.RefreshPartition(t.Context(), chclient.Client{DB: db10}, table, "pid")
	require.ErrorContains(t, err, "parts failed")
}

func TestMoveStatementRowClassifierBranches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT live").WillReturnRows(sqlmock.NewRows([]string{"query_id"}).AddRow("bad"))
	rows, err := db.QueryContext(t.Context(), "SELECT live")
	require.NoError(t, err)
	_, err = classifyMoveStatementRows(rows, "mine")
	require.Error(t, err)
	require.NoError(t, rows.Close())

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectQuery("SELECT live").WillReturnRows(sqlmock.NewRows([]string{"query_id", "log_comment", "query"}).
		AddRow("movoor-mine", "movoor:mine", "ALTER").RowError(0, errors.New("row failed")))
	rows, err = db2.QueryContext(t.Context(), "SELECT live")
	require.NoError(t, err)
	_, err = classifyMoveStatementRows(rows, "mine")
	require.ErrorContains(t, err, "row failed")
	require.NoError(t, rows.Close())
}

func TestVersionGateBranches(t *testing.T) {
	obs := TableObservation{Version: "26.2.5.45", EffectiveMode: ModeEnforce}
	applyVersionGate(&obs)
	require.Empty(t, obs.Conditions)
	require.Equal(t, ModeEnforce, obs.EffectiveMode)

	obs = TableObservation{Version: "26.3.1", EffectiveMode: ModeEnforce}
	applyVersionGate(&obs)
	require.Equal(t, "clickhouse_minor_unvalidated", obs.Conditions[0].Code)
	require.Equal(t, ModeEnforce, obs.EffectiveMode)

	obs = TableObservation{Version: "27.1.1", EffectiveMode: ModeEnforce}
	applyVersionGate(&obs)
	require.Equal(t, "clickhouse_major_unvalidated", obs.Conditions[0].Code)
	require.Equal(t, ModePlan, obs.EffectiveMode)

	obs = TableObservation{Version: "broken", EffectiveMode: ModeEnforce}
	applyVersionGate(&obs)
	require.Equal(t, "clickhouse_version_unknown", obs.Conditions[0].Code)

	major, minor, ok := clickHouseMajorMinor("26.2.5")
	require.True(t, ok)
	require.Equal(t, 26, major)
	require.Equal(t, 2, minor)
	_, _, ok = clickHouseMajorMinor("26.nope")
	require.False(t, ok)
	_, _, ok = clickHouseMajorMinor("bad.2")
	require.False(t, ok)
}

func TestExecutorStatementContextAndIdentityGuard(t *testing.T) {
	exec := NewExecutor(slog.New(slog.DiscardHandler), nil, fakeSafetyObserver{identity: errors.New("identity changed")}, "instance")
	ctx := exec.statementContext(t.Context(), "attempt/id", "move 1", nil)
	require.NotNil(t, ctx)

	err := exec.moveToCold(t.Context(), chclient.Client{}, executorTable(true), executorVerdict(DecisionTier, 1, 1), "attempt")
	require.ErrorContains(t, err, "identity changed")
	require.Equal(t, "unknown", exec.sanitizeQueryID(""))
}

func TestControllerSeedAndForeignGuard(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	cfg := DefaultConfig()
	boot := time.Now().Add(-time.Minute)
	c := &controller{
		log:       slog.New(slog.DiscardHandler),
		cfg:       ControllerConfig{Tiering: cfg, InstanceID: "mine", Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: ptrSettings(frontierSettings())}}},
		observer:  fakeSafetyObserver{boot: boot, seed: 123},
		store:     NewStore(10),
		inFlight:  make(map[string]InFlightLeg),
		bootTimes: make(map[string]time.Time),
	}
	c.seedNode(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db})
	require.Equal(t, boot, c.bootTimes["n1"])
	require.Equal(t, uint64(123), c.store.Status().BytesMovedToday)
	require.Equal(t, "mine", c.instanceID())

	table := TableObservation{Database: "db", Table: "tbl"}
	c.observer = fakeSafetyObserver{move: ForeignMoveObservation{DuplicateInstance: true, Message: "dup"}}
	require.False(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, table))
	require.Equal(t, PauseReasonDuplicateInstance, c.store.Status().PauseReason)

	c.store.Resume()
	c.observer = fakeSafetyObserver{move: ForeignMoveObservation{ForeignActivity: true, Message: "manual"}}
	require.False(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, table))
	require.Equal(t, PauseReasonForeignMover, c.store.Status().PauseReason)

	c.observer = fakeSafetyObserver{}
	require.True(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, table))
	require.True(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, table))
	require.Equal(t, PauseRunning, c.store.Status().PauseState)

	c.observer = fakeSafetyObserver{err: errors.New("guard failed")}
	require.False(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, table))
	require.Equal(t, PauseReasonForeignMover, c.store.Status().PauseReason)

	c.store.Resume()
	c.observer = fakeSafetyObserver{}
	c.foreignClean = map[string]int{"n1/db/tbl": 3}
	require.True(t, c.guardForeignMovers(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, table))
	require.Zero(t, c.foreignClean["n1/db/tbl"])

	c.instrumenter = noopInstrumenter{}
	c.observer = fakeSafetyObserver{probeErr: errors.New("missing object")}
	conditions := c.maybeProbeColdPartition(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, TableObservation{
		Node:     chclient.Node{ID: "n1"},
		Database: "db",
		Table:    "tbl",
		Settings: frontierSettings(),
		Partitions: []PartitionObservation{{
			Partition:   "('mainnet',0)",
			PartitionID: "pid",
			Disks:       []DiskPart{{Disk: "s3_cache", Parts: 1}},
			Hashes:      []PartHash{{Name: "part-cold", Disk: "s3_cache"}},
		}},
	})
	require.Len(t, conditions, 1)
	require.Equal(t, "cold_read_probe_failed", conditions[0].Code)
}

//nolint:funlen // Edge coverage stays compact so controller safety fixtures remain visible together.
func TestControllerSafetyEdgeBranches(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	cfg := DefaultConfig()
	settings := frontierSettings()

	c := &controller{
		log:       slog.New(slog.DiscardHandler),
		cfg:       ControllerConfig{Tiering: cfg, Watches: []EffectiveWatch{{Database: "db", Table: "tbl", Settings: &settings}}},
		observer:  fakeSafetyObserver{bootErr: errors.New("boot failed"), seedErr: errors.New("seed failed")},
		store:     NewStore(10),
		bootTimes: make(map[string]time.Time),
	}
	c.seedNode(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db})
	require.NotZero(t, c.bootTimes["n1"])

	c.observer = fakeTableObserver{}
	c.seedNode(t.Context(), chclient.Client{Node: chclient.Node{ID: "n2"}, DB: db})
	require.NotZero(t, c.bootTimes["n2"])

	cfg.MaxConcurrentPartitions = 2
	cfg.Safety.MaxBytesInFlight = Bytes{Value: 100}
	cfg.Safety.MaxBytesPerDay = Bytes{Value: 10}
	c = &controller{cfg: ControllerConfig{Tiering: cfg}, store: NewStore(10), inFlight: make(map[string]InFlightLeg), bytesMovedToday: map[string]uint64{"n1": 9}, budgetDay: map[string]string{"n1": time.Now().UTC().Format("2006-01-02")}}
	require.False(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "budget", BytesOnDisk: 2}))
	// Same over-budget tally, but stamped to a prior day: the daily budget rolls
	// over and the dispatch is admitted (maxBytesPerDay is a daily, not lifetime,
	// budget).
	c.budgetDay["n1"] = "2000-01-01"
	require.True(t, c.tryStart(Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "rollover", BytesOnDisk: 2}))
	require.Zero(t, c.bytesMovedToday["n1"])
	require.Equal(t, time.Now().UTC().Format("2006-01-02"), c.budgetDay["n1"])
	c.bytesMovedToday["n1"] = 0
	v := Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "dup", BytesOnDisk: 1}
	c.inFlight[flightKey(v)] = InFlightLeg{}
	require.False(t, c.tryStart(v))

	cfg.Mode = ModeEnforce
	cfg.Safety.MaxMovesPerCycle = 2
	cfg.Safety.MaxBytesPerDay = Bytes{Value: 100}
	c = &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: cfg},
		store:    NewStore(10),
		observer: fakeSafetyObserver{err: errors.New("guard failed")},
		inFlight: make(map[string]InFlightLeg),
		executor: fakeActuator{calls: make(chan Verdict, 1)},
	}
	c.dispatch(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, TableObservation{EffectiveMode: ModeEnforce, Database: "db", Table: "tbl"}, []Verdict{{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p", Decision: DecisionTier, BytesOnDisk: 1}})
	require.Equal(t, PauseReasonForeignMover, c.store.Status().PauseReason)

	cfg.MaxConcurrentPartitions = 0
	cfg.Safety.DiffBreaker.MaxPartitions = 10
	cfg.Safety.DiffBreaker.MaxTableFraction = 1
	c = &controller{
		log:      slog.New(slog.DiscardHandler),
		cfg:      ControllerConfig{Tiering: cfg},
		store:    NewStore(10),
		observer: fakeTableObserver{},
		inFlight: make(map[string]InFlightLeg),
		executor: fakeActuator{calls: make(chan Verdict, 1)},
	}
	c.store.SetStatus(StatusSnapshot{PauseState: PauseRunning})
	c.dispatch(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, TableObservation{EffectiveMode: ModeEnforce, Database: "db", Table: "tbl"}, []Verdict{{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "p", Decision: DecisionTier, BytesOnDisk: 1}})

	cfg.Interval = Duration{}
	obs := frontierObservation(time.Now())
	c = &controller{log: slog.New(slog.DiscardHandler), cfg: ControllerConfig{Tiering: cfg}, observer: fakeSafetyObserver{table: obs}, store: NewStore(10), inFlight: make(map[string]InFlightLeg)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.reconcileLoop(ctx, chclient.Client{Node: chclient.Node{ID: "n1"}}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})

	probeObs := frontierObservation(time.Now())
	probeObs.Database = "db"
	probeObs.Table = "tbl"
	probeObs.Partitions = []PartitionObservation{{
		Partition:   "('mainnet',0)",
		PartitionID: "cold",
		GroupKey:    "mainnet",
		AgeInteger:  0,
		ActiveParts: 1,
		Disks:       []DiskPart{{Disk: "s3_cache", Parts: 1}},
		Hashes:      []PartHash{{Name: "cold-part", Disk: "s3_cache"}},
	}}
	c = &controller{
		log:          slog.New(slog.DiscardHandler),
		cfg:          ControllerConfig{Tiering: cfg},
		observer:     fakeSafetyObserver{table: probeObs, probeErr: errors.New("probe failed")},
		store:        NewStore(10),
		inFlight:     make(map[string]InFlightLeg),
		probeLast:    make(map[string]time.Time),
		instrumenter: noopInstrumenter{},
	}
	c.reconcileOnce(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})
	require.Equal(t, "cold_read_probe_failed", c.store.Snapshot().Tables[0].Conditions[0].Code)

	c = &controller{observer: fakeTableObserver{}, probeLast: make(map[string]time.Time)}
	require.Nil(t, c.maybeProbeColdPartition(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, TableObservation{}))

	c = &controller{observer: fakeSafetyObserver{}, probeLast: map[string]time.Time{"n1/db/tbl": time.Now()}}
	require.Nil(t, c.maybeProbeColdPartition(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, TableObservation{Database: "db", Table: "tbl"}))

	c = &controller{observer: fakeSafetyObserver{}, probeLast: make(map[string]time.Time)}
	hotOnly := TableObservation{
		Database: "db",
		Table:    "tbl",
		Settings: settings,
		Partitions: []PartitionObservation{{
			Disks: []DiskPart{{Disk: "default", Parts: 1}},
		}},
	}
	require.Nil(t, c.maybeProbeColdPartition(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, hotOnly))

	c = &controller{observer: fakeSafetyObserver{}, probeLast: make(map[string]time.Time)}
	coldOnly := TableObservation{
		Database: "db",
		Table:    "tbl",
		Settings: settings,
		Partitions: []PartitionObservation{{
			Disks:  []DiskPart{{Disk: "s3_cache", Parts: 1}},
			Hashes: []PartHash{{Name: "cold", Disk: "s3_cache"}},
		}},
	}
	require.Nil(t, c.maybeProbeColdPartition(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, coldOnly))
	require.Zero(t, probeCandidateIndex("key", time.Now(), 1))
	require.Less(t, probeCandidateIndex("key", time.Now(), 3), 3)

	flightTable := TableObservation{
		Node:     chclient.Node{ID: "n1"},
		Database: "db",
		Table:    "tbl",
		Partitions: []PartitionObservation{
			{PartitionID: "skip", Hashes: []PartHash{{Name: "skip-part"}}},
			{PartitionID: "pid", Hashes: []PartHash{{Name: "part-a"}, {Name: "part-b"}}},
		},
	}
	flightVerdict := Verdict{NodeID: "n1", Database: "db", Table: "tbl", PartitionID: "pid"}
	c = &controller{inFlight: map[string]InFlightLeg{flightKey(flightVerdict): {}}}
	require.Equal(t, []string{"part-a", "part-b"}, c.inFlightPartNames(flightTable))

	instrumenter := &countingInstrumenter{}
	sideTable := TableObservation{Node: chclient.Node{ID: "n1"}, Database: "db", Table: "tbl", Settings: settings, ObservedAt: time.Now()}
	c = &controller{
		log:           slog.New(slog.DiscardHandler),
		observer:      fakeSafetyObserver{sideCount: 2},
		instrumenter:  instrumenter,
		sideMergeLast: make(map[string]time.Time),
	}
	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, sideTable)
	require.Zero(t, instrumenter.sideMerges)
	sideTable.ObservedAt = sideTable.ObservedAt.Add(time.Minute)
	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, sideTable)
	require.Equal(t, uint64(2), instrumenter.sideMerges)

	c.observer = fakeSafetyObserver{sideErr: errors.New("side merge query failed")}
	c.recordColdSideMerges(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}}, sideTable)
	(&controller{observer: fakeTableObserver{}}).recordColdSideMerges(t.Context(), chclient.Client{}, sideTable)
	(&controller{observer: fakeSafetyObserver{}}).recordColdSideMerges(t.Context(), chclient.Client{}, sideTable)

	require.Equal(t, "default", (&controller{}).instanceID())
	breakerCfg := DefaultConfig()
	breakerCfg.Safety.DiffBreaker.MaxTableFraction = 0.5
	require.True(t, (&controller{cfg: ControllerConfig{Tiering: breakerCfg}}).breakerTripped(2, 3))
}
