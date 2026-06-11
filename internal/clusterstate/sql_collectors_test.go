package clusterstate

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

var sqlTestWatch = Watch{Database: "db", Table: "tbl"}

func TestCollectNodeSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.one")).
		WillReturnRows(sqlmock.NewRows([]string{"version", "timezone", "uptime"}).
			AddRow("26.2.5.45", "UTC", int64(123)))

	result := collector.collectNode(t.Context(), client)

	require.Nil(t, result.warning)
	require.True(t, result.item.Reachable)
	require.Equal(t, client.Node, result.item.Node)
	require.Equal(t, "26.2.5.45", result.item.Version)
	require.Equal(t, "UTC", result.item.Timezone)
	require.Equal(t, uint64(123), result.item.UptimeSeconds)
}

func TestCollectNodeSQLWarning(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.one")).
		WillReturnError(errors.New("connection refused"))

	result := collector.collectNode(t.Context(), client)

	require.NotNil(t, result.warning)
	require.False(t, result.item.Reachable)
	require.Equal(t, "connection refused", result.item.LastError)
	require.Equal(t, warningKindReachability, result.warning.Kind)
	require.Equal(t, "node_unreachable", result.warning.Code)
	require.Equal(t, client.Node.ID, result.warning.NodeID)
}

func TestCollectWatchValidationItemsSQL(t *testing.T) {
	t.Parallel()

	watches := []Watch{
		sqlTestWatch,
		{Database: "db", Table: "missing"},
	}
	collector, client, mock := mockCollectorClient(t, watches...)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("ReplicatedMergeTree"))
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "missing").
		WillReturnError(sql.ErrNoRows)

	items, err := collector.collectWatchValidationItems(t.Context(), client)

	require.NoError(t, err)
	require.Equal(t, []WatchValidationItem{
		{Node: client.Node, Watch: watches[0], Engine: "ReplicatedMergeTree", Found: true},
		{Node: client.Node, Watch: watches[1]},
	}, items)
}

func TestCollectWatchedPartBytesByDiskSQL(t *testing.T) {
	t.Parallel()

	watches := []Watch{
		sqlTestWatch,
		{Database: "db", Table: "tbl2"},
	}
	collector, client, mock := mockCollectorClient(t, watches...)
	mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"disk_name", "sum"}).
			AddRow("default", int64(10)).
			AddRow("s3_cache", int64(5)))
	mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
		WithArgs("db", "tbl2").
		WillReturnRows(sqlmock.NewRows([]string{"disk_name", "sum"}).
			AddRow("default", int64(7)))

	used, err := collector.collectWatchedPartBytesByDisk(t.Context(), client)

	require.NoError(t, err)
	require.Equal(t, map[string]uint64{"default": 17, "s3_cache": 5}, used)
}

func TestCollectDisksSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	maxUint := strconv.FormatUint(math.MaxUint64, 10)
	mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"disk_name", "sum"}).
			AddRow("default", int64(33)))
	mock.ExpectQuery(queryContaining("FROM system.disks")).
		WillReturnRows(sqlmock.NewRows([]string{
			"name",
			"path",
			"cache_path",
			"free_space",
			"total_space",
			"unreserved_space",
			"type",
			"object_storage_type",
			"is_remote",
			"is_broken",
		}).
			AddRow("default", "/var/lib/clickhouse/", "", int64(100), int64(200), int64(90), "Local", "", int64(0), int64(0)).
			AddRow("s3_cache", "", "/var/lib/clickhouse/disks/s3_cache/", maxUint, maxUint, maxUint, "Cache", "s3", int64(1), int64(1)))

	disks, warning := collector.collectDisks(t.Context(), client)

	require.Nil(t, warning)
	require.Len(t, disks, 2)
	require.Equal(t, client.Node, disks[0].Node)
	require.Equal(t, "default", disks[0].Name)
	require.Equal(t, uint64(33), disks[0].UsedByActiveParts)
	require.True(t, disks[0].CapacityKnown)
	require.Equal(t, uint64(100), *disks[0].FreeSpaceBytes)
	require.False(t, disks[0].IsRemote)
	require.Equal(t, "s3_cache", disks[1].Name)
	require.False(t, disks[1].CapacityKnown)
	require.True(t, disks[1].IsRemote)
	require.True(t, disks[1].IsBroken)
}

func TestCollectDisksUsageWarning(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
		WithArgs("db", "tbl").
		WillReturnError(errors.New("parts query failed"))

	disks, warning := collector.collectDisks(t.Context(), client)

	require.Nil(t, disks)
	require.NotNil(t, warning)
	require.Equal(t, warningKindQueryError, warning.Kind)
	require.Equal(t, "system_parts_disk_usage_query_failed", warning.Code)
}

func TestCollectDisksWarningsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "disks query",
			code: "system_disks_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
					WithArgs("db", "tbl").
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.disks")).
					WillReturnError(errors.New("disks query failed"))
			},
		},
		{
			name: "disks scan",
			code: "system_disks_scan_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
					WithArgs("db", "tbl").
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.disks")).
					WillReturnRows(sqlmock.NewRows(diskColumns()).
						AddRow("default", "/var/lib/clickhouse/", "", "bad", int64(200), int64(90), "Local", "", int64(0), int64(0)))
			},
		},
		{
			name: "disks rows",
			code: "system_disks_rows_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
					WithArgs("db", "tbl").
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.disks")).
					WillReturnRows(sqlmock.NewRows(diskColumns()).
						AddRow("default", "/var/lib/clickhouse/", "", int64(100), int64(200), int64(90), "Local", "", int64(0), int64(0)).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			disks, warning := collector.collectDisks(t.Context(), client)

			require.Nil(t, disks)
			require.NotNil(t, warning)
			require.Equal(t, warningKindQueryError, warning.Kind)
			require.Equal(t, tt.code, warning.Code)
		})
	}
}

func TestCollectWatchedPartBytesByDiskErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows([]string{"disk_name", "sum"}).
						AddRow("default", "bad"))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows([]string{"disk_name", "sum"}).
						AddRow("default", int64(1)).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			used, err := collector.collectWatchedPartBytesByDisk(t.Context(), client)

			require.Nil(t, used)
			require.Error(t, err)
		})
	}
}

func TestPublicCollectorSuccessBranchesSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(tableDefinitionRows())
	mock.ExpectQuery(queryContaining("countDistinct(partition_id)")).
		WithArgs("db", "tbl").
		WillReturnRows(tableSummaryRows())
	mock.ExpectQuery(queryContaining("FROM system.replicas")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	tables := collector.CollectTables(t.Context())
	require.Len(t, tables.Items, 1)

	mock.ExpectQuery(queryContaining("FROM system.columns")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows(columnColumns()).
			AddRow("ts", int64(1), "DateTime", "", "", "", "", int64(1), int64(1), int64(1), int64(0)))
	columns := collector.CollectTableColumns(t.Context(), sqlTestWatch)
	require.Len(t, columns.Items, 1)
}

func TestCollectTableStateSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{
			"uuid",
			"engine",
			"storage_policy",
			"partition_key",
			"sorting_key",
			"primary_key",
			"sampling_key",
			"is_replicated",
		}).AddRow("uuid-1", "ReplicatedMergeTree", "tiered", "toYYYYMM(ts)", "ts", "ts", "", int64(1)))
	mock.ExpectQuery(queryContaining("countDistinct(partition_id)")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{
			"active_partitions",
			"active_parts",
			"rows",
			"bytes_on_disk",
			"min_partition",
			"max_partition",
			"last_modification_time",
		}).AddRow(int64(2), int64(5), int64(100), int64(4096), "202601", "202602", modified))
	mock.ExpectQuery(queryContaining("FROM system.replicas")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{
			"is_readonly",
			"is_session_expired",
			"queue_size",
			"absolute_delay",
			"total_replicas",
			"active_replicas",
		}).AddRow(int64(1), int64(0), int64(3), int64(9), int64(2), int64(1)))

	table, ok, err := collector.collectTableState(t.Context(), client, sqlTestWatch)

	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, client.Node, table.Node)
	require.Equal(t, "uuid-1", table.UUID)
	require.Equal(t, "ReplicatedMergeTree", table.Engine)
	require.True(t, table.IsReplicated)
	require.Equal(t, 2, table.ActivePartitions)
	require.Equal(t, uint64(5), table.ActiveParts)
	require.Equal(t, uint64(100), table.Rows)
	require.Equal(t, uint64(4096), table.BytesOnDisk)
	require.Equal(t, "202601", *table.MinPartition)
	require.Equal(t, "202602", *table.MaxPartition)
	require.Equal(t, modified, *table.LastModificationTime)
	require.NotNil(t, table.Replica)
	require.True(t, table.Replica.Readonly)
	require.False(t, table.Replica.SessionExpired)
	require.Equal(t, uint64(3), table.Replica.QueueSize)
	require.Equal(t, uint64(9), table.Replica.AbsoluteDelaySeconds)
	require.Equal(t, uint64(2), table.Replica.TotalReplicas)
	require.Equal(t, uint64(1), table.Replica.ActiveReplicas)
}

func TestCollectTableStateMissingSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{
			"uuid",
			"engine",
			"storage_policy",
			"partition_key",
			"sorting_key",
			"primary_key",
			"sampling_key",
			"is_replicated",
		}))

	table, ok, err := collector.collectTableState(t.Context(), client, sqlTestWatch)

	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, table)
}

func TestCollectTableStateErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "tables query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.tables")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("tables query failed"))
			},
		},
		{
			name: "parts summary",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.tables")).
					WithArgs("db", "tbl").
					WillReturnRows(tableDefinitionRows())
				mock.ExpectQuery(queryContaining("countDistinct(partition_id)")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("parts summary failed"))
			},
		},
		{
			name: "replica state",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.tables")).
					WithArgs("db", "tbl").
					WillReturnRows(tableDefinitionRows())
				mock.ExpectQuery(queryContaining("countDistinct(partition_id)")).
					WithArgs("db", "tbl").
					WillReturnRows(tableSummaryRows())
				mock.ExpectQuery(queryContaining("FROM system.replicas")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("replica query failed"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			table, ok, err := collector.collectTableState(t.Context(), client, sqlTestWatch)

			require.Error(t, err)
			require.False(t, ok)
			require.Empty(t, table)
		})
	}
}

func TestCollectReplicaStateNoRowsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.replicas")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{
			"is_readonly",
			"is_session_expired",
			"queue_size",
			"absolute_delay",
			"total_replicas",
			"active_replicas",
		}))

	replica, err := collector.collectReplicaState(t.Context(), client, sqlTestWatch)

	require.NoError(t, err)
	require.Nil(t, replica)
}

func TestCollectReplicaStateQueryErrorSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.replicas")).
		WithArgs("db", "tbl").
		WillReturnError(errors.New("replica query failed"))

	replica, err := collector.collectReplicaState(t.Context(), client, sqlTestWatch)

	require.Nil(t, replica)
	require.Error(t, err)
}

func TestCollectTableColumnsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.columns")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{
			"name",
			"position",
			"type",
			"default_kind",
			"default_expression",
			"compression_codec",
			"comment",
			"is_in_partition_key",
			"is_in_sorting_key",
			"is_in_primary_key",
			"is_in_sampling_key",
		}).
			AddRow("ts", int64(1), "DateTime", "", "", "CODEC(ZSTD)", "event time", int64(1), int64(1), int64(1), int64(0)).
			AddRow("derived", int64(2), "String", "MATERIALIZED", "toString(ts)", "", "", int64(0), int64(0), int64(0), int64(0)))

	item, err := collector.collectTableColumns(t.Context(), client, sqlTestWatch)

	require.NoError(t, err)
	require.Equal(t, client.Node, item.Node)
	require.Len(t, item.Columns, 2)
	require.Equal(t, "regular", item.Columns[0].Kind)
	require.Nil(t, item.Columns[0].DefaultKind)
	require.Equal(t, "CODEC(ZSTD)", *item.Columns[0].CodecExpression)
	require.True(t, item.Columns[0].IsInPartitionKey)
	require.True(t, item.Columns[0].IsInSortingKey)
	require.True(t, item.Columns[0].IsInPrimaryKey)
	require.False(t, item.Columns[0].IsInSamplingKey)
	require.Equal(t, "materialized", item.Columns[1].Kind)
	require.Equal(t, "MATERIALIZED", *item.Columns[1].DefaultKind)
	require.Equal(t, "toString(ts)", *item.Columns[1].DefaultExpression)
}

func TestCollectTableColumnsErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.columns")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("columns query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.columns")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows(columnColumns()).
						AddRow("ts", "bad", "DateTime", "", "", "", "", int64(1), int64(1), int64(1), int64(0)))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.columns")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows(columnColumns()).
						AddRow("ts", int64(1), "DateTime", "", "", "", "", int64(1), int64(1), int64(1), int64(0)).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			item, err := collector.collectTableColumns(t.Context(), client, sqlTestWatch)

			require.Error(t, err)
			require.Empty(t, item)
		})
	}
}

func TestCollectPartsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	modified := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	removed := modified.Add(time.Hour)
	ttlMin := modified.Add(-time.Hour)
	mock.ExpectQuery(queryContaining("default_compression_codec")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows(partColumns()).
			AddRow(
				"db",
				"tbl",
				"202602",
				"pid",
				"all_0_0_0",
				"part-uuid",
				int64(1),
				"default",
				"/var/lib/clickhouse/data/db/tbl/all_0_0_0/",
				"Wide",
				int64(10),
				int64(2),
				int64(1024),
				int64(700),
				int64(1400),
				int64(40),
				int64(20),
				int64(30),
				int64(3),
				int64(6),
				int64(1),
				modified,
				removed,
				int64(3),
				int64(0),
				int64(0),
				int64(1),
				int64(7),
				ttlMin,
				time.Time{},
				"LZ4",
			))

	parts, err := collector.collectParts(t.Context(), client, sqlTestWatch, true)

	require.NoError(t, err)
	require.Len(t, parts, 1)
	part := parts[0]
	require.Equal(t, client.Node, part.Node)
	require.Equal(t, "db", part.Database)
	require.Equal(t, "tbl", part.Table)
	require.Equal(t, "202602", part.Partition)
	require.Equal(t, "pid", part.PartitionID)
	require.Equal(t, "all_0_0_0", part.Name)
	require.True(t, part.Active)
	require.Equal(t, "default", part.Disk)
	require.Equal(t, "Wide", part.PartType)
	require.Equal(t, uint64(10), part.Rows)
	require.Equal(t, uint64(3), part.Refcount)
	require.Equal(t, int64(0), part.MinBlockNumber)
	require.Equal(t, uint64(1), part.Level)
	require.Equal(t, uint64(7), part.DataVersion)
	require.Equal(t, removed, *part.RemoveTime)
	require.Equal(t, ttlMin, *part.DeleteTTLInfoMin)
	require.Nil(t, part.DeleteTTLInfoMax)
	require.Equal(t, "LZ4", part.DefaultCompressionCodec)
}

func TestCollectPartsErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("default_compression_codec")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("parts query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("default_compression_codec")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows(partColumns()).AddRow(partRowValues("bad")...))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("default_compression_codec")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows(partColumns()).
						AddRow(partRowValues(int64(1))...).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			parts, err := collector.collectParts(t.Context(), client, sqlTestWatch, false)

			require.Nil(t, parts)
			require.Error(t, err)
		})
	}
}

func TestCollectDetachedPartsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	modified := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{
			"database",
			"table",
			"partition_id",
			"name",
			"disk",
			"reason",
			"path",
			"bytes_on_disk",
			"modification_time",
			"min_block_number",
			"max_block_number",
			"level",
		}).
			AddRow("db", "tbl", "pid", "detached_1", "default", "broken", "/detached_1", int64(512), modified, int64(1), int64(2), int64(3)).
			AddRow("db", "tbl", nil, "detached_2", "s3", nil, "/detached_2", int64(128), modified, nil, nil, nil))

	parts, err := collector.collectDetachedParts(t.Context(), client, sqlTestWatch)

	require.NoError(t, err)
	require.Len(t, parts, 2)
	require.Equal(t, client.Node, parts[0].Node)
	require.Equal(t, "pid", *parts[0].PartitionID)
	require.Equal(t, "broken", *parts[0].Reason)
	require.Equal(t, int64(1), *parts[0].MinBlockNumber)
	require.Equal(t, int64(2), *parts[0].MaxBlockNumber)
	require.Equal(t, uint64(3), *parts[0].Level)
	require.Nil(t, parts[1].PartitionID)
	require.Nil(t, parts[1].Reason)
	require.Nil(t, parts[1].MinBlockNumber)
	require.Nil(t, parts[1].MaxBlockNumber)
	require.Nil(t, parts[1].Level)
}

func TestCollectDetachedPartsErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("detached query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows(detachedColumns()).
						AddRow("db", "tbl", "pid", "detached_1", "default", "broken", "/detached_1", "bad", time.Now(), int64(1), int64(2), int64(3)))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows(detachedColumns()).
						AddRow("db", "tbl", "pid", "detached_1", "default", "broken", "/detached_1", int64(512), time.Now(), int64(1), int64(2), int64(3)).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			parts, err := collector.collectDetachedParts(t.Context(), client, sqlTestWatch)

			require.Nil(t, parts)
			require.Error(t, err)
		})
	}
}

func TestCollectMutationsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	created := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	failedAt := created.Add(time.Minute)
	mock.ExpectQuery(queryContaining("FROM system.mutations")).
		WithArgs("db", "tbl").
		WillReturnRows(mock.NewRows([]string{
			"database",
			"table",
			"mutation_id",
			"command",
			"create_time",
			"is_done",
			"is_killed",
			"parts_to_do",
			"parts_to_do_names",
			"block_numbers.partition_id",
			"block_numbers.number",
			"latest_failed_part",
			"latest_fail_time",
			"latest_fail_reason",
		}).AddRow(
			"db",
			"tbl",
			"mutation_1.txt",
			"DELETE WHERE bad = 1",
			created,
			int64(0),
			int64(1),
			int64(-1),
			[]string{"all_1_1_0"},
			[]string{"pid1", "pid2"},
			[]int64{4, -1},
			"all_1_1_0",
			failedAt,
			"bad mutation",
		))

	mutations, err := collector.collectMutations(t.Context(), client, sqlTestWatch)

	require.NoError(t, err)
	require.Len(t, mutations, 1)
	mutation := mutations[0]
	require.Equal(t, client.Node, mutation.Node)
	require.Equal(t, "mutation_1.txt", mutation.MutationID)
	require.False(t, mutation.IsDone)
	require.True(t, mutation.IsKilled)
	require.Equal(t, uint64(0), mutation.PartsToDo)
	require.Equal(t, []string{"all_1_1_0"}, mutation.PartsToDoNames)
	require.Equal(t, []MutationBlockNumber{{PartitionID: "pid1", Number: 4}, {PartitionID: "pid2", Number: 0}}, mutation.BlockNumbers)
	require.Equal(t, "all_1_1_0", *mutation.LatestFailedPart)
	require.Equal(t, failedAt, *mutation.LatestFailTime)
	require.Equal(t, "bad mutation", *mutation.LatestFailReason)
	require.Len(t, mutation.Conditions, 1)
	require.Equal(t, "mutation_failed", mutation.Conditions[0].Code)
}

func TestCollectMutationsErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.mutations")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("mutations query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.mutations")).
					WithArgs("db", "tbl").
					WillReturnRows(mock.NewRows(mutationColumns()).
						AddRow(mutationRowValues("bad")...))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.mutations")).
					WithArgs("db", "tbl").
					WillReturnRows(mock.NewRows(mutationColumns()).
						AddRow(mutationRowValues(int64(0))...).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			mutations, err := collector.collectMutations(t.Context(), client, sqlTestWatch)

			require.Nil(t, mutations)
			require.Error(t, err)
		})
	}
}

func TestCollectReplicationQueueSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	created := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	attempted := created.Add(time.Minute)
	mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
		WithArgs("db", "tbl").
		WillReturnRows(mock.NewRows([]string{
			"database",
			"table",
			"replica_name",
			"position",
			"node_name",
			"type",
			"create_time",
			"required_quorum",
			"source_replica",
			"new_part_name",
			"parts_to_merge",
			"is_detach",
			"is_currently_executing",
			"num_tries",
			"last_attempt_time",
			"last_postpone_time",
			"num_postponed",
			"postpone_reason",
			"last_exception",
		}).AddRow(
			"db",
			"tbl",
			"replica1",
			int64(4),
			"/clickhouse/task",
			"GET_PART",
			created,
			int64(2),
			"replica2",
			"all_2_2_0",
			[]string{"all_1_1_0", "all_2_2_0"},
			int64(0),
			int64(1),
			int64(3),
			attempted,
			time.Time{},
			int64(1),
			"",
			"fetch failed",
		))

	items, err := collector.collectReplicationQueue(t.Context(), client, sqlTestWatch)

	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	require.Equal(t, client.Node, item.Node)
	require.Equal(t, "replica1", item.ReplicaName)
	require.Equal(t, uint64(4), item.Position)
	require.Equal(t, uint64(2), item.RequiredQuorum)
	require.Equal(t, "replica2", *item.SourceReplica)
	require.Equal(t, "all_2_2_0", *item.NewPartName)
	require.Equal(t, []string{"all_1_1_0", "all_2_2_0"}, item.PartsToMerge)
	require.False(t, item.IsDetach)
	require.True(t, item.IsCurrentlyExecuting)
	require.Equal(t, uint64(3), item.NumTries)
	require.Equal(t, attempted, *item.LastAttemptTime)
	require.Nil(t, item.LastPostponeTime)
	require.Equal(t, uint64(1), item.NumPostponed)
	require.Nil(t, item.PostponeReason)
	require.Equal(t, "fetch failed", *item.LastException)
	require.Len(t, item.Conditions, 1)
	require.Equal(t, "replication_queue_exception", item.Conditions[0].Code)
}

func TestCollectReplicationQueueErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("replication queue failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
					WithArgs("db", "tbl").
					WillReturnRows(mock.NewRows(replicationQueueColumns()).
						AddRow(replicationQueueRowValues("bad")...))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
					WithArgs("db", "tbl").
					WillReturnRows(mock.NewRows(replicationQueueColumns()).
						AddRow(replicationQueueRowValues(int64(4))...).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			items, err := collector.collectReplicationQueue(t.Context(), client, sqlTestWatch)

			require.Nil(t, items)
			require.Error(t, err)
		})
	}
}

func TestCollectPartEventsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	eventTime := from.Add(time.Hour)
	mock.ExpectQuery(queryContaining("FROM system.part_log")).
		WithArgs("db", "tbl", from, from, to).
		WillReturnRows(mock.NewRows([]string{
			"database",
			"table",
			"partition",
			"partition_id",
			"part_name",
			"event_type",
			"event_time",
			"event_time_microseconds",
			"duration_ms",
			"rows",
			"size_in_bytes",
			"bytes_uncompressed",
			"read_rows",
			"read_bytes",
			"merged_from",
			"disk_name",
			"error",
			"exception",
		}).AddRow(
			"db",
			"tbl",
			"202606",
			"pid",
			"all_0_1_1",
			"MergeParts",
			eventTime,
			"2026-06-01 01:00:00.123456",
			int64(25),
			int64(50),
			int64(4096),
			int64(8192),
			int64(60),
			int64(2048),
			[]string{"all_0_0_0", "all_1_1_0"},
			"s3_cache",
			int64(0),
			"",
		))

	events, warning, err := collector.collectPartEvents(t.Context(), client, sqlTestWatch, from, &to)

	require.NoError(t, err)
	require.Nil(t, warning)
	require.Len(t, events, 1)
	event := events[0]
	require.Equal(t, client.Node, event.Node)
	require.Equal(t, "MergeParts", event.EventType)
	require.Equal(t, eventTime, event.EventTime)
	require.Equal(t, "2026-06-01 01:00:00.123456", event.EventTimeMicrostamp)
	require.Equal(t, uint64(25), event.DurationMs)
	require.Equal(t, []string{"all_0_0_0", "all_1_1_0"}, event.MergedFrom)
	require.Equal(t, "s3_cache", *event.TargetDisk)
	require.Equal(t, int64(0), event.Error)
	require.Nil(t, event.Exception)
}

func TestCollectPartEventsUnavailableWarningSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(queryContaining("FROM system.part_log")).
		WithArgs("db", "tbl", from, from).
		WillReturnError(&clickhouse.Exception{Code: chErrCodeUnknownTable, Message: "system.part_log does not exist"})

	events, warning, err := collector.collectPartEvents(t.Context(), client, sqlTestWatch, from, nil)

	require.NoError(t, err)
	require.Nil(t, events)
	require.NotNil(t, warning)
	require.Equal(t, warningKindCapability, warning.Kind)
	require.Equal(t, "part_log_unavailable", warning.Code)
}

func TestCollectPartEventsErrorsSQL(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.part_log")).
					WithArgs("db", "tbl", from, from).
					WillReturnError(errors.New("part log query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.part_log")).
					WithArgs("db", "tbl", from, from).
					WillReturnRows(mock.NewRows(partEventColumns()).
						AddRow(partEventRowValues("bad")...))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.part_log")).
					WithArgs("db", "tbl", from, from).
					WillReturnRows(mock.NewRows(partEventColumns()).
						AddRow(partEventRowValues(int64(25))...).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			events, warning, err := collector.collectPartEvents(t.Context(), client, sqlTestWatch, from, nil)

			require.Nil(t, events)
			require.Nil(t, warning)
			require.Error(t, err)
		})
	}
}

func TestCollectMoveOperationsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
		WillReturnRows(sqlmock.NewRows([]string{
			"database",
			"table",
			"partition",
			"partition_id",
			"elapsed",
			"target_disk_name",
			"part_name",
			"part_size",
			"thread_id",
		}).
			AddRow("db", "tbl", "202606", "pid", 1.5, "s3", "all_0_0_0", int64(2048), int64(99)).
			AddRow("other", "tbl", "202606", "pid", 2.5, "s3", "ignored", int64(4096), int64(100)))

	operations, err := collector.collectMoveOperations(t.Context(), client)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	require.Equal(t, "move", operations[0].Kind)
	require.Equal(t, "running", operations[0].State)
	require.Equal(t, "all_0_0_0:99", operations[0].AttemptID)
	require.Equal(t, "202606", *operations[0].Partition)
	require.Equal(t, "pid", *operations[0].PartitionID)
	require.Equal(t, "s3", *operations[0].TargetDisk)
	require.Equal(t, uint64(2048), *operations[0].BytesTotal)
	require.InDelta(t, 1.5, *operations[0].ElapsedSeconds, 0)
}

func TestCollectMoveOperationsErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
					WillReturnError(errors.New("moves query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
					WillReturnRows(sqlmock.NewRows(moveColumns()).
						AddRow("db", "tbl", "202606", "pid", "bad", "s3", "all_0_0_0", int64(2048), int64(99)))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
					WillReturnRows(sqlmock.NewRows(moveColumns()).
						AddRow("db", "tbl", "202606", "pid", 1.5, "s3", "all_0_0_0", int64(2048), int64(99)).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			operations, err := collector.collectMoveOperations(t.Context(), client)

			require.Nil(t, operations)
			require.Error(t, err)
		})
	}
}

func TestCollectMergeOperationsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.merges")).
		WillReturnRows(sqlmock.NewRows([]string{
			"database",
			"table",
			"partition",
			"partition_id",
			"elapsed",
			"progress",
			"result_part_name",
			"is_mutation",
			"total_size_bytes_compressed",
			"bytes_read_uncompressed",
		}).
			AddRow("db", "tbl", "202606", "pid", 4.5, 0.25, "all_0_1_1", int64(0), int64(1000), int64(250)).
			AddRow("db", "tbl", "202607", "pid2", 5.5, 0.75, "all_2_3_1", int64(1), int64(2000), int64(1500)).
			AddRow("other", "tbl", "202608", "pid3", 6.5, 0.5, "ignored", int64(0), int64(3000), int64(1500)))

	operations, err := collector.collectMergeOperations(t.Context(), client)

	require.NoError(t, err)
	require.Len(t, operations, 2)
	require.Equal(t, "merge", operations[0].Kind)
	require.Equal(t, "mutation", operations[1].Kind)
	require.Equal(t, "running", operations[0].State)
	require.Equal(t, "all_0_1_1", operations[0].AttemptID)
	require.Equal(t, "202606", *operations[0].Partition)
	require.Equal(t, "pid", *operations[0].PartitionID)
	require.Equal(t, uint64(1000), *operations[0].BytesTotal)
	require.Equal(t, uint64(250), *operations[0].BytesProcessed)
	require.InDelta(t, 0.25, *operations[0].Progress, 0)
}

func TestCollectMergeOperationsErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "query",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.merges")).
					WillReturnError(errors.New("merges query failed"))
			},
		},
		{
			name: "scan",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.merges")).
					WillReturnRows(sqlmock.NewRows(mergeColumns()).
						AddRow("db", "tbl", "202606", "pid", "bad", 0.25, "all_0_1_1", int64(0), int64(1000), int64(250)))
			},
		},
		{
			name: "rows",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.merges")).
					WillReturnRows(sqlmock.NewRows(mergeColumns()).
						AddRow("db", "tbl", "202606", "pid", 4.5, 0.25, "all_0_1_1", int64(0), int64(1000), int64(250)).
						RowError(0, errors.New("rows failed")))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, client, mock := mockCollectorClient(t)
			tt.mock(mock)

			operations, err := collector.collectMergeOperations(t.Context(), client)

			require.Nil(t, operations)
			require.Error(t, err)
		})
	}
}

