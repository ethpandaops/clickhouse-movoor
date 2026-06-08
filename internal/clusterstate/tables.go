//nolint:govet,modernize,intrange // The system-table collectors keep ClickHouse scan loops explicit.
package clusterstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

const (
	severityCritical = "critical"
	severityWarning  = "warning"
	severityInfo     = "info"
)

// TableState is one configured table as observed on one ClickHouse node.
type TableState struct {
	Node                 chclient.Node
	Database             string
	Table                string
	UUID                 string
	Engine               string
	StoragePolicy        string
	PartitionKey         string
	SortingKey           string
	PrimaryKey           string
	SamplingKey          string
	IsReplicated         bool
	ActivePartitions     int
	ActiveParts          uint64
	Rows                 uint64
	BytesOnDisk          uint64
	MinPartition         *string
	MaxPartition         *string
	LastModificationTime *time.Time
	Replica              *ReplicaState
}

// ReplicaState is coarse replication state for a replicated table on one node.
type ReplicaState struct {
	Readonly             bool
	SessionExpired       bool
	QueueSize            uint64
	AbsoluteDelaySeconds uint64
	TotalReplicas        uint64
	ActiveReplicas       uint64
}

// NodeColumns is the column schema for one table on one node.
type NodeColumns struct {
	Node       chclient.Node
	Database   string
	Table      string
	Columns    []Column
	Conditions []Condition
}

// Column is one row from system.columns normalized for the API.
type Column struct {
	Name              string
	Position          uint64
	Type              string
	Kind              string
	DefaultKind       *string
	DefaultExpression *string
	CodecExpression   *string
	TTLExpression     *string
	Comment           string
	IsInPartitionKey  bool
	IsInSortingKey    bool
	IsInPrimaryKey    bool
	IsInSamplingKey   bool
}

// Part is one physical part from system.parts.
type Part struct {
	Node                              chclient.Node
	Database                          string
	Table                             string
	Partition                         string
	PartitionID                       string
	Name                              string
	UUID                              string
	Active                            bool
	Disk                              string
	Path                              string
	PartType                          string
	Rows                              uint64
	Marks                             uint64
	BytesOnDisk                       uint64
	DataCompressedBytes               uint64
	DataUncompressedBytes             uint64
	MarksBytes                        uint64
	PrimaryKeyBytesInMemory           uint64
	PrimaryKeyBytesInMemoryAllocated  uint64
	SecondaryIndicesCompressedBytes   uint64
	SecondaryIndicesUncompressedBytes uint64
	SecondaryIndicesMarksBytes        uint64
	ModificationTime                  time.Time
	RemoveTime                        *time.Time
	Refcount                          uint64
	MinBlockNumber                    int64
	MaxBlockNumber                    int64
	Level                             uint64
	DataVersion                       uint64
	DeleteTTLInfoMin                  *time.Time
	DeleteTTLInfoMax                  *time.Time
	DefaultCompressionCodec           string
	Conditions                        []Condition
}

// DetachedPart is one physical detached part from system.detached_parts.
type DetachedPart struct {
	Node             chclient.Node
	Database         string
	Table            string
	PartitionID      *string
	Name             string
	Disk             string
	Reason           *string
	Path             string
	BytesOnDisk      uint64
	Rows             uint64
	MinBlockNumber   *int64
	MaxBlockNumber   *int64
	Level            *uint64
	ModificationTime time.Time
	Conditions       []Condition
}

// Mutation is one mutation row from system.mutations.
type Mutation struct {
	Node             chclient.Node
	Database         string
	Table            string
	MutationID       string
	Command          string
	CreateTime       time.Time
	IsDone           bool
	IsKilled         bool
	PartsToDo        uint64
	PartsToDoNames   []string
	BlockNumbers     []MutationBlockNumber
	LatestFailedPart *string
	LatestFailTime   *time.Time
	LatestFailReason *string
	Conditions       []Condition
}

// MutationBlockNumber is one partition block number from system.mutations.
type MutationBlockNumber struct {
	PartitionID string
	Number      uint64
}

// ReplicationQueueItem is one row from system.replication_queue.
type ReplicationQueueItem struct {
	Node                 chclient.Node
	Database             string
	Table                string
	ReplicaName          string
	Position             uint64
	NodeName             string
	Type                 string
	CreateTime           time.Time
	RequiredQuorum       uint64
	SourceReplica        *string
	NewPartName          *string
	PartsToMerge         []string
	IsDetach             bool
	IsCurrentlyExecuting bool
	NumTries             uint64
	LastAttemptTime      *time.Time
	LastPostponeTime     *time.Time
	NumPostponed         uint64
	PostponeReason       *string
	LastException        *string
	Conditions           []Condition
}

// PartEvent is one immutable event from system.part_log.
type PartEvent struct {
	Node                chclient.Node
	Database            string
	Table               string
	Partition           string
	PartitionID         string
	PartName            string
	EventType           string
	EventTime           time.Time
	DurationMs          uint64
	Rows                uint64
	BytesCompressed     uint64
	BytesUncompressed   uint64
	ReadRows            uint64
	ReadBytes           uint64
	MergedFrom          []string
	SourceDisk          *string
	TargetDisk          *string
	Error               int64
	Exception           *string
	EventTimeMicrostamp string
}

// Operation is one in-flight or queued unit of ClickHouse work relevant to movoor.
type Operation struct {
	OperationID    string
	Kind           string
	NodeID         string
	Database       string
	Table          string
	Partition      *string
	PartitionID    *string
	AttemptID      string
	State          string
	ElapsedSeconds *float64
	Progress       *float64
	SourceDisk     *string
	TargetDisk     *string
	BytesTotal     *uint64
	BytesProcessed *uint64
	LatestMessage  *string
	StartedAt      *time.Time
}

