package clusterstate

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

const (
	warningKindReachability = "reachability"
	warningKindCapability   = "capability"
	warningKindQueryError   = "query_error"
)

// Collector gathers request-time state from every configured ClickHouse node.
type Collector struct {
	pool         *chclient.Pool
	queryTimeout time.Duration
	watches      []Watch
}

// Watch identifies one configured table that should be monitored.
type Watch struct {
	Database string
	Table    string
}

// Result is a per-request collection result. It permits partial success:
// callers get all successful node rows plus warnings for failed nodes.
type Result[T any] struct {
	CollectedAt        time.Time
	CollectionDuration time.Duration
	NodesExpected      int
	NodesResponded     int
	NodesFailed        int
	Warnings           []Warning
	Items              []T
}

// Partial reports whether one or more configured nodes failed to respond.
func (r Result[T]) Partial() bool {
	return r.NodesFailed > 0 || len(r.Warnings) > 0
}

// Warning describes a node-level collection issue.
type Warning struct {
	Kind    string
	Code    string
	Message string
	NodeID  string
}

// NodeStatus is coarse health and identity for one configured ClickHouse node.
type NodeStatus struct {
	Node          chclient.Node
	Reachable     bool
	ObservedAt    time.Time
	Version       string
	Timezone      string
	UptimeSeconds uint64
	LastError     string
}

// Disk is one row from system.disks, normalized for UI/API consumption.
type Disk struct {
	Node                 chclient.Node
	Name                 string
	Path                 string
	CachePath            string
	Type                 string
	ObjectStorageType    string
	IsRemote             bool
	IsBroken             bool
	CapacityKnown        bool
	FreeSpaceBytes       *uint64
	TotalSpaceBytes      *uint64
	UnreservedSpaceBytes *uint64
	UsedByActiveParts    uint64
}

// New constructs a collector using one client per configured node.
func New(pool *chclient.Pool, queryTimeout time.Duration, watches []Watch) *Collector {
	watchesCopy := make([]Watch, len(watches))
	copy(watchesCopy, watches)

	return &Collector{
		pool:         pool,
		queryTimeout: queryTimeout,
		watches:      watchesCopy,
	}
}

// watchPredicate returns a SQL fragment matching every configured watch plus
// its bind args, e.g. "(database, table) IN ((?, ?), (?, ?))". tableColumn
// names the table column: system.tables calls it name, the rest table.
// Callers must guard against an empty watch list.
func (c *Collector) watchPredicate(tableColumn string) (string, []any) {
	pairs := make([]string, 0, len(c.watches))
	args := make([]any, 0, len(c.watches)*2)
	for _, watch := range c.watches {
		pairs = append(pairs, "(?, ?)")
		args = append(args, watch.Database, watch.Table)
	}

	return fmt.Sprintf("(database, %s) IN (%s)", tableColumn, strings.Join(pairs, ", ")), args
}

// Watches returns the configured table watches.
func (c *Collector) Watches() []Watch {
	if c == nil {
		return nil
	}

	watches := make([]Watch, len(c.watches))
	copy(watches, c.watches)

	return watches
}

// CollectNodes checks every configured ClickHouse node.
func (c *Collector) CollectNodes(ctx context.Context) Result[NodeStatus] {
	start := time.Now()
	clients := c.clients()
	items := make([]NodeStatus, 0, len(clients))
	warnings := make([]Warning, 0)

	results := make(chan nodeResult, len(clients))
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Go(func() {
			results <- c.collectNode(ctx, client)
		})
	}

	wg.Wait()
	close(results)

	for result := range results {
		items = append(items, result.item)
		if result.warning != nil {
			warnings = append(warnings, *result.warning)
		}
	}

	return result(start, len(clients), len(items)-len(warnings), warnings, items)
}