//nolint:funlen // This walks every public collector against one mock node in one ordered pass.
func TestPublicCollectorsSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(queryContaining("FROM system.one")).
		WillReturnRows(sqlmock.NewRows([]string{"version", "timezone", "uptime"}).
			AddRow("26.2.5.45", "UTC", int64(123)))
	nodes := collector.CollectNodes(t.Context())
	require.Equal(t, 1, nodes.NodesExpected)
	require.Equal(t, 1, nodes.NodesResponded)
	require.Len(t, nodes.Items, 1)

	mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.disks")).
		WillReturnRows(emptyRows())
	disks := collector.CollectDisks(t.Context())
	require.Equal(t, 1, disks.NodesExpected)
	require.Equal(t, 1, disks.NodesResponded)
	require.Empty(t, disks.Items)

	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	tables := collector.CollectTables(t.Context())
	require.Equal(t, 1, tables.NodesResponded)
	require.Empty(t, tables.Items)

	mock.ExpectQuery(queryContaining("FROM system.columns")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	columns := collector.CollectTableColumns(t.Context(), sqlTestWatch)
	require.Equal(t, 1, columns.NodesResponded)
	require.Empty(t, columns.Items)

	mock.ExpectQuery(queryContaining("default_compression_codec")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	parts := collector.CollectParts(t.Context(), sqlTestWatch)
	require.Equal(t, 1, parts.NodesResponded)
	require.Empty(t, parts.Items)

	mock.ExpectQuery(queryContaining("default_compression_codec")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	activeParts := collector.CollectActiveParts(t.Context(), sqlTestWatch)
	require.Equal(t, 1, activeParts.NodesResponded)
	require.Empty(t, activeParts.Items)

	mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	detached := collector.CollectDetachedParts(t.Context(), sqlTestWatch)
	require.Equal(t, 1, detached.NodesResponded)
	require.Empty(t, detached.Items)

	mock.ExpectQuery(queryContaining("FROM system.mutations")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mutations := collector.CollectMutations(t.Context())
	require.Equal(t, 1, mutations.NodesResponded)
	require.Empty(t, mutations.Items)

	mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	queue := collector.CollectReplicationQueue(t.Context())
	require.Equal(t, 1, queue.NodesResponded)
	require.Empty(t, queue.Items)

	mock.ExpectQuery(queryContaining("FROM system.part_log")).
		WithArgs("db", "tbl", from, from).
		WillReturnRows(emptyRows())
	events := collector.CollectPartEvents(t.Context(), &from, nil)
	require.Equal(t, 1, events.NodesResponded)
	require.Empty(t, events.Items)

	mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.merges")).
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.mutations")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	operations := collector.CollectOperations(t.Context())
	require.Equal(t, 1, operations.NodesResponded)
	require.Empty(t, operations.Items)
}

//nolint:funlen // The public warning matrix mirrors every collector entrypoint.
func TestPublicCollectorsWarningsSQL(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		code    string
		mock    func(sqlmock.Sqlmock)
		collect func(*Collector) []Warning
	}{
		{
			name: "nodes",
			code: "node_unreachable",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.one")).
					WillReturnError(errors.New("connection refused"))
			},
			collect: func(c *Collector) []Warning { return c.CollectNodes(t.Context()).Warnings },
		},
		{
			name: "disks",
			code: "system_disks_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("sum(bytes_on_disk)")).
					WithArgs("db", "tbl").
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.disks")).
					WillReturnError(errors.New("disks query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectDisks(t.Context()).Warnings },
		},
		{
			name: "tables",
			code: "system_tables_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.tables")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("tables query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectTables(t.Context()).Warnings },
		},
		{
			name: "columns",
			code: "system_columns_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.columns")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("columns query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectTableColumns(t.Context(), sqlTestWatch).Warnings },
		},
		{
			name: "parts",
			code: "system_parts_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("default_compression_codec")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("parts query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectParts(t.Context(), sqlTestWatch).Warnings },
		},
		{
			name: "detached parts",
			code: "system_detached_parts_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("detached query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectDetachedParts(t.Context(), sqlTestWatch).Warnings },
		},
		{
			name: "mutations",
			code: "system_mutations_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.mutations")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("mutations query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectMutations(t.Context()).Warnings },
		},
		{
			name: "replication queue",
			code: "system_replication_queue_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("queue query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectReplicationQueue(t.Context()).Warnings },
		},
		{
			name: "part events warning",
			code: "part_log_unavailable",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.part_log")).
					WithArgs("db", "tbl", from, from).
					WillReturnError(&clickhouse.Exception{Code: chErrCodeUnknownTable, Message: "system.part_log does not exist"})
			},
			collect: func(c *Collector) []Warning { return c.CollectPartEvents(t.Context(), &from, nil).Warnings },
		},
		{
			name: "part events error",
			code: "system_part_log_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.part_log")).
					WithArgs("db", "tbl", from, from).
					WillReturnError(errors.New("part log query failed"))
			},
			collect: func(c *Collector) []Warning { return c.CollectPartEvents(t.Context(), &from, nil).Warnings },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, _, mock := mockPooledCollectorClient(t)
			tt.mock(mock)

			warnings := tt.collect(collector)

			require.Len(t, warnings, 1)
			require.Equal(t, tt.code, warnings[0].Code)
		})
	}
}

func TestCollectOperationsFromMutationsAndReplicationQueueSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	created := time.Date(2026, 7, 8, 9, 10, 11, 0, time.UTC)
	mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.merges")).
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.mutations")).
		WithArgs("db", "tbl").
		WillReturnRows(mock.NewRows(mutationColumns()).
			AddRow(mutationRowValuesWithID("done.txt", int64(1), "")...).
			AddRow(mutationRowValuesWithID("running.txt", int64(0), "still running")...))
	mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
		WithArgs("db", "tbl").
		WillReturnRows(mock.NewRows(replicationQueueColumns()).
			AddRow(replicationQueueRowValuesWithPosition(4, "GET_PART", true, "all_2_2_0", "fetch failed", created)...).
			AddRow(replicationQueueRowValuesWithPosition(5, "MERGE_PARTS", false, "all_3_3_0", "", created)...))

	result := collector.CollectOperations(t.Context())

	require.Empty(t, result.Warnings)
	require.Len(t, result.Items, 3)
	require.Equal(t, "mutation", result.Items[0].Kind)
	require.Equal(t, "running.txt", result.Items[0].AttemptID)
	require.Equal(t, "fetch", result.Items[1].Kind)
	require.Equal(t, "running", result.Items[1].State)
	require.Equal(t, "replication_queue", result.Items[2].Kind)
	require.Equal(t, "queued", result.Items[2].State)
}

func TestCollectOperationsWarningsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		mock func(sqlmock.Sqlmock)
	}{
		{
			name: "moves",
			code: "system_moves_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
					WillReturnError(errors.New("moves query failed"))
			},
		},
		{
			name: "merges",
			code: "system_merges_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.merges")).
					WillReturnError(errors.New("merges query failed"))
			},
		},
		{
			name: "mutations",
			code: "system_mutations_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.merges")).
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.mutations")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("mutations query failed"))
			},
		},
		{
			name: "replication queue",
			code: "system_replication_queue_query_failed",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.moves AS m")).
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.merges")).
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.mutations")).
					WithArgs("db", "tbl").
					WillReturnRows(emptyRows())
				mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
					WithArgs("db", "tbl").
					WillReturnError(errors.New("queue query failed"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, _, mock := mockPooledCollectorClient(t)
			tt.mock(mock)

			result := collector.CollectOperations(t.Context())

			require.Len(t, result.Warnings, 1)
			require.Equal(t, tt.code, result.Warnings[0].Code)
			require.Empty(t, result.Items)
		})
	}
}

func TestCollectConditionsSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("default_compression_codec")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.mutations")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())

	conditions := collector.CollectConditions(t.Context())

	require.Equal(t, 1, conditions.NodesResponded)
	require.Len(t, conditions.Items, 1)
	require.Equal(t, "table_missing_on_node", conditions.Items[0].Code)
}

func TestTableConditionsIncludesMutationAndQueueConditionsSQL(t *testing.T) {
	t.Parallel()

	collector, client, mock := mockPooledCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.mutations")).
		WithArgs("db", "tbl").
		WillReturnRows(mock.NewRows(mutationColumns()).
			AddRow(mutationRowValuesWithID("running.txt", int64(0), "still running")...))
	mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
		WithArgs("db", "tbl").
		WillReturnRows(mock.NewRows(replicationQueueColumns()).
			AddRow(replicationQueueRowValuesWithPosition(4, "GET_PART", true, "all_2_2_0", "fetch failed", time.Now())...))

	tables := Result[TableState]{
		NodesExpected:  1,
		NodesResponded: 1,
		Items: []TableState{{
			Node:     client.Node,
			Database: "db",
			Table:    "tbl",
			Engine:   "MergeTree",
		}},
	}
	result := collector.TableConditions(t.Context(), tables)

	require.Empty(t, result.Warnings)
	require.Len(t, result.Items, 2)
	require.Equal(t, "mutation_failed", result.Items[0].Code)
	require.Equal(t, "replication_queue_exception", result.Items[1].Code)
}

func TestPartitionConditionsIncludesDetachedPartSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows(detachedColumns()).
			AddRow("db", "tbl", "pid", "detached_1", "default", "broken", "/detached_1", int64(512), time.Now(), int64(1), int64(2), int64(3)))

	result := collector.PartitionConditions(t.Context(), sqlTestWatch, Result[Part]{NodesExpected: 1, NodesResponded: 1})

	require.Empty(t, result.Warnings)
	require.Len(t, result.Items, 1)
	require.Equal(t, "detached_part_present", result.Items[0].Code)
}

func TestCollectConditionsIncludesCollectionWarningsSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnError(errors.New("tables query failed"))
	mock.ExpectQuery(queryContaining("default_compression_codec")).
		WithArgs("db", "tbl").
		WillReturnError(errors.New("parts query failed"))
	mock.ExpectQuery(queryContaining("FROM system.detached_parts")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.mutations")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())
	mock.ExpectQuery(queryContaining("FROM system.replication_queue")).
		WithArgs("db", "tbl").
		WillReturnRows(emptyRows())

	result := collector.CollectConditions(t.Context())

	require.NotEmpty(t, result.Warnings)
	require.NotEmpty(t, result.Items)
	warningCodes := make(map[string]struct{}, len(result.Warnings))
	for _, warning := range result.Warnings {
		warningCodes[warning.Code] = struct{}{}
	}
	_, found := warningCodes[result.Items[len(result.Items)-1].Code]
	require.True(t, found)
	require.Equal(t, warningKindQueryError, result.Items[len(result.Items)-1].Evidence["kind"])
}

func TestValidateWatchesSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("MergeTree"))

	warnings, err := collector.ValidateWatches(t.Context())

	require.NoError(t, err)
	require.Empty(t, warnings)
}

func TestValidateWatchesDetailedPartialSQL(t *testing.T) {
	t.Parallel()

	_, clientA, mockA := mockCollectorClient(t)
	_, clientB, mockB := mockCollectorClient(t)
	clientB.Node.ID = "node-b"
	clientB.Node.Replica = "replica2"
	collector := New(poolWithClients(clientA, clientB), 0, []Watch{sqlTestWatch})

	mockA.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("MergeTree"))
	mockB.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnError(errors.New("connection refused"))

	result, err := collector.ValidateWatchesDetailed(t.Context())

	require.NoError(t, err)
	require.Equal(t, 2, result.NodesExpected)
	require.Equal(t, 1, result.NodesResponded)
	require.Equal(t, 1, result.NodesFailed)
	require.Len(t, result.Warnings, 1)
	require.Equal(t, "node_unreachable", result.Warnings[0].Code)
	require.Equal(t, "node-b", result.Warnings[0].NodeID)
}

func TestValidateWatchesErrorsSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		collector *Collector
		expect    func(sqlmock.Sqlmock)
		wantErr   string
	}{
		{
			name:      "no responders",
			collector: &Collector{},
			wantErr:   "no ClickHouse node responded",
		},
		{
			name: "missing table",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.tables")).
					WithArgs("db", "tbl").
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: "db.tbl: missing on responding node node-a",
		},
		{
			name: "distributed table",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(queryContaining("FROM system.tables")).
					WithArgs("db", "tbl").
					WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("Distributed"))
			},
			wantErr: `db.tbl: engine "Distributed" on node node-a is not a physical MergeTree-family table`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector := tt.collector
			if collector == nil {
				var mock sqlmock.Sqlmock
				collector, _, mock = mockPooledCollectorClient(t)
				tt.expect(mock)
			}

			warnings, err := collector.ValidateWatches(t.Context())

			require.Empty(t, warnings)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateWatchesWarningSQL(t *testing.T) {
	t.Parallel()

	collector, _, mock := mockPooledCollectorClient(t)
	mock.ExpectQuery(queryContaining("FROM system.tables")).
		WithArgs("db", "tbl").
		WillReturnError(errors.New("validation query failed"))

	warnings, err := collector.ValidateWatches(t.Context())

	require.ErrorContains(t, err, "no ClickHouse node responded")
	require.Len(t, warnings, 1)
	require.Equal(t, "system_tables_watch_validation_failed", warnings[0].Code)
}

func mockCollectorClient(t *testing.T, watches ...Watch) (*Collector, chclient.Client, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp),
		sqlmock.ValueConverterOption(sqlArrayConverter{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})

	if len(watches) == 0 {
		watches = []Watch{sqlTestWatch}
	}

	collector := New(nil, 0, watches)
	client := chclient.Client{
		Node: chclient.Node{
			ID:      "node-a",
			Shard:   "shard1",
			Replica: "replica1",
			Addr:    "127.0.0.1:9000",
		},
		DB: db,
	}

	return collector, client, mock
}

func mockPooledCollectorClient(t *testing.T) (*Collector, chclient.Client, sqlmock.Sqlmock) {
	t.Helper()

	collector, client, mock := mockCollectorClient(t)
	collector.pool = poolWithClients(client)

	return collector, client, mock
}

func poolWithClients(clients ...chclient.Client) *chclient.Pool {
	pool := &chclient.Pool{}
	field := reflect.ValueOf(pool).Elem().FieldByName("clients")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(clients))

	return pool
}

type sqlArrayConverter struct{}

func (sqlArrayConverter) ConvertValue(value any) (driver.Value, error) {
	switch value := value.(type) {
	case []string:
		return value, nil
	case []int64:
		return value, nil
	default:
		return driver.DefaultParameterConverter.ConvertValue(value)
	}
}

func queryContaining(fragment string) string {
	return "(?s).*" + regexp.QuoteMeta(fragment) + ".*"
}

func emptyRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"x"})
}

func diskColumns() []string {
	return []string{
		"name",
		"path",
		"cache_path",
		"free_space",
		"total_space",
		"unreserved_space",
		"type",
		"object_storage_type",
		"is_remote",
		"is_broken",
	}
}

func tableDefinitionRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"uuid",
		"engine",
		"storage_policy",
		"partition_key",
		"sorting_key",
		"primary_key",
		"sampling_key",
		"is_replicated",
	}).AddRow("uuid-1", "ReplicatedMergeTree", "tiered", "toYYYYMM(ts)", "ts", "ts", "", int64(1))
}