// Condition is one flattened operator condition.
type Condition struct {
	ConditionID string
	Severity    string
	Code        string
	Message     string
	ObservedAt  time.Time
	Database    *string
	Table       *string
	Partition   *string
	PartitionID *string
	NodeID      *string
	Evidence    map[string]any
	Links       map[string]string
}

// CollectTables reads the configured watched tables from every node.
func (c *Collector) CollectTables(ctx context.Context) Result[TableState] {
	return collectPerNode(ctx, c, len(c.watches), func(ctx context.Context, client chclient.Client) ([]TableState, *Warning) {
		items := make([]TableState, 0, len(c.watches))
		for _, watch := range c.watches {
			item, ok, err := c.collectTableState(ctx, client, watch)
			if err != nil {
				return nil, queryWarning(client.Node.ID, "system_tables_query_failed", err)
			}
			if ok {
				items = append(items, item)
			}
		}

		return items, nil
	})
}

// CollectTableColumns reads system.columns for one table from every node.
func (c *Collector) CollectTableColumns(ctx context.Context, watch Watch) Result[NodeColumns] {
	return collectPerNode(ctx, c, 1, func(ctx context.Context, client chclient.Client) ([]NodeColumns, *Warning) {
		item, err := c.collectTableColumns(ctx, client, watch)
		if err != nil {
			return nil, queryWarning(client.Node.ID, "system_columns_query_failed", err)
		}
		if len(item.Columns) == 0 {
			return nil, nil
		}

		return []NodeColumns{item}, nil
	})
}

// CollectParts reads system.parts for one table from every node.
func (c *Collector) CollectParts(ctx context.Context, watch Watch) Result[Part] {
	return collectPerNode(ctx, c, 32, func(ctx context.Context, client chclient.Client) ([]Part, *Warning) {
		items, err := c.collectParts(ctx, client, watch)
		if err != nil {
			return nil, queryWarning(client.Node.ID, "system_parts_query_failed", err)
		}

		return items, nil
	})
}

// CollectDetachedParts reads system.detached_parts for one table from every node.
func (c *Collector) CollectDetachedParts(ctx context.Context, watch Watch) Result[DetachedPart] {
	return collectPerNode(ctx, c, 4, func(ctx context.Context, client chclient.Client) ([]DetachedPart, *Warning) {
		items, err := c.collectDetachedParts(ctx, client, watch)
		if err != nil {
			return nil, queryWarning(client.Node.ID, "system_detached_parts_query_failed", err)
		}

		return items, nil
	})
}

// CollectMutations reads system.mutations for configured watches from every node.
func (c *Collector) CollectMutations(ctx context.Context) Result[Mutation] {
	return collectPerNode(ctx, c, len(c.watches), func(ctx context.Context, client chclient.Client) ([]Mutation, *Warning) {
		items := make([]Mutation, 0)
		for _, watch := range c.watches {
			watchItems, err := c.collectMutations(ctx, client, watch)
			if err != nil {
				return nil, queryWarning(client.Node.ID, "system_mutations_query_failed", err)
			}
			items = append(items, watchItems...)
		}

		return items, nil
	})
}

// CollectReplicationQueue reads system.replication_queue for configured watches from every node.
func (c *Collector) CollectReplicationQueue(ctx context.Context) Result[ReplicationQueueItem] {
	return collectPerNode(ctx, c, len(c.watches), func(ctx context.Context, client chclient.Client) ([]ReplicationQueueItem, *Warning) {
		items := make([]ReplicationQueueItem, 0)
		for _, watch := range c.watches {
			watchItems, err := c.collectReplicationQueue(ctx, client, watch)
			if err != nil {
				return nil, queryWarning(client.Node.ID, "system_replication_queue_query_failed", err)
			}
			items = append(items, watchItems...)
		}

		return items, nil
	})
}

// CollectPartEvents reads recent system.part_log entries for configured watches from every node.
func (c *Collector) CollectPartEvents(ctx context.Context) Result[PartEvent] {
	return collectPerNode(ctx, c, 64, func(ctx context.Context, client chclient.Client) ([]PartEvent, *Warning) {
		items := make([]PartEvent, 0)
		for _, watch := range c.watches {
			watchItems, warning, err := c.collectPartEvents(ctx, client, watch)
			if warning != nil || err != nil {
				if warning != nil {
					return nil, warning
				}

				return nil, queryWarning(client.Node.ID, "system_part_log_query_failed", err)
			}
			items = append(items, watchItems...)
		}

		return items, nil
	})
}

// CollectOperations reads in-flight moves, merges, mutations, and replication queue work.
//
//nolint:gocognit // This keeps the operation sources visible in one collection pass.
func (c *Collector) CollectOperations(ctx context.Context) Result[Operation] {
	return collectPerNode(ctx, c, 16, func(ctx context.Context, client chclient.Client) ([]Operation, *Warning) {
		items := make([]Operation, 0)
		moves, err := c.collectMoveOperations(ctx, client)
		if err != nil {
			return nil, queryWarning(client.Node.ID, "system_moves_query_failed", err)
		}
		items = append(items, moves...)

		merges, err := c.collectMergeOperations(ctx, client)
		if err != nil {
			return nil, queryWarning(client.Node.ID, "system_merges_query_failed", err)
		}
		items = append(items, merges...)

		for _, watch := range c.watches {
			mutations, err := c.collectMutations(ctx, client, watch)
			if err != nil {
				return nil, queryWarning(client.Node.ID, "system_mutations_query_failed", err)
			}
			for _, mutation := range mutations {
				if mutation.IsDone {
					continue
				}
				items = append(items, operationFromMutation(mutation))
			}

			queueItems, err := c.collectReplicationQueue(ctx, client, watch)
			if err != nil {
				return nil, queryWarning(client.Node.ID, "system_replication_queue_query_failed", err)
			}
			for _, queueItem := range queueItems {
				items = append(items, operationFromReplicationQueue(queueItem))
			}
		}

		return items, nil
	})
}

