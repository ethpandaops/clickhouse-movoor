package clusterstate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

// WatchValidationItem is the observed engine for one configured watch on one node.
type WatchValidationItem struct {
	Node   chclient.Node
	Watch  Watch
	Engine string
	Found  bool
}

// WatchValidationResult summarizes startup watch validation across configured
// nodes. Items are intentionally not exposed because callers should only need
// the health envelope; schema problems are returned as errors.
type WatchValidationResult struct {
	CollectedAt        time.Time
	CollectionDuration time.Duration
	NodesExpected      int
	NodesResponded     int
	NodesFailed        int
	Warnings           []Warning
}

// ValidateWatches verifies that configured watches point at physical
// MergeTree-family tables. Distributed tables are intentionally rejected for
// now because part movement must target local MergeTree storage.
func (c *Collector) ValidateWatches(ctx context.Context) ([]Warning, error) {
	result, err := c.ValidateWatchesDetailed(ctx)

	return result.Warnings, err
}

// ValidateWatchesDetailed is ValidateWatches plus the per-node validation
// summary used by startup health/readiness reporting.
func (c *Collector) ValidateWatchesDetailed(ctx context.Context) (WatchValidationResult, error) {
	result := collectPerNode(ctx, c, len(c.watches), func(ctx context.Context, client chclient.Client) ([]WatchValidationItem, *Warning) {
		items, err := c.collectWatchValidationItems(ctx, client)
		if err != nil {
			return nil, queryWarning(client.Node.ID, "system_tables_watch_validation_failed", err)
		}

		return items, nil
	})
	summary := WatchValidationResult{
		CollectedAt:        result.CollectedAt,
		CollectionDuration: result.CollectionDuration,
		NodesExpected:      result.NodesExpected,
		NodesResponded:     result.NodesResponded,
		NodesFailed:        result.NodesFailed,
		Warnings:           append([]Warning(nil), result.Warnings...),
	}
	if result.NodesResponded == 0 {
		return summary, errors.New("no ClickHouse node responded during watch validation")
	}

	var err error
	seenErrors := make(map[string]struct{})
	for _, item := range result.Items {
		if !item.Found {
			err = joinUniqueWatchValidationError(err, seenErrors,
				fmt.Sprintf("%s.%s: missing on responding node %s", item.Watch.Database, item.Watch.Table, item.Node.ID),
			)

			continue
		}
		if !isPhysicalMergeTreeEngine(item.Engine) {
			err = joinUniqueWatchValidationError(err, seenErrors,
				fmt.Sprintf("%s.%s: engine %q on node %s is not a physical MergeTree-family table", item.Watch.Database, item.Watch.Table, item.Engine, item.Node.ID),
			)
		}
	}

	return summary, err
}

// collectWatchValidationItems resolves every watch in a single system.tables
// query per node: one round-trip per watch does not fit inside one
// queryTimeout once the watch count grows past a few dozen.
func (c *Collector) collectWatchValidationItems(ctx context.Context, client chclient.Client) ([]WatchValidationItem, error) {
	if len(c.watches) == 0 {
		return nil, nil
	}

	predicate, args := c.watchPredicate("name")
	rows, err := client.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT database, name, engine
		FROM system.tables
		WHERE %s
	`, predicate), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	engines := make(map[Watch]string, len(c.watches))
	for rows.Next() {
		var table Watch
		var engine string
		if scanErr := rows.Scan(&table.Database, &table.Table, &engine); scanErr != nil {
			return nil, scanErr
		}
		engines[table] = engine
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}

	items := make([]WatchValidationItem, 0, len(c.watches))
	for _, watch := range c.watches {
		item := WatchValidationItem{
			Node:  client.Node,
			Watch: watch,
		}
		if engine, ok := engines[watch]; ok {
			item.Engine = engine
			item.Found = true
		}
		items = append(items, item)
	}

	return items, nil
}

func isPhysicalMergeTreeEngine(engine string) bool {
	return strings.HasSuffix(engine, "MergeTree")
}

func joinUniqueWatchValidationError(existing error, seen map[string]struct{}, message string) error {
	if _, ok := seen[message]; ok {
		return existing
	}
	seen[message] = struct{}{}

	return errors.Join(existing, errors.New(message))
}
