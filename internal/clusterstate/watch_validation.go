package clusterstate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

// WatchValidationItem is the observed engine for one configured watch on one node.
type WatchValidationItem struct {
	Node   chclient.Node
	Watch  Watch
	Engine string
	Found  bool
}

// ValidateWatches verifies that configured watches point at physical
// MergeTree-family tables. Distributed tables are intentionally rejected for
// now because part movement must target local MergeTree storage.
func (c *Collector) ValidateWatches(ctx context.Context) ([]Warning, error) {
	result := collectPerNode(ctx, c, len(c.watches), func(ctx context.Context, client chclient.Client) ([]WatchValidationItem, *Warning) {
		items, err := c.collectWatchValidationItems(ctx, client)
		if err != nil {
			return nil, queryWarning(client.Node.ID, "system_tables_watch_validation_failed", err)
		}

		return items, nil
	})
	if result.NodesResponded == 0 {
		return result.Warnings, errors.New("no ClickHouse node responded during watch validation")
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

	return result.Warnings, err
}

func (c *Collector) collectWatchValidationItems(ctx context.Context, client chclient.Client) ([]WatchValidationItem, error) {
	items := make([]WatchValidationItem, 0, len(c.watches))
	for _, watch := range c.watches {
		item := WatchValidationItem{
			Node:  client.Node,
			Watch: watch,
		}

		err := client.DB.QueryRowContext(ctx, `
			SELECT engine
			FROM system.tables
			WHERE database = ? AND name = ?
			LIMIT 1
		`, watch.Database, watch.Table).Scan(&item.Engine)
		if err != nil {
			if errorsIsNoRows(err) {
				items = append(items, item)

				continue
			}

			return nil, err
		}

		item.Found = true
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