// CollectConditions derives operator conditions from the configured watches.
func (c *Collector) CollectConditions(ctx context.Context) Result[Condition] {
	start := time.Now()
	nodesExpected := len(c.clients())
	conditions := make([]Condition, 0)
	warnings := make([]Warning, 0)

	tables := c.CollectTables(ctx)
	warnings = appendWarnings(warnings, tables.Warnings...)
	conditions = append(conditions, c.tableConditions(tables.Items)...)

	for _, watch := range c.watches {
		parts := c.CollectParts(ctx, watch)
		warnings = appendWarnings(warnings, parts.Warnings...)
		conditions = append(conditions, partitionConditions(parts.Items)...)

		detached := c.CollectDetachedParts(ctx, watch)
		warnings = appendWarnings(warnings, detached.Warnings...)
		for _, part := range detached.Items {
			conditions = append(conditions, detachedPartCondition(part))
		}
	}

	mutations := c.CollectMutations(ctx)
	warnings = appendWarnings(warnings, mutations.Warnings...)
	for _, mutation := range mutations.Items {
		if mutation.LatestFailReason != nil && *mutation.LatestFailReason != "" {
			conditions = append(conditions, mutationFailureCondition(mutation))
		}
	}

	replicationQueue := c.CollectReplicationQueue(ctx)
	warnings = appendWarnings(warnings, replicationQueue.Warnings...)
	for _, item := range replicationQueue.Items {
		if item.LastException != nil && *item.LastException != "" {
			conditions = append(conditions, replicationQueueExceptionCondition(item))
		}
	}

	for _, warning := range warnings {
		conditions = append(conditions, collectionWarningCondition(warning, start.UTC()))
	}

	return result(start, nodesExpected, respondedNodes(nodesExpected, warnings), warnings, conditions)
}

func (c *Collector) collectTableState(ctx context.Context, client chclient.Client, watch Watch) (TableState, bool, error) {
	var (
		item         TableState
		isReplicated uint8
	)
	err := client.DB.QueryRowContext(ctx, `
		SELECT
			toString(uuid),
			engine,
			storage_policy,
			partition_key,
			sorting_key,
			primary_key,
			sampling_key,
			if(startsWith(engine, 'Replicated'), 1, 0)
		FROM system.tables
		WHERE database = ? AND name = ?
	`, watch.Database, watch.Table).Scan(
		&item.UUID,
		&item.Engine,
		&item.StoragePolicy,
		&item.PartitionKey,
		&item.SortingKey,
		&item.PrimaryKey,
		&item.SamplingKey,
		&isReplicated,
	)
	if err != nil {
		if errorsIsNoRows(err) {
			return TableState{}, false, nil
		}

		return TableState{}, false, err
	}

	item.Node = client.Node
	item.Database = watch.Database
	item.Table = watch.Table
	item.IsReplicated = isReplicated != 0

	var (
		minPartition sql.NullString
		maxPartition sql.NullString
		lastModified sql.NullTime
	)
	err = client.DB.QueryRowContext(ctx, `
		SELECT
			countDistinct(partition_id),
			count(),
			coalesce(sum(rows), 0),
			coalesce(sum(bytes_on_disk), 0),
			min(partition),
			max(partition),
			max(modification_time)
		FROM system.parts
		WHERE database = ? AND table = ? AND active
	`, watch.Database, watch.Table).Scan(
		&item.ActivePartitions,
		&item.ActiveParts,
		&item.Rows,
		&item.BytesOnDisk,
		&minPartition,
		&maxPartition,
		&lastModified,
	)
	if err != nil {
		return TableState{}, false, err
	}
	item.MinPartition = nullableStringPtr(minPartition)
	item.MaxPartition = nullableStringPtr(maxPartition)
	item.LastModificationTime = nullableTimePtr(lastModified)

	replica, err := c.collectReplicaState(ctx, client, watch)
	if err != nil {
		return TableState{}, false, err
	}
	item.Replica = replica

	return item, true, nil
}

func (c *Collector) collectReplicaState(ctx context.Context, client chclient.Client, watch Watch) (*ReplicaState, error) {
	var (
		replica        ReplicaState
		readonly       uint8
		sessionExpired uint8
		queueSize      uint32
		absoluteDelay  uint64
		totalReplicas  uint32
		activeReplicas uint32
	)
	err := client.DB.QueryRowContext(ctx, `
		SELECT
			is_readonly,
			is_session_expired,
			queue_size,
			absolute_delay,
			total_replicas,
			active_replicas
		FROM system.replicas
		WHERE database = ? AND table = ?
		LIMIT 1
	`, watch.Database, watch.Table).Scan(
		&readonly,
		&sessionExpired,
		&queueSize,
		&absoluteDelay,
		&totalReplicas,
		&activeReplicas,
	)
	if err != nil {
		if errorsIsNoRows(err) {
			return nil, nil
		}

		return nil, err
	}

	replica.Readonly = readonly != 0
	replica.SessionExpired = sessionExpired != 0
	replica.QueueSize = uint64(queueSize)
	replica.AbsoluteDelaySeconds = absoluteDelay
	replica.TotalReplicas = uint64(totalReplicas)
	replica.ActiveReplicas = uint64(activeReplicas)

	return &replica, nil
}

