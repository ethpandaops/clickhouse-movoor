//nolint:modernize // Pointer helpers keep observer fixtures readable.
package tiering

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

func TestSQLObserverObserveTableSuccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	expectObserveSuccess(mock, now)

	observer := NewSQLObserver(time.Second)
	watch := EffectiveWatch{Database: "db", Table: "tbl", Settings: ptrSettings(frontierSettings())}
	obs, err := observer.ObserveTable(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db}, watch)
	require.NoError(t, err)
	require.Equal(t, "uuid", obs.UUID)
	require.True(t, obs.IsReplicated)
	require.Equal(t, now, obs.ObservedAt)
	require.True(t, obs.TargetDiskFound)
	require.Equal(t, "hot", obs.HotVolume)
	require.Equal(t, map[string]int64{"mainnet": 350}, obs.Heads)
	require.Len(t, obs.Partitions, 1)
	require.Equal(t, uint64(2), obs.Partitions[0].ActiveParts)
	require.Equal(t, []DiskPart{{Disk: "default", Parts: 1}, {Disk: "s3_cache", Parts: 1}}, obs.Partitions[0].Disks)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLObserverObserveTableMarksRunningMerges(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	expectObserveSuccess(mock, now, "pid")

	observer := NewSQLObserver(time.Second)
	watch := EffectiveWatch{Database: "db", Table: "tbl", Settings: ptrSettings(frontierSettings())}
	obs, err := observer.ObserveTable(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db}, watch)
	require.NoError(t, err)
	require.Len(t, obs.Partitions, 1)
	require.True(t, obs.Partitions[0].MergeInFlight)
	for _, condition := range obs.Conditions {
		require.NotEqual(t, "merges_unreadable", condition.Code)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

//nolint:funlen // Warning/error branches share the same observation setup.
func TestSQLObserverObserveWarningsAndErrors(t *testing.T) {
	settings := frontierSettings()
	observer := NewSQLObserver(0)
	_, err := observer.ObserveTable(t.Context(), chclient.Client{}, EffectiveWatch{Settings: nil})
	require.ErrorContains(t, err, "no tier settings")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("FROM system.tables").WillReturnError(errors.New("metadata failed"))
	_, err = observer.ObserveTable(t.Context(), chclient.Client{DB: db}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})
	require.ErrorContains(t, err, "metadata failed")
	require.NoError(t, mock.ExpectationsWereMet())

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectQuery("FROM system.tables").WillReturnRows(sqlmock.NewRows([]string{"uuid", "engine", "storage_policy", "partition_key", "is_replicated", "version", "server_time"}).AddRow("uuid", "ReplicatedMergeTree", "policy", "tuple()", uint8(1), "26.2.5.45", time.Now().UTC()))
	obs, err := observer.ObserveTable(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db2}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})
	require.NoError(t, err)
	require.Len(t, obs.Conditions, 1)
	require.Equal(t, ConditionSeverityCritical, obs.Conditions[0].Severity)

	dbUnsupported, mockUnsupported, err := sqlmock.New()
	require.NoError(t, err)
	defer dbUnsupported.Close()
	mockUnsupported.ExpectQuery("FROM system.tables").WillReturnRows(sqlmock.NewRows([]string{"uuid", "engine", "storage_policy", "partition_key", "is_replicated", "version", "server_time"}).AddRow("uuid", "Distributed", "policy", "(network_id, intDiv(block_number, 100))", uint8(0), "26.2.5.45", time.Now().UTC()))
	obs, err = observer.ObserveTable(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: dbUnsupported}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})
	require.NoError(t, err)
	require.Equal(t, "unsupported_engine", obs.Conditions[0].Code)
	require.NoError(t, mockUnsupported.ExpectationsWereMet())

	db3, mock3, err := sqlmock.New()
	require.NoError(t, err)
	defer db3.Close()
	expectMetadata(mock3, "ReplicatedMergeTree", "(network_id, intDiv(block_number, 100))", time.Now().UTC())
	mock3.ExpectQuery("FROM system.storage_policies").WillReturnError(errors.New("storage failed"))
	mock3.ExpectQuery("SELECT toString").WillReturnError(errors.New("heads failed"))
	mock3.ExpectQuery("FROM system.replicas").WillReturnError(errors.New("replica failed"))
	mock3.ExpectQuery("FROM system.mutations").WillReturnError(errors.New("mutations failed"))
	mock3.ExpectQuery("FROM system.part_log").WillReturnError(errors.New("partlog failed"))
	mock3.ExpectQuery("FROM system.parts").WillReturnError(errors.New("parts failed"))
	_, err = observer.ObserveTable(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db3}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &settings})
	require.ErrorContains(t, err, "parts failed")
	require.NoError(t, mock3.ExpectationsWereMet())

	timeSettings := DefaultTierSettings()
	timeSettings.Age = AgeSettings{Basis: AgeBasisPartitionTime, OlderThan: Duration{Duration: time.Hour}}
	db4, mock4, err := sqlmock.New()
	require.NoError(t, err)
	defer db4.Close()
	expectMetadata(mock4, "ReplicatedMergeTree", "toYYYYMM(ts)", time.Now().UTC())
	mock4.ExpectQuery("FROM system.columns").WithArgs("db", "tbl", "ts").WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow("DateTime('No/SuchZone')"))
	obs, err = observer.ObserveTable(t.Context(), chclient.Client{Node: chclient.Node{ID: "n1"}, DB: db4}, EffectiveWatch{Database: "db", Table: "tbl", Settings: &timeSettings})
	require.NoError(t, err)
	require.Equal(t, ConditionSeverityCritical, obs.Conditions[0].Severity)
	require.NoError(t, mock4.ExpectationsWereMet())
}