// CollectDisks reads system.disks from every configured ClickHouse node.
func (c *Collector) CollectDisks(ctx context.Context) Result[Disk] {
	start := time.Now()
	clients := c.clients()
	items := make([]Disk, 0, len(clients)*3)
	warnings := make([]Warning, 0)

	type diskResult struct {
		items   []Disk
		warning *Warning
	}

	results := make(chan diskResult, len(clients))
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Go(func() {
			nodeDisks, warning := c.collectDisks(ctx, client)
			results <- diskResult{items: nodeDisks, warning: warning}
		})
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

func (c *Collector) collectNode(ctx context.Context, client chclient.Client) nodeResult {
	now := time.Now().UTC()
	item := NodeStatus{
		Node:       client.Node,
		ObservedAt: now,
	}

	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()

	err := client.DB.QueryRowContext(queryCtx, `
		SELECT
			version(),
			timezone(),
			toUInt64(uptime())
		FROM system.one
	`).Scan(&item.Version, &item.Timezone, &item.UptimeSeconds)
	if err != nil {
		item.Reachable = false
		item.LastError = err.Error()

		return nodeResult{
			item: item,
			warning: &Warning{
				Kind:    warningKindReachability,
				Code:    "node_unreachable",
				Message: err.Error(),
				NodeID:  client.Node.ID,
			},
		}
	}

	item.Reachable = true

	return nodeResult{item: item}
}

type nodeResult struct {
	item    NodeStatus
	warning *Warning
}

func (c *Collector) collectDisks(ctx context.Context, client chclient.Client) ([]Disk, *Warning) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()

	usedByDisk, err := c.collectWatchedPartBytesByDisk(queryCtx, client)
	if err != nil {
		return nil, queryWarning(client.Node.ID, "system_parts_disk_usage_query_failed", err)
	}

	rows, err := client.DB.QueryContext(queryCtx, `
		SELECT
			name,
			path,
			cache_path,
			free_space,
			total_space,
			unreserved_space,
			type,
			object_storage_type,
			is_remote,
			is_broken
		FROM system.disks
		ORDER BY name
	`)
	if err != nil {
		return nil, queryWarning(client.Node.ID, "system_disks_query_failed", err)
	}
	defer rows.Close()

	disks := make([]Disk, 0, 3)
	for rows.Next() {
		var (
			disk            Disk
			freeSpace       uint64
			totalSpace      uint64
			unreservedSpace uint64
			isRemote        uint8
			isBroken        uint8
		)
		if scanErr := rows.Scan(
			&disk.Name,
			&disk.Path,
			&disk.CachePath,
			&freeSpace,
			&totalSpace,
			&unreservedSpace,
			&disk.Type,
			&disk.ObjectStorageType,
			&isRemote,
			&isBroken,
		); scanErr != nil {
			return nil, &Warning{
				Kind:    warningKindQueryError,
				Code:    "system_disks_scan_failed",
				Message: scanErr.Error(),
				NodeID:  client.Node.ID,
			}
		}

		disk.Node = client.Node
		disk.IsRemote = isRemote != 0
		disk.IsBroken = isBroken != 0
		disk.UsedByActiveParts = usedByDisk[disk.Name]
		setDiskCapacity(&disk, freeSpace, totalSpace, unreservedSpace)
		disks = append(disks, disk)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, queryWarning(client.Node.ID, "system_disks_rows_failed", rowsErr)
	}

	return disks, nil
}

func (c *Collector) collectWatchedPartBytesByDisk(ctx context.Context, client chclient.Client) (map[string]uint64, error) {
	usedByDisk := make(map[string]uint64)
	if len(c.watches) == 0 {
		return usedByDisk, nil
	}

	predicate, args := c.watchPredicate("table")
	rows, err := client.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			disk_name,
			sum(bytes_on_disk)
		FROM system.parts
		WHERE %s AND active
		GROUP BY disk_name
	`, predicate), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			disk string
			used uint64
		)
		if scanErr := rows.Scan(&disk, &used); scanErr != nil {
			return nil, scanErr
		}
		usedByDisk[disk] += used
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}

	return usedByDisk, nil
}

func (c *Collector) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.queryTimeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, c.queryTimeout)
}

func (c *Collector) clients() []chclient.Client {
	if c == nil || c.pool == nil {
		return nil
	}

	return c.pool.Clients()
}

func result[T any](start time.Time, nodesExpected int, nodesResponded int, warnings []Warning, items []T) Result[T] {
	return Result[T]{
		CollectedAt:        start.UTC(),
		CollectionDuration: time.Since(start),
		NodesExpected:      nodesExpected,
		NodesResponded:     nodesResponded,
		NodesFailed:        nodesExpected - nodesResponded,
		Warnings:           warnings,
		Items:              items,
	}
}

func setDiskCapacity(disk *Disk, freeSpace uint64, totalSpace uint64, unreservedSpace uint64) {
	if freeSpace == math.MaxUint64 || totalSpace == math.MaxUint64 || unreservedSpace == math.MaxUint64 {
		disk.CapacityKnown = false

		return
	}

	disk.CapacityKnown = true
	disk.FreeSpaceBytes = new(freeSpace)
	disk.TotalSpaceBytes = new(totalSpace)
	disk.UnreservedSpaceBytes = new(unreservedSpace)
}