func (c *Collector) collectTableColumns(ctx context.Context, client chclient.Client, watch Watch) (NodeColumns, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			name,
			position,
			type,
			default_kind,
			default_expression,
			compression_codec,
			comment,
			is_in_partition_key,
			is_in_sorting_key,
			is_in_primary_key,
			is_in_sampling_key
		FROM system.columns
		WHERE database = ? AND table = ?
		ORDER BY position
	`, watch.Database, watch.Table)
	if err != nil {
		return NodeColumns{}, err
	}
	defer rows.Close()

	item := NodeColumns{
		Node:     client.Node,
		Database: watch.Database,
		Table:    watch.Table,
		Columns:  make([]Column, 0),
	}
	for rows.Next() {
		var (
			column       Column
			defaultKind  string
			defaultExpr  string
			codecExpr    string
			partitionKey uint8
			sortingKey   uint8
			primaryKey   uint8
			samplingKey  uint8
		)
		if err := rows.Scan(
			&column.Name,
			&column.Position,
			&column.Type,
			&defaultKind,
			&defaultExpr,
			&codecExpr,
			&column.Comment,
			&partitionKey,
			&sortingKey,
			&primaryKey,
			&samplingKey,
		); err != nil {
			return NodeColumns{}, err
		}

		column.Kind = columnKind(defaultKind)
		column.DefaultKind = nonEmptyStringPtr(defaultKind)
		column.DefaultExpression = nonEmptyStringPtr(defaultExpr)
		column.CodecExpression = nonEmptyStringPtr(codecExpr)
		column.IsInPartitionKey = partitionKey != 0
		column.IsInSortingKey = sortingKey != 0
		column.IsInPrimaryKey = primaryKey != 0
		column.IsInSamplingKey = samplingKey != 0
		item.Columns = append(item.Columns, column)
	}
	if err := rows.Err(); err != nil {
		return NodeColumns{}, err
	}

	return item, nil
}

//nolint:funlen // system.parts has many fields; keeping the scan in one place avoids lossy mapping.
func (c *Collector) collectParts(ctx context.Context, client chclient.Client, watch Watch) ([]Part, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			database,
			table,
			partition,
			partition_id,
			name,
			toString(uuid),
			active,
			disk_name,
			path,
			part_type,
			rows,
			marks,
			bytes_on_disk,
			data_compressed_bytes,
			data_uncompressed_bytes,
			marks_bytes,
			primary_key_bytes_in_memory,
			primary_key_bytes_in_memory_allocated,
			secondary_indices_compressed_bytes,
			secondary_indices_uncompressed_bytes,
			secondary_indices_marks_bytes,
			modification_time,
			remove_time,
			refcount,
			min_block_number,
			max_block_number,
			level,
			data_version,
			delete_ttl_info_min,
			delete_ttl_info_max,
			default_compression_codec
		FROM system.parts
		WHERE database = ? AND table = ?
		ORDER BY active DESC, partition_id, name
	`, watch.Database, watch.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Part, 0)
	for rows.Next() {
		var (
			part             Part
			active           uint8
			removeTime       time.Time
			refcount         uint32
			level            uint32
			deleteTTLInfoMin time.Time
			deleteTTLInfoMax time.Time
		)
		if err := rows.Scan(
			&part.Database,
			&part.Table,
			&part.Partition,
			&part.PartitionID,
			&part.Name,
			&part.UUID,
			&active,
			&part.Disk,
			&part.Path,
			&part.PartType,
			&part.Rows,
			&part.Marks,
			&part.BytesOnDisk,
			&part.DataCompressedBytes,
			&part.DataUncompressedBytes,
			&part.MarksBytes,
			&part.PrimaryKeyBytesInMemory,
			&part.PrimaryKeyBytesInMemoryAllocated,
			&part.SecondaryIndicesCompressedBytes,
			&part.SecondaryIndicesUncompressedBytes,
			&part.SecondaryIndicesMarksBytes,
			&part.ModificationTime,
			&removeTime,
			&refcount,
			&part.MinBlockNumber,
			&part.MaxBlockNumber,
			&level,
			&part.DataVersion,
			&deleteTTLInfoMin,
			&deleteTTLInfoMax,
			&part.DefaultCompressionCodec,
		); err != nil {
			return nil, err
		}
		part.Node = client.Node
		part.Active = active != 0
		part.RemoveTime = nonZeroTimePtr(removeTime)
		part.Refcount = uint64(refcount)
		part.Level = uint64(level)
		part.DeleteTTLInfoMin = nonZeroTimePtr(deleteTTLInfoMin)
		part.DeleteTTLInfoMax = nonZeroTimePtr(deleteTTLInfoMax)
		items = append(items, part)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (c *Collector) collectDetachedParts(ctx context.Context, client chclient.Client, watch Watch) ([]DetachedPart, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			database,
			table,
			partition_id,
			name,
			disk,
			reason,
			path,
			bytes_on_disk,
			modification_time,
			min_block_number,
			max_block_number,
			level
		FROM system.detached_parts
		WHERE database = ? AND table = ?
		ORDER BY partition_id, name
	`, watch.Database, watch.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]DetachedPart, 0)
	for rows.Next() {
		var (
			part           DetachedPart
			partitionID    sql.NullString
			reason         sql.NullString
			minBlockNumber sql.NullInt64
			maxBlockNumber sql.NullInt64
			level          sql.NullInt64
		)
		if err := rows.Scan(
			&part.Database,
			&part.Table,
			&partitionID,
			&part.Name,
			&part.Disk,
			&reason,
			&part.Path,
			&part.BytesOnDisk,
			&part.ModificationTime,
			&minBlockNumber,
			&maxBlockNumber,
			&level,
		); err != nil {
			return nil, err
		}
		part.Node = client.Node
		part.PartitionID = nullableStringPtr(partitionID)
		part.Reason = nullableStringPtr(reason)
		part.MinBlockNumber = nullableInt64Ptr(minBlockNumber)
		part.MaxBlockNumber = nullableInt64Ptr(maxBlockNumber)
		part.Level = nullableUint64Ptr(level)
		items = append(items, part)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (c *Collector) collectMutations(ctx context.Context, client chclient.Client, watch Watch) ([]Mutation, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			database,
			table,
			mutation_id,
			command,
			create_time,
			is_done,
			is_killed,
			parts_to_do,
			parts_to_do_names,
			`+"`block_numbers.partition_id`"+`,
			`+"`block_numbers.number`"+`,
			latest_failed_part,
			latest_fail_time,
			latest_fail_reason
		FROM system.mutations
		WHERE database = ? AND table = ?
		ORDER BY create_time DESC, mutation_id
	`, watch.Database, watch.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Mutation, 0)
	for rows.Next() {
		var (
			mutation         Mutation
			isDone           uint8
			isKilled         uint8
			partsToDo        int64
			partitionIDs     []string
			blockNumbers     []int64
			latestFailedPart string
			latestFailTime   time.Time
			latestFailReason string
		)
		if err := rows.Scan(
			&mutation.Database,
			&mutation.Table,
			&mutation.MutationID,
			&mutation.Command,
			&mutation.CreateTime,
			&isDone,
			&isKilled,
			&partsToDo,
			&mutation.PartsToDoNames,
			&partitionIDs,
			&blockNumbers,
			&latestFailedPart,
			&latestFailTime,
			&latestFailReason,
		); err != nil {
			return nil, err
		}
		mutation.Node = client.Node
		mutation.IsDone = isDone != 0
		mutation.IsKilled = isKilled != 0
		mutation.PartsToDo = uint64FromInt64(partsToDo)
		mutation.BlockNumbers = mutationBlockNumbers(partitionIDs, blockNumbers)
		mutation.LatestFailedPart = nonEmptyStringPtr(latestFailedPart)
		mutation.LatestFailTime = nonZeroTimePtr(latestFailTime)
		mutation.LatestFailReason = nonEmptyStringPtr(latestFailReason)
		if mutation.LatestFailReason != nil {
			mutation.Conditions = append(mutation.Conditions, mutationFailureCondition(mutation))
		}
		items = append(items, mutation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (c *Collector) collectReplicationQueue(ctx context.Context, client chclient.Client, watch Watch) ([]ReplicationQueueItem, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			database,
			table,
			replica_name,
			position,
			node_name,
			type,
			create_time,
			required_quorum,
			source_replica,
			new_part_name,
			parts_to_merge,
			is_detach,
			is_currently_executing,
			num_tries,
			last_attempt_time,
			last_postpone_time,
			num_postponed,
			postpone_reason,
			last_exception
		FROM system.replication_queue
		WHERE database = ? AND table = ?
		ORDER BY position
	`, watch.Database, watch.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]ReplicationQueueItem, 0)
	for rows.Next() {
		var (
			item                 ReplicationQueueItem
			position             uint32
			requiredQuorum       uint32
			sourceReplica        string
			newPartName          string
			isDetach             uint8
			isCurrentlyExecuting uint8
			numTries             uint32
			lastAttemptTime      time.Time
			lastPostponeTime     time.Time
			numPostponed         uint32
			postponeReason       string
			lastException        string
		)
		if err := rows.Scan(
			&item.Database,
			&item.Table,
			&item.ReplicaName,
			&position,
			&item.NodeName,
			&item.Type,
			&item.CreateTime,
			&requiredQuorum,
			&sourceReplica,
			&newPartName,
			&item.PartsToMerge,
			&isDetach,
			&isCurrentlyExecuting,
			&numTries,
			&lastAttemptTime,
			&lastPostponeTime,
			&numPostponed,
			&postponeReason,
			&lastException,
		); err != nil {
			return nil, err
		}
		item.Node = client.Node
		item.Position = uint64(position)
		item.RequiredQuorum = uint64(requiredQuorum)
		item.SourceReplica = nonEmptyStringPtr(sourceReplica)
		item.NewPartName = nonEmptyStringPtr(newPartName)
		item.IsDetach = isDetach != 0
		item.IsCurrentlyExecuting = isCurrentlyExecuting != 0
		item.NumTries = uint64(numTries)
		item.LastAttemptTime = nonZeroTimePtr(lastAttemptTime)
		item.LastPostponeTime = nonZeroTimePtr(lastPostponeTime)
		item.NumPostponed = uint64(numPostponed)
		item.PostponeReason = nonEmptyStringPtr(postponeReason)
		item.LastException = nonEmptyStringPtr(lastException)
		if item.LastException != nil {
			item.Conditions = append(item.Conditions, replicationQueueExceptionCondition(item))
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (c *Collector) collectPartEvents(ctx context.Context, client chclient.Client, watch Watch) ([]PartEvent, *Warning, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			database,
			table,
			partition,
			partition_id,
			part_name,
			toString(event_type),
			event_time,
			toString(event_time_microseconds),
			duration_ms,
			rows,
			size_in_bytes,
			bytes_uncompressed,
			read_rows,
			read_bytes,
			merged_from,
			disk_name,
			error,
			exception
		FROM system.part_log
		WHERE database = ? AND table = ?
		ORDER BY event_time_microseconds DESC
		LIMIT 1000
	`, watch.Database, watch.Table)
	if err != nil {
		if strings.Contains(err.Error(), "UNKNOWN_TABLE") || strings.Contains(err.Error(), "system.part_log") {
			return nil, &Warning{
				Kind:    warningKindCapability,
				Code:    "part_log_unavailable",
				Message: err.Error(),
				NodeID:  client.Node.ID,
			}, nil
		}

		return nil, nil, err
	}
	defer rows.Close()

	items := make([]PartEvent, 0)
	for rows.Next() {
		var (
			event     PartEvent
			diskName  string
			errorCode uint16
			exception string
		)
		if err := rows.Scan(
			&event.Database,
			&event.Table,
			&event.Partition,
			&event.PartitionID,
			&event.PartName,
			&event.EventType,
			&event.EventTime,
			&event.EventTimeMicrostamp,
			&event.DurationMs,
			&event.Rows,
			&event.BytesCompressed,
			&event.BytesUncompressed,
			&event.ReadRows,
			&event.ReadBytes,
			&event.MergedFrom,
			&diskName,
			&errorCode,
			&exception,
		); err != nil {
			return nil, nil, err
		}
		event.Node = client.Node
		event.TargetDisk = nonEmptyStringPtr(diskName)
		event.Error = int64(errorCode)
		event.Exception = nonEmptyStringPtr(exception)
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return items, nil, nil
}

func (c *Collector) collectMoveOperations(ctx context.Context, client chclient.Client) ([]Operation, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			m.database,
			m.table,
			p.partition,
			p.partition_id,
			m.elapsed,
			m.target_disk_name,
			m.part_name,
			m.part_size,
			m.thread_id
		FROM system.moves AS m
		LEFT JOIN system.parts AS p
			ON p.database = m.database
			AND p.table = m.table
			AND p.name = m.part_name
			AND p.active
		ORDER BY m.database, m.table, m.part_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Operation, 0)
	for rows.Next() {
		var (
			database    string
			table       string
			partition   sql.NullString
			partitionID sql.NullString
			elapsed     float64
			targetDisk  string
			partName    string
			partSize    uint64
			threadID    uint64
		)
		if err := rows.Scan(&database, &table, &partition, &partitionID, &elapsed, &targetDisk, &partName, &partSize, &threadID); err != nil {
			return nil, err
		}
		if !c.isWatched(database, table) {
			continue
		}

		attemptID := fmt.Sprintf("%s:%d", partName, threadID)
		items = append(items, Operation{
			OperationID:    opaqueID("move", client.Node.ID, database, table, attemptID),
			Kind:           "move",
			NodeID:         client.Node.ID,
			Database:       database,
			Table:          table,
			Partition:      nullableStringPtr(partition),
			PartitionID:    nullableStringPtr(partitionID),
			AttemptID:      attemptID,
			State:          "running",
			ElapsedSeconds: &elapsed,
			TargetDisk:     nonEmptyStringPtr(targetDisk),
			BytesTotal:     &partSize,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (c *Collector) collectMergeOperations(ctx context.Context, client chclient.Client) ([]Operation, error) {
	rows, err := client.DB.QueryContext(ctx, `
		SELECT
			database,
			table,
			partition,
			partition_id,
			elapsed,
			progress,
			result_part_name,
			is_mutation,
			total_size_bytes_compressed,
			bytes_read_uncompressed
		FROM system.merges
		ORDER BY database, table, result_part_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Operation, 0)
	for rows.Next() {
		var (
			database       string
			table          string
			partition      string
			partitionID    string
			elapsed        float64
			progress       float64
			resultPartName string
			isMutation     uint8
			totalBytes     uint64
			processedBytes uint64
		)
		if err := rows.Scan(
			&database,
			&table,
			&partition,
			&partitionID,
			&elapsed,
			&progress,
			&resultPartName,
			&isMutation,
			&totalBytes,
			&processedBytes,
		); err != nil {
			return nil, err
		}
		if !c.isWatched(database, table) {
			continue
		}

		kind := "merge"
		if isMutation != 0 {
			kind = "mutation"
		}
		items = append(items, Operation{
			OperationID:    opaqueID(kind, client.Node.ID, database, table, resultPartName),
			Kind:           kind,
			NodeID:         client.Node.ID,
			Database:       database,
			Table:          table,
			Partition:      &partition,
			PartitionID:    &partitionID,
			AttemptID:      resultPartName,
			State:          "running",
			ElapsedSeconds: &elapsed,
			Progress:       &progress,
			BytesTotal:     &totalBytes,
			BytesProcessed: &processedBytes,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func operationFromMutation(mutation Mutation) Operation {
	attemptID := mutation.MutationID
	message := mutation.LatestFailReason

	return Operation{
		OperationID:   opaqueID("mutation", mutation.Node.ID, mutation.Database, mutation.Table, attemptID),
		Kind:          "mutation",
		NodeID:        mutation.Node.ID,
		Database:      mutation.Database,
		Table:         mutation.Table,
		AttemptID:     attemptID,
		State:         "running",
		LatestMessage: message,
		StartedAt:     &mutation.CreateTime,
	}
}

func operationFromReplicationQueue(item ReplicationQueueItem) Operation {
	kind := "replication_queue"
	if item.IsCurrentlyExecuting && (item.Type == "GET_PART" || item.Type == "ATTACH_PART") {
		kind = "fetch"
	}

	state := "queued"
	if item.IsCurrentlyExecuting {
		state = "running"
	}
	attemptID := fmt.Sprintf("%d:%s:%s", item.Position, item.Type, derefString(item.NewPartName))

	return Operation{
		OperationID:   opaqueID(kind, item.Node.ID, item.Database, item.Table, attemptID),
		Kind:          kind,
		NodeID:        item.Node.ID,
		Database:      item.Database,
		Table:         item.Table,
		AttemptID:     attemptID,
		State:         state,
		LatestMessage: firstNonNilString(item.LastException, item.PostponeReason),
		StartedAt:     &item.CreateTime,
	}
}

func collectPerNode[T any](
	ctx context.Context,
	c *Collector,
	perNodeCapacity int,
	fn func(context.Context, chclient.Client) ([]T, *Warning),
) Result[T] {
	start := time.Now()
	clients := c.clients()
	items := make([]T, 0, len(clients)*perNodeCapacity)
	warnings := make([]Warning, 0)

	type nodeItems struct {
		items   []T
		warning *Warning
	}

	results := make(chan nodeItems, len(clients))
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()

			queryCtx, cancel := c.queryContext(ctx)
			defer cancel()

			items, warning := fn(queryCtx, client)
			results <- nodeItems{items: items, warning: warning}
		}()
	}

	wg.Wait()
	close(results)

	for result := range results {
		items = append(items, result.items...)
		if result.warning != nil {
			warnings = append(warnings, *result.warning)
		}
	}

	return result(start, len(clients), len(clients)-len(warnings), warnings, items)
}

func queryWarning(nodeID string, code string, err error) *Warning {
	if isReachabilityError(err) {
		return &Warning{
			Kind:    warningKindReachability,
			Code:    "node_unreachable",
			Message: err.Error(),
			NodeID:  nodeID,
		}
	}

	return &Warning{
		Kind:    warningKindQueryError,
		Code:    code,
		Message: err.Error(),
		NodeID:  nodeID,
	}
}

func isReachabilityError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connect: connection refused",
		"connection refused",
		"i/o timeout",
		"no such host",
		"connection reset",
		"broken pipe",
		"unexpected eof",
	} {
		if strings.Contains(message, needle) {
			return true
		}
	}

	return false
}

func (c *Collector) isWatched(database string, table string) bool {
	for _, watch := range c.watches {
		if watch.Database == database && watch.Table == table {
			return true
		}
	}

	return false
}

func (c *Collector) tableConditions(items []TableState) []Condition {
	observed := make(map[string]TableState, len(items))
	for _, item := range items {
		observed[item.Node.ID+"/"+item.Database+"/"+item.Table] = item
	}

	conditions := make([]Condition, 0)
	for _, client := range c.clients() {
		for _, watch := range c.watches {
			key := client.Node.ID + "/" + watch.Database + "/" + watch.Table
			if _, ok := observed[key]; ok {
				continue
			}
			database := watch.Database
			table := watch.Table
			nodeID := client.Node.ID
			conditions = append(conditions, Condition{
				ConditionID: opaqueID("table_missing_on_node", nodeID, database, table, ""),
				Severity:    severityCritical,
				Code:        "table_missing_on_node",
				Message:     "watched table was not observed on configured node",
				ObservedAt:  time.Now().UTC(),
				Database:    &database,
				Table:       &table,
				NodeID:      &nodeID,
			})
		}
	}

	return conditions
}

//nolint:gocognit,funlen // Replica/disk comparison is intentionally explicit for operator evidence.
func partitionConditions(parts []Part) []Condition {
	type nodeDiskSet struct {
		shard   string
		replica string
		disks   map[string]struct{}
		parts   int
	}
	type partitionKey struct {
		database    string
		table       string
		partition   string
		partitionID string
	}

	byPartition := make(map[partitionKey][]nodeDiskSet)
	for _, part := range parts {
		if !part.Active {
			continue
		}
		key := partitionKey{
			database:    part.Database,
			table:       part.Table,
			partition:   part.Partition,
			partitionID: part.PartitionID,
		}
		nodes := byPartition[key]
		idx := -1
		for i := range nodes {
			if nodes[i].shard == part.Node.Shard && nodes[i].replica == part.Node.Replica {
				idx = i
				break
			}
		}
		if idx == -1 {
			nodes = append(nodes, nodeDiskSet{
				shard:   part.Node.Shard,
				replica: part.Node.Replica,
				disks:   make(map[string]struct{}),
			})
			idx = len(nodes) - 1
		}
		nodes[idx].disks[part.Disk] = struct{}{}
		nodes[idx].parts++
		byPartition[key] = nodes
	}

	conditions := make([]Condition, 0)
	for key, nodes := range byPartition {
		partitionDiskCount := make(map[string]struct{})
		for _, node := range nodes {
			for disk := range node.disks {
				partitionDiskCount[disk] = struct{}{}
			}
		}
		if len(partitionDiskCount) > 1 {
			database := key.database
			table := key.table
			partition := key.partition
			partitionID := key.partitionID
			conditions = append(conditions, Condition{
				ConditionID: opaqueID("partition_split_across_disks", database, table, partitionID, ""),
				Severity:    severityInfo,
				Code:        "partition_split_across_disks",
				Message:     "partition has active parts on more than one disk",
				ObservedAt:  time.Now().UTC(),
				Database:    &database,
				Table:       &table,
				Partition:   &partition,
				PartitionID: &partitionID,
				Evidence: map[string]any{
					"disks": sortedKeys(partitionDiskCount),
				},
			})
		}

		byShard := make(map[string]string)
		for _, node := range nodes {
			signature := diskSignature(node.disks) + ":" + strconv.Itoa(node.parts)
			if existing, ok := byShard[node.shard]; ok && existing != signature {
				database := key.database
				table := key.table
				partition := key.partition
				partitionID := key.partitionID
				conditions = append(conditions, Condition{
					ConditionID: opaqueID("replica_part_mismatch", database, table, partitionID, node.shard),
					Severity:    severityWarning,
					Code:        "replica_part_mismatch",
					Message:     "replicas in the same shard report different disk or part state",
					ObservedAt:  time.Now().UTC(),
					Database:    &database,
					Table:       &table,
					Partition:   &partition,
					PartitionID: &partitionID,
					Evidence: map[string]any{
						"shard": node.shard,
					},
				})
				break
			}
			byShard[node.shard] = signature
		}
	}

	return conditions
}

func detachedPartCondition(part DetachedPart) Condition {
	database := part.Database
	table := part.Table
	nodeID := part.Node.ID
	partitionID := derefString(part.PartitionID)
	reason := derefString(part.Reason)

	return Condition{
		ConditionID: opaqueID("detached_part_present", nodeID, database, table, part.Name),
		Severity:    severityWarning,
		Code:        "detached_part_present",
		Message:     "detached part is present",
		ObservedAt:  time.Now().UTC(),
		Database:    &database,
		Table:       &table,
		PartitionID: &partitionID,
		NodeID:      &nodeID,
		Evidence: map[string]any{
			"partName": part.Name,
			"disk":     part.Disk,
			"reason":   reason,
		},
	}
}

func mutationFailureCondition(mutation Mutation) Condition {
	database := mutation.Database
	table := mutation.Table
	nodeID := mutation.Node.ID
	message := "mutation has a latest failure"
	if mutation.LatestFailReason != nil {
		message = *mutation.LatestFailReason
	}

	return Condition{
		ConditionID: opaqueID("mutation_failed", nodeID, database, table, mutation.MutationID),
		Severity:    severityWarning,
		Code:        "mutation_failed",
		Message:     message,
		ObservedAt:  time.Now().UTC(),
		Database:    &database,
		Table:       &table,
		NodeID:      &nodeID,
		Evidence: map[string]any{
			"mutationId": mutation.MutationID,
		},
	}
}

func replicationQueueExceptionCondition(item ReplicationQueueItem) Condition {
	database := item.Database
	table := item.Table
	nodeID := item.Node.ID
	message := "replication queue item has a latest exception"
	if item.LastException != nil {
		message = *item.LastException
	}

	return Condition{
		ConditionID: opaqueID("replication_queue_exception", nodeID, database, table, item.NodeName),
		Severity:    severityWarning,
		Code:        "replication_queue_exception",
		Message:     message,
		ObservedAt:  time.Now().UTC(),
		Database:    &database,
		Table:       &table,
		NodeID:      &nodeID,
		Evidence: map[string]any{
			"type":     item.Type,
			"position": item.Position,
		},
	}
}

func collectionWarningCondition(warning Warning, observedAt time.Time) Condition {
	nodeID := warning.NodeID

	return Condition{
		ConditionID: opaqueID("collection_warning", warning.Kind, warning.Code, nodeID, warning.Message),
		Severity:    severityWarning,
		Code:        warning.Code,
		Message:     warning.Message,
		ObservedAt:  observedAt,
		NodeID:      nonEmptyStringPtr(nodeID),
		Evidence: map[string]any{
			"kind": warning.Kind,
		},
	}
}

func appendWarnings(existing []Warning, next ...Warning) []Warning {
	seen := make(map[string]struct{}, len(existing)+len(next))
	for _, warning := range existing {
		seen[warningKey(warning)] = struct{}{}
	}

	for _, warning := range next {
		key := warningKey(warning)
		if _, ok := seen[key]; ok {
			continue
		}
		existing = append(existing, warning)
		seen[key] = struct{}{}
	}

	return existing
}

func respondedNodes(nodesExpected int, warnings []Warning) int {
	unreachable := make(map[string]struct{})
	for _, warning := range warnings {
		if warning.Kind == warningKindReachability && warning.NodeID != "" {
			unreachable[warning.NodeID] = struct{}{}
		}
	}

	return nodesExpected - len(unreachable)
}

func warningKey(warning Warning) string {
	return warning.Kind + "\x00" + warning.Code + "\x00" + warning.NodeID + "\x00" + warning.Message
}

func mutationBlockNumbers(partitionIDs []string, numbers []int64) []MutationBlockNumber {
	limit := len(partitionIDs)
	if len(numbers) < limit {
		limit = len(numbers)
	}

	blockNumbers := make([]MutationBlockNumber, 0, limit)
	for i := 0; i < limit; i++ {
		blockNumbers = append(blockNumbers, MutationBlockNumber{
			PartitionID: partitionIDs[i],
			Number:      uint64FromInt64(numbers[i]),
		})
	}

	return blockNumbers
}

func columnKind(defaultKind string) string {
	switch strings.ToUpper(defaultKind) {
	case "MATERIALIZED":
		return "materialized"
	case "ALIAS":
		return "alias"
	default:
		return "regular"
	}
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func nullableStringPtr(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}

	return &value.String
}

func nullableTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return nonZeroTimePtr(value.Time)
}

func nullableInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}

	return &value.Int64
}

func nullableUint64Ptr(value sql.NullInt64) *uint64 {
	if !value.Valid || value.Int64 < 0 {
		return nil
	}

	converted := uint64(value.Int64)

	return &converted
}

func nonEmptyStringPtr(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func nonZeroTimePtr(value time.Time) *time.Time {
	if value.IsZero() || value.Unix() == 0 {
		return nil
	}

	utc := value.UTC()

	return &utc
}

func uint64FromInt64(value int64) uint64 {
	if value < 0 {
		return 0
	}

	return uint64(value)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func firstNonNilString(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}

	return nil
}

func opaqueID(parts ...string) string {
	return strings.Join(parts, "|")
}

func diskSignature(disks map[string]struct{}) string {
	return strings.Join(sortedKeys(disks), ",")
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)

	return keys
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