func TestSQLObserverStoragePolicyBranches(t *testing.T) {
	observer := NewSQLObserver(time.Second)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	obs := TableObservation{Settings: frontierSettings(), StoragePolicy: "policy", Node: chclient.Node{ID: "n1"}, Database: "db", Table: "tbl"}
	mock.ExpectQuery("FROM system.storage_policies").WillReturnRows(sqlmock.NewRows([]string{"policy_name", "volume_name", "volume_priority", "disks_csv", "move_factor"}))
	require.ErrorContains(t, observer.collectStoragePolicy(t.Context(), db, &obs), "was not observed")

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	obs = TableObservation{Settings: frontierSettings(), StoragePolicy: "policy", Node: chclient.Node{ID: "n1"}, Database: "db", Table: "tbl"}
	mock2.ExpectQuery("FROM system.storage_policies").WillReturnRows(sqlmock.NewRows([]string{"policy_name", "volume_name", "volume_priority", "disks_csv", "move_factor"}).AddRow("policy", "hot", uint64(1), "default", 0.0))
	require.NoError(t, observer.collectStoragePolicy(t.Context(), db2, &obs))
	require.False(t, obs.TargetDiskFound)
	require.Equal(t, "target_disk_missing", obs.Conditions[0].Code)

	db3, mock3, err := sqlmock.New()
	require.NoError(t, err)
	defer db3.Close()
	settings := frontierSettings()
	settings.HotVolume = "explicit"
	obs = TableObservation{Settings: settings, StoragePolicy: "policy", Node: chclient.Node{ID: "n1"}, Database: "db", Table: "tbl"}
	mock3.ExpectQuery("FROM system.storage_policies").WillReturnRows(sqlmock.NewRows([]string{"policy_name", "volume_name", "volume_priority", "disks_csv", "move_factor"}).AddRow("policy", "cold", uint64(1), "s3_cache", 0.0))
	require.NoError(t, observer.collectStoragePolicy(t.Context(), db3, &obs))
	require.Equal(t, "explicit", obs.HotVolume)
}