func tableSummaryRows() *sqlmock.Rows {
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	return sqlmock.NewRows([]string{
		"active_partitions",
		"active_parts",
		"rows",
		"bytes_on_disk",
		"min_partition",
		"max_partition",
		"last_modification_time",
	}).AddRow(int64(2), int64(5), int64(100), int64(4096), "202601", "202602", modified)
}

func columnColumns() []string {
	return []string{
		"name",
		"position",
		"type",
		"default_kind",
		"default_expression",
		"compression_codec",
		"comment",
		"is_in_partition_key",
		"is_in_sorting_key",
		"is_in_primary_key",
		"is_in_sampling_key",
	}
}

func partColumns() []string {
	return []string{
		"database",
		"table",
		"partition",
		"partition_id",
		"name",
		"uuid",
		"active",
		"disk_name",
		"path",
		"part_type",
		"rows",
		"marks",
		"bytes_on_disk",
		"data_compressed_bytes",
		"data_uncompressed_bytes",
		"marks_bytes",
		"primary_key_bytes_in_memory",
		"primary_key_bytes_in_memory_allocated",
		"secondary_indices_compressed_bytes",
		"secondary_indices_uncompressed_bytes",
		"secondary_indices_marks_bytes",
		"modification_time",
		"remove_time",
		"refcount",
		"min_block_number",
		"max_block_number",
		"level",
		"data_version",
		"delete_ttl_info_min",
		"delete_ttl_info_max",
		"default_compression_codec",
	}
}

func partRowValues(active any) []driver.Value {
	modified := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	return []driver.Value{
		"db",
		"tbl",
		"202602",
		"pid",
		"all_0_0_0",
		"part-uuid",
		active,
		"default",
		"/var/lib/clickhouse/data/db/tbl/all_0_0_0/",
		"Wide",
		int64(10),
		int64(2),
		int64(1024),
		int64(700),
		int64(1400),
		int64(40),
		int64(20),
		int64(30),
		int64(3),
		int64(6),
		int64(1),
		modified,
		time.Time{},
		int64(3),
		int64(0),
		int64(0),
		int64(1),
		int64(7),
		time.Time{},
		time.Time{},
		"LZ4",
	}
}

func detachedColumns() []string {
	return []string{
		"database",
		"table",
		"partition_id",
		"name",
		"disk",
		"reason",
		"path",
		"bytes_on_disk",
		"modification_time",
		"min_block_number",
		"max_block_number",
		"level",
	}
}

func mutationColumns() []string {
	return []string{
		"database",
		"table",
		"mutation_id",
		"command",
		"create_time",
		"is_done",
		"is_killed",
		"parts_to_do",
		"parts_to_do_names",
		"block_numbers.partition_id",
		"block_numbers.number",
		"latest_failed_part",
		"latest_fail_time",
		"latest_fail_reason",
	}
}

func mutationRowValues(isDone any) []driver.Value {
	return mutationRowValuesWithID("mutation_1.txt", isDone, "bad mutation")
}

func mutationRowValuesWithID(id string, isDone any, failReason string) []driver.Value {
	created := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	failedAt := created.Add(time.Minute)

	return []driver.Value{
		"db",
		"tbl",
		id,
		"DELETE WHERE bad = 1",
		created,
		isDone,
		int64(0),
		int64(1),
		[]string{"all_1_1_0"},
		[]string{"pid1"},
		[]int64{4},
		"all_1_1_0",
		failedAt,
		failReason,
	}
}

func replicationQueueColumns() []string {
	return []string{
		"database",
		"table",
		"replica_name",
		"position",
		"node_name",
		"type",
		"create_time",
		"required_quorum",
		"source_replica",
		"new_part_name",
		"parts_to_merge",
		"is_detach",
		"is_currently_executing",
		"num_tries",
		"last_attempt_time",
		"last_postpone_time",
		"num_postponed",
		"postpone_reason",
		"last_exception",
	}
}

func replicationQueueRowValues(position any) []driver.Value {
	return replicationQueueRowValuesWithPositionValue(position, "GET_PART", true, "all_2_2_0", "fetch failed", time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC))
}

func replicationQueueRowValuesWithPosition(position int64, itemType string, executing bool, newPartName string, exception string, created time.Time) []driver.Value {
	return replicationQueueRowValuesWithPositionValue(position, itemType, executing, newPartName, exception, created)
}

func replicationQueueRowValuesWithPositionValue(
	position any,
	itemType string,
	executing bool,
	newPartName string,
	exception string,
	created time.Time,
) []driver.Value {
	isExecuting := int64(0)
	if executing {
		isExecuting = 1
	}

	return []driver.Value{
		"db",
		"tbl",
		"replica1",
		position,
		"/clickhouse/task",
		itemType,
		created,
		int64(2),
		"replica2",
		newPartName,
		[]string{"all_1_1_0", newPartName},
		int64(0),
		isExecuting,
		int64(3),
		created.Add(time.Minute),
		time.Time{},
		int64(1),
		"",
		exception,
	}
}

func partEventColumns() []string {
	return []string{
		"database",
		"table",
		"partition",
		"partition_id",
		"part_name",
		"event_type",
		"event_time",
		"event_time_microseconds",
		"duration_ms",
		"rows",
		"size_in_bytes",
		"bytes_uncompressed",
		"read_rows",
		"read_bytes",
		"merged_from",
		"disk_name",
		"error",
		"exception",
	}
}

func partEventRowValues(duration any) []driver.Value {
	eventTime := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)

	return []driver.Value{
		"db",
		"tbl",
		"202606",
		"pid",
		"all_0_1_1",
		"MergeParts",
		eventTime,
		"2026-06-01 01:00:00.123456",
		duration,
		int64(50),
		int64(4096),
		int64(8192),
		int64(60),
		int64(2048),
		[]string{"all_0_0_0", "all_1_1_0"},
		"s3_cache",
		int64(0),
		"",
	}
}

func moveColumns() []string {
	return []string{
		"database",
		"table",
		"partition",
		"partition_id",
		"elapsed",
		"target_disk_name",
		"part_name",
		"part_size",
		"thread_id",
	}
}

func mergeColumns() []string {
	return []string{
		"database",
		"table",
		"partition",
		"partition_id",
		"elapsed",
		"progress",
		"result_part_name",
		"is_mutation",
		"total_size_bytes_compressed",
		"bytes_read_uncompressed",
	}
}