func TestSQLObserverRefreshPartition(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	table := TableObservation{Database: "db", Table: "tbl", Layout: TableLayout{Basis: AgeBasisFrontier, GroupColumns: []string{"network_id"}, FrontierDivisor: 100}}
	expectParts(mock, time.Now())
	observer := NewSQLObserver(time.Second)
	part, err := observer.RefreshPartition(t.Context(), chclient.Client{DB: db}, table, "pid")
	require.NoError(t, err)
	require.Equal(t, "pid", part.PartitionID)
	require.NoError(t, mock.ExpectationsWereMet())

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer db2.Close()
	mock2.ExpectQuery("FROM system.parts").WillReturnRows(sqlmock.NewRows([]string{"partition", "partition_id", "name", "hash_of_all_files", "disk_name", "rows", "bytes_on_disk", "modification_time"}))
	_, err = observer.RefreshPartition(t.Context(), chclient.Client{DB: db2}, table, "missing")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func expectObserveSuccess(mock sqlmock.Sqlmock, now time.Time, mergingPartitionIDs ...string) {
	expectMetadata(mock, "ReplicatedMergeTree", "(network_id, intDiv(block_number, 100))", now)
	mock.ExpectQuery("FROM system.storage_policies").WillReturnRows(sqlmock.NewRows([]string{"policy_name", "volume_name", "volume_priority", "disks_csv", "move_factor"}).
		AddRow("policy", "hot", uint64(1), "default", 1.0).
		AddRow("policy", "cold", uint64(2), "s3_cache", 0.0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT toString(`network_id`) AS group_key")).WillReturnRows(sqlmock.NewRows([]string{"group_key", "head"}).AddRow("mainnet", int64(350)))
	mock.ExpectQuery("FROM system.replicas").WillReturnRows(sqlmock.NewRows([]string{"is_readonly", "is_session_expired", "queue_size", "absolute_delay"}).AddRow(false, false, uint64(0), uint64(0)))
	mock.ExpectQuery("FROM system.replication_queue").WillReturnRows(sqlmock.NewRows([]string{"parts_to_merge_csv"}))
	mock.ExpectQuery("FROM system.mutations").WillReturnRows(sqlmock.NewRows([]string{"mutation_id", "command", "is_done", "parts_to_do", "parts_to_do_names_csv", "latest_failed_part", "latest_fail_reason"}))
	mock.ExpectQuery("FROM system.part_log").WillReturnRows(sqlmock.NewRows([]string{"partition_id", "latest_new_part", "latest_any", "min_event_time"}).AddRow("pid", now.Add(-time.Hour), now.Add(-time.Hour), now.Add(-2*time.Hour)))
	mergeRows := sqlmock.NewRows([]string{"partition_id"})
	for _, partitionID := range mergingPartitionIDs {
		mergeRows.AddRow(partitionID)
	}
	mock.ExpectQuery("FROM system.merges").WillReturnRows(mergeRows)
	expectParts(mock, now)
}

func expectMetadata(mock sqlmock.Sqlmock, engine string, partitionKey string, now time.Time) {
	mock.ExpectQuery("FROM system.tables").WillReturnRows(sqlmock.NewRows([]string{"uuid", "engine", "storage_policy", "partition_key", "is_replicated", "version", "server_time"}).AddRow("uuid", engine, "policy", partitionKey, uint8(1), "26.2.5.45", now))
}

func expectParts(mock sqlmock.Sqlmock, now time.Time) {
	mock.ExpectQuery("FROM system.parts").WillReturnRows(sqlmock.NewRows([]string{"partition", "partition_id", "name", "hash_of_all_files", "disk_name", "rows", "bytes_on_disk", "modification_time"}).
		AddRow("('mainnet',2)", "pid", "part-1", "hash-1", "default", uint64(10), uint64(100), now).
		AddRow("('mainnet',2)", "pid", "part-2", "hash-2", "s3_cache", uint64(20), uint64(200), now.Add(time.Second)))
}

func frontierSettings() TierSettings {
	settings := DefaultTierSettings()
	settings.Age = AgeSettings{Basis: AgeBasisFrontier, Field: "block_number", KeepLast: 100}
	return settings
}

func ptrSettings(settings TierSettings) *TierSettings {
	return &settings
}
