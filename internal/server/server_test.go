package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/server"
)

func TestServer(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('hi')")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, newFakeState())

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	base := "http://" + srv.Addr()

	for _, tt := range serverTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+tt.path, nil)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			require.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantContentType != "" {
				require.Equal(t, tt.wantContentType, resp.Header.Get("Content-Type"))
			}
			if tt.wantJSONBody != "" {
				require.JSONEq(t, tt.wantJSONBody, string(body))

				return
			}

			require.Equal(t, tt.wantBody, strings.TrimSpace(string(body)))
		})
	}
}

type serverTestCase struct {
	name            string
	path            string
	wantStatus      int
	wantContentType string
	wantBody        string
	wantJSONBody    string
}

//nolint:funlen // Table of HTTP test cases; length is inherent to the fixture data.
func serverTestCases() []serverTestCase {
	return []serverTestCase{
		{
			name:            "health endpoint",
			path:            "/api/v1/healthz",
			wantStatus:      http.StatusOK,
			wantContentType: "application/json",
			wantBody:        `{"status":"ok"}`,
		},
		{
			name:            "nodes endpoint",
			path:            "/api/v1/nodes",
			wantStatus:      http.StatusOK,
			wantContentType: "application/json",
			wantJSONBody: `{
				"collection": {
					"collectedAt": "2026-01-01T00:00:00Z",
					"partial": false,
					"collectionDurationMs": 12,
					"nodesExpected": 2,
					"nodesResponded": 2,
					"nodesFailed": 0,
					"warnings": []
				},
				"items": [
					{
						"nodeId": "node-a",
						"shard": "shard1",
						"replica": "replica1",
						"endpoint": "clickhouse://127.0.0.1:9000",
						"reachable": true,
						"observedAt": "2026-01-01T00:00:01Z",
						"version": "26.2.5.45",
						"timezone": "UTC",
						"uptimeSeconds": "3842",
						"lastError": null
					},
					{
						"nodeId": "node-b",
						"shard": "shard1",
						"replica": "replica2",
						"endpoint": "clickhouse://127.0.0.1:9001",
						"reachable": false,
						"observedAt": "2026-01-01T00:00:02Z",
						"lastError": "dial timeout"
					}
				]
			}`,
		},
		{
			name:            "disk endpoint normalizes remote capacity",
			path:            "/api/v1/storage/disks?disk=s3_cache",
			wantStatus:      http.StatusOK,
			wantContentType: "application/json",
			wantJSONBody: `{
				"collection": {
					"collectedAt": "2026-01-01T00:00:00Z",
					"partial": false,
					"collectionDurationMs": 12,
					"nodesExpected": 2,
					"nodesResponded": 2,
					"nodesFailed": 0,
					"warnings": []
				},
				"items": [
					{
						"nodeId": "node-a",
						"shard": "shard1",
						"replica": "replica1",
						"disk": "s3_cache",
						"type": "ObjectStorage",
						"objectStorageType": "S3",
						"isRemote": true,
						"isBroken": false,
						"path": "/var/lib/clickhouse/disks/s3/",
						"cachePath": "/var/lib/clickhouse/disks/s3_cache/",
						"capacityKnown": false,
						"freeSpaceBytes": null,
						"totalSpaceBytes": null,
						"unreservedSpaceBytes": null,
						"usedByActivePartsBytes": "456"
					}
				]
			}`,
		},
		{
			name:            "open node filter miss returns empty result",
			path:            "/api/v1/nodes?nodeId=missing-node",
			wantStatus:      http.StatusOK,
			wantContentType: "application/json",
			wantJSONBody: `{
				"collection": {
					"collectedAt": "2026-01-01T00:00:00Z",
					"partial": false,
					"collectionDurationMs": 12,
					"nodesExpected": 2,
					"nodesResponded": 2,
					"nodesFailed": 0,
					"warnings": []
				},
				"items": []
			}`,
		},
		{
			name:            "open disk filters miss returns empty result",
			path:            "/api/v1/storage/disks?disk=missing-disk&type=ObjectStorage",
			wantStatus:      http.StatusOK,
			wantContentType: "application/json",
			wantJSONBody: `{
				"collection": {
					"collectedAt": "2026-01-01T00:00:00Z",
					"partial": false,
					"collectionDurationMs": 12,
					"nodesExpected": 2,
					"nodesResponded": 2,
					"nodesFailed": 0,
					"warnings": []
				},
				"items": []
			}`,
		},
		{
			name:            "invalid boolean returns problem json",
			path:            "/api/v1/storage/disks?broken=lol",
			wantStatus:      http.StatusBadRequest,
			wantContentType: "application/problem+json",
			wantJSONBody: `{
				"type": "about:blank",
				"title": "Bad Request",
				"status": 400,
				"detail": "must be a boolean",
				"instance": "/api/v1/storage/disks?broken=lol",
				"errors": [{"parameter": "broken", "detail": "must be a boolean"}]
			}`,
		},
		{
			name:            "unknown api route returns problem json",
			path:            "/api/v1/not-real",
			wantStatus:      http.StatusNotFound,
			wantContentType: "application/problem+json",
			wantJSONBody: `{
				"type": "about:blank",
				"title": "Not Found",
				"status": 404,
				"detail": "API route is not implemented",
				"instance": "/api/v1/not-real"
			}`,
		},
		{
			name:       "static asset served directly",
			path:       "/assets/app.js",
			wantStatus: http.StatusOK,
			wantBody:   "console.log('hi')",
		},
		{
			name:       "unknown path falls back to index",
			path:       "/some/client/route",
			wantStatus: http.StatusOK,
			wantBody:   "<!doctype html><title>test</title>",
		},
	}
}

func TestServerNoRespondingNodes(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, &noResponderState{fakeState: newFakeState()})

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	for _, path := range apiReadPaths() {
		t.Run(path, func(t *testing.T) {
			got := getJSON(t, "http://"+srv.Addr()+path, http.StatusServiceUnavailable)
			require.Equal(t, "no configured ClickHouse node responded", got["detail"])
		})
	}
}

func TestServerStateNotConfigured(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, nil)

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	for _, path := range apiReadPaths() {
		t.Run(path, func(t *testing.T) {
			got := getJSON(t, "http://"+srv.Addr()+path, http.StatusServiceUnavailable)
			require.Equal(t, "cluster state collector is not configured", got["detail"])
		})
	}
}

func TestServerUnobservedWatchedTable(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, &unobservedTableState{fakeState: newFakeState()})

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	got := getJSON(t, "http://"+srv.Addr()+"/api/v1/tables/movoor_dev/test_generic_network_month_local", http.StatusNotFound)
	require.Equal(t, "watched table was not observed", got["detail"])
}

func TestServerLifecycleAndSPAErrors(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	require.NoError(t, server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, fstest.MapFS{}, nil).Stop(t.Context()))
	require.ErrorContains(t, server.New(log, server.Config{ListenAddress: "127.0.0.1:bad"}, fstest.MapFS{}, nil).Start(t.Context()), "listen on")

	tests := []struct {
		name       string
		webFS      fs.FS
		wantStatus int
		wantBody   string
	}{
		{
			name:       "missing index",
			webFS:      fstest.MapFS{},
			wantStatus: http.StatusNotFound,
			wantBody:   "web UI not built",
		},
		{
			name:       "index stat error",
			webFS:      singleFileFS{file: &statErrorFile{reader: strings.NewReader("index")}},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "web UI unavailable",
		},
		{
			name:       "index is not seekable",
			webFS:      singleFileFS{file: &nonSeekFile{reader: strings.NewReader("index")}},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "web UI unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, tt.webFS, nil)
			require.NoError(t, srv.Start(t.Context()))
			t.Cleanup(func() {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer stopCancel()
				require.NoError(t, srv.Stop(stopCtx))
			})

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+srv.Addr()+"/", nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			require.Equal(t, tt.wantStatus, resp.StatusCode)
			require.Contains(t, string(body), tt.wantBody)
		})
	}
}

//nolint:funlen // Endpoint cases stay together to share one server fixture.
func TestServerReadAPIEndpoints(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, newFakeState())

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	base := "http://" + srv.Addr()
	tests := []struct {
		name   string
		path   string
		assert func(*testing.T, map[string]any)
	}{
		{
			name: "tables list aggregates watched table",
			path: "/api/v1/tables?hasConditions=true",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				require.Len(t, items, 1)
				item := jsonMap(t, items[0])
				require.Equal(t, "movoor_dev", item["database"])
				require.Equal(t, "test_generic_network_month_local", item["table"])
				require.Equal(t, "2", item["activeParts"])
				require.Len(t, jsonList(t, item["conditions"]), 1)
			},
		},
		{
			name: "table detail includes replica state",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local",
			assert: func(t *testing.T, got map[string]any) {
				item := jsonMap(t, got["item"])
				require.Equal(t, "uuid-a", item["uuid"])
				require.EqualValues(t, 2, item["nodesObserved"])
				nodes := jsonList(t, item["nodes"])
				require.Len(t, nodes, 2)
				require.Equal(t, "1", jsonMap(t, jsonMap(t, nodes[0])["replica"])["queueSize"])
			},
		},
		{
			name: "columns can filter by kind",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/columns?name=network_id",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				columns := jsonList(t, jsonMap(t, items[0])["columns"])
				require.Len(t, columns, 1)
				require.Equal(t, "network_id", jsonMap(t, columns[0])["name"])
			},
		},
		{
			name: "partitions include placement aggregates and conditions",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions?disk=default&hasConditions=true",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				require.Len(t, items, 1)
				item := jsonMap(t, items[0])
				require.Equal(t, "p-mainnet-202601", item["partitionId"])
				require.Equal(t, "2", item["activeParts"])
				require.Len(t, jsonList(t, item["placements"]), 2)
				require.Len(t, jsonList(t, item["conditions"]), 1)
			},
		},
		{
			name: "partitions filter by node placement",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions?partitionId=p-mainnet-202601&nodeId=node-b&shard=shard1&replica=b",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
			},
		},
		{
			name: "partitions filter miss by operation",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions?operation=moving",
			assert: func(t *testing.T, got map[string]any) {
				require.Empty(t, jsonItems(t, got))
			},
		},
		{
			name: "parts filter by byte range",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?active=true&minBytesOnDisk=100&maxBytesOnDisk=1000",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				require.Len(t, items, 1)
				require.Equal(t, "part-a", jsonMap(t, items[0])["partName"])
				require.Equal(t, "100", jsonMap(t, items[0])["bytesOnDisk"])
			},
		},
		{
			name: "parts filter by node and name",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?nodeId=node-a&shard=shard1&replica=a&partitionId=p-mainnet-202601&partName=part-a&disk=default",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
			},
		},
		{
			name: "detached parts count reasons",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts?reason=broken",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
				counts := jsonMap(t, got["counts"])
				require.EqualValues(t, 1, counts["total"])
				require.EqualValues(t, 1, jsonMap(t, counts["byReason"])["broken"])
			},
		},
		{
			name: "detached parts filter by identity",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts?nodeId=node-a&partitionId=p-mainnet-202601&partName=detached-a&disk=default",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
			},
		},
		{
			name: "operations filter by kind",
			path: "/api/v1/operations?kind=move",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
				counts := jsonMap(t, got["counts"])
				require.EqualValues(t, 1, counts["total"])
				require.EqualValues(t, 1, jsonMap(t, counts["byKind"])["move"])
				require.EqualValues(t, 0, jsonMap(t, counts["byKind"])["merge"])
			},
		},
		{
			name: "operations filter by scope",
			path: "/api/v1/operations?nodeId=node-a&database=movoor_dev&table=test_generic_network_month_local&partitionId=p-mainnet-202601",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
			},
		},
		{
			name: "mutations filter failures",
			path: "/api/v1/operations/mutations?done=false&failed=true",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
				counts := jsonMap(t, got["counts"])
				require.EqualValues(t, 1, counts["unfinished"])
				require.EqualValues(t, 1, counts["failed"])
			},
		},
		{
			name: "mutations filter by id and scope",
			path: "/api/v1/operations/mutations?nodeId=node-a&database=movoor_dev&table=test_generic_network_month_local&mutationId=0000000000",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
			},
		},
		{
			name: "replication queue filters executing exceptions",
			path: "/api/v1/operations/replication-queue?currentlyExecuting=true&hasException=true",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
				counts := jsonMap(t, got["counts"])
				require.EqualValues(t, 1, counts["currentlyExecuting"])
				require.EqualValues(t, 1, counts["withException"])
				require.EqualValues(t, 1, jsonMap(t, counts["byType"])["GET_PART"])
			},
		},
		{
			name: "replication queue filters by type and scope",
			path: "/api/v1/operations/replication-queue?nodeId=node-a&database=movoor_dev&table=test_generic_network_month_local&type=GET_PART",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
			},
		},
		{
			name: "part events filter window and event type",
			path: "/api/v1/part-events?from=2026-01-01T00:00:00Z&to=2026-01-01T00:00:10Z&eventType=MovePart",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
				counts := jsonMap(t, got["counts"])
				require.EqualValues(t, 1, counts["withErrors"])
				require.EqualValues(t, 1, jsonMap(t, counts["byEventType"])["MovePart"])
			},
		},
		{
			name: "part events filter by scope",
			path: "/api/v1/part-events?nodeId=node-a&database=movoor_dev&table=test_generic_network_month_local&partitionId=p-mainnet-202601&partName=part-a",
			assert: func(t *testing.T, got map[string]any) {
				require.Len(t, jsonItems(t, got), 1)
			},
		},
		{
			name: "conditions filter severity",
			path: "/api/v1/conditions?severity=critical",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				require.Len(t, items, 1)
				require.Equal(t, "stuck_mutation", jsonMap(t, items[0])["code"])
				require.EqualValues(t, 1, jsonMap(t, jsonMap(t, got["counts"])["bySeverity"])["critical"])
			},
		},
		{
			name: "conditions filter by code and scope",
			path: "/api/v1/conditions?code=split_partition&database=movoor_dev&table=test_generic_network_month_local&partitionId=p-mainnet-202601&nodeId=node-a",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				require.Len(t, items, 1)
				require.Equal(t, "split_partition", jsonMap(t, items[0])["code"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getJSON(t, base+tt.path, http.StatusOK)
			tt.assert(t, got)
		})
	}
}

func TestServerAPIFilterMisses(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, newFakeState())

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	base := "http://" + srv.Addr()
	emptyItems := func(t *testing.T, got map[string]any) {
		t.Helper()
		require.Empty(t, jsonItems(t, got))
	}
	tests := []struct {
		name   string
		path   string
		assert func(*testing.T, map[string]any)
	}{
		{name: "disks node miss", path: "/api/v1/storage/disks?nodeId=missing", assert: emptyItems},
		{name: "disks type miss", path: "/api/v1/storage/disks?type=Missing", assert: emptyItems},
		{name: "disks broken miss", path: "/api/v1/storage/disks?broken=true", assert: emptyItems},
		{name: "tables database miss", path: "/api/v1/tables?database=missing", assert: emptyItems},
		{name: "tables table miss", path: "/api/v1/tables?table=missing", assert: emptyItems},
		{name: "tables engine miss", path: "/api/v1/tables?engine=MergeTree", assert: emptyItems},
		{name: "tables storage policy miss", path: "/api/v1/tables?storagePolicy=missing", assert: emptyItems},
		{name: "tables has partitions miss", path: "/api/v1/tables?hasPartitions=false", assert: emptyItems},
		{name: "tables has conditions miss", path: "/api/v1/tables?hasConditions=false", assert: emptyItems},
		{name: "columns node miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/columns?nodeId=missing", assert: emptyItems},
		{
			name: "columns kind miss",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/columns?kind=missing",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				require.Len(t, items, 1)
				require.Empty(t, jsonList(t, jsonMap(t, items[0])["columns"]))
			},
		},
		{name: "partitions has conditions miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions?hasConditions=false", assert: emptyItems},
		{name: "parts node miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?nodeId=missing", assert: emptyItems},
		{name: "parts partition miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?partitionId=missing", assert: emptyItems},
		{name: "parts name miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?partName=missing", assert: emptyItems},
		{name: "parts disk miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?disk=s3", assert: emptyItems},
		{name: "parts min bytes miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?minBytesOnDisk=1000", assert: emptyItems},
		{name: "parts max bytes miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?maxBytesOnDisk=10", assert: emptyItems},
		{
			name: "parts active false skips active part",
			path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?active=false",
			assert: func(t *testing.T, got map[string]any) {
				items := jsonItems(t, got)
				require.Len(t, items, 1)
				require.Equal(t, "part-old", jsonMap(t, items[0])["partName"])
			},
		},
		{name: "detached node miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts?nodeId=missing", assert: emptyItems},
		{name: "detached partition miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts?partitionId=missing", assert: emptyItems},
		{name: "detached name miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts?partName=missing", assert: emptyItems},
		{name: "detached disk miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts?disk=s3", assert: emptyItems},
		{name: "detached reason miss", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts?reason=missing", assert: emptyItems},
		{name: "operations kind miss", path: "/api/v1/operations?kind=merge", assert: emptyItems},
		{name: "operations node miss", path: "/api/v1/operations?nodeId=missing", assert: emptyItems},
		{name: "operations database miss", path: "/api/v1/operations?database=missing", assert: emptyItems},
		{name: "operations table miss", path: "/api/v1/operations?table=missing", assert: emptyItems},
		{name: "operations partition miss", path: "/api/v1/operations?partitionId=missing", assert: emptyItems},
		{name: "mutations node miss", path: "/api/v1/operations/mutations?nodeId=missing", assert: emptyItems},
		{name: "mutations database miss", path: "/api/v1/operations/mutations?database=missing", assert: emptyItems},
		{name: "mutations table miss", path: "/api/v1/operations/mutations?table=missing", assert: emptyItems},
		{name: "mutations id miss", path: "/api/v1/operations/mutations?mutationId=missing", assert: emptyItems},
		{name: "mutations done miss", path: "/api/v1/operations/mutations?done=true", assert: emptyItems},
		{name: "mutations failed miss", path: "/api/v1/operations/mutations?failed=false", assert: emptyItems},
		{name: "replication queue node miss", path: "/api/v1/operations/replication-queue?nodeId=missing", assert: emptyItems},
		{name: "replication queue database miss", path: "/api/v1/operations/replication-queue?database=missing", assert: emptyItems},
		{name: "replication queue table miss", path: "/api/v1/operations/replication-queue?table=missing", assert: emptyItems},
		{name: "replication queue type miss", path: "/api/v1/operations/replication-queue?type=MERGE_PARTS", assert: emptyItems},
		{name: "replication queue executing miss", path: "/api/v1/operations/replication-queue?currentlyExecuting=false", assert: emptyItems},
		{name: "replication queue exception miss", path: "/api/v1/operations/replication-queue?hasException=false", assert: emptyItems},
		{name: "part events node miss", path: "/api/v1/part-events?nodeId=missing", assert: emptyItems},
		{name: "part events database miss", path: "/api/v1/part-events?database=missing", assert: emptyItems},
		{name: "part events table miss", path: "/api/v1/part-events?table=missing", assert: emptyItems},
		{name: "part events partition miss", path: "/api/v1/part-events?partitionId=missing", assert: emptyItems},
		{name: "part events name miss", path: "/api/v1/part-events?partName=missing", assert: emptyItems},
		{name: "part events type miss", path: "/api/v1/part-events?eventType=MergeParts", assert: emptyItems},
		{name: "part events from miss", path: "/api/v1/part-events?from=2026-01-01T00:00:06Z", assert: emptyItems},
		{name: "part events to miss", path: "/api/v1/part-events?to=2026-01-01T00:00:04Z", assert: emptyItems},
		{name: "conditions node miss", path: "/api/v1/conditions?nodeId=missing", assert: emptyItems},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assert(t, getJSON(t, base+tt.path, http.StatusOK))
		})
	}
}

func TestServerBadParametersAndMissingWatches(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, newFakeState())

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	base := "http://" + srv.Addr()
	tests := []struct {
		name   string
		path   string
		status int
		detail string
	}{
		{name: "unknown watch", path: "/api/v1/tables/movoor_dev/not_watched", status: http.StatusNotFound, detail: "table is not configured as a watch"},
		{name: "bad placement enum", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions?placement=nope", status: http.StatusBadRequest, detail: "unsupported value"},
		{name: "bad partitions boolean", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions?hasConditions=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad tables boolean", path: "/api/v1/tables?hasPartitions=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad tables conditions boolean", path: "/api/v1/tables?hasConditions=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad min bytes", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?minBytesOnDisk=lol", status: http.StatusBadRequest, detail: "must be an unsigned integer"},
		{name: "bad max bytes", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?maxBytesOnDisk=lol", status: http.StatusBadRequest, detail: "must be an unsigned integer"},
		{name: "bad active boolean", path: "/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?active=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad operation kind", path: "/api/v1/operations?kind=nope", status: http.StatusBadRequest, detail: "unsupported value"},
		{name: "bad mutation done", path: "/api/v1/operations/mutations?done=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad mutation failed", path: "/api/v1/operations/mutations?failed=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad replication executing", path: "/api/v1/operations/replication-queue?currentlyExecuting=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad replication exception", path: "/api/v1/operations/replication-queue?hasException=lol", status: http.StatusBadRequest, detail: "must be a boolean"},
		{name: "bad part event time", path: "/api/v1/part-events?from=not-time", status: http.StatusBadRequest, detail: "must be an RFC3339 timestamp"},
		{name: "bad part event to time", path: "/api/v1/part-events?to=not-time", status: http.StatusBadRequest, detail: "must be an RFC3339 timestamp"},
		{name: "bad condition severity", path: "/api/v1/conditions?severity=nope", status: http.StatusBadRequest, detail: "unsupported value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getJSON(t, base+tt.path, tt.status)
			if tt.status == http.StatusBadRequest {
				errs := jsonList(t, got["errors"])
				require.Equal(t, tt.detail, jsonMap(t, errs[0])["detail"])

				return
			}
			require.Equal(t, tt.detail, got["detail"])
		})
	}
}

func TestServerSortsMultiItemResponses(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS, &sortState{fakeState: newFakeState()})

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	base := "http://" + srv.Addr()
	for _, path := range []string{
		"/api/v1/storage/disks",
		"/api/v1/tables",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/columns",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/parts",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts",
		"/api/v1/operations",
		"/api/v1/operations/mutations",
		"/api/v1/operations/replication-queue",
		"/api/v1/part-events",
		"/api/v1/conditions",
	} {
		t.Run(path, func(t *testing.T) {
			got := getJSON(t, base+path, http.StatusOK)
			require.GreaterOrEqual(t, len(jsonItems(t, got)), 2)
		})
	}
}

func apiReadPaths() []string {
	return []string{
		"/api/v1/nodes",
		"/api/v1/storage/disks",
		"/api/v1/tables",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/columns",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/parts",
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts",
		"/api/v1/operations",
		"/api/v1/operations/mutations",
		"/api/v1/operations/replication-queue",
		"/api/v1/part-events",
		"/api/v1/conditions",
	}
}

func getJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, wantStatus, resp.StatusCode, string(body))

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got), string(body))

	return got
}

func jsonItems(t *testing.T, got map[string]any) []any {
	t.Helper()

	return jsonList(t, got["items"])
}

func jsonList(t *testing.T, value any) []any {
	t.Helper()

	items, ok := value.([]any)
	require.True(t, ok, "value is not an array in %#v", value)

	return items
}

func jsonMap(t *testing.T, value any) map[string]any {
	t.Helper()

	item, ok := value.(map[string]any)
	require.True(t, ok, "value is not an object in %#v", value)

	return item
}

type fakeState struct {
	now time.Time
}

type noResponderState struct {
	*fakeState
}

type unobservedTableState struct {
	*fakeState
}

type sortState struct {
	*fakeState
}

func newFakeState() *fakeState {
	return &fakeState{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeState) Watches() []clusterstate.Watch {
	return []clusterstate.Watch{{Database: "movoor_dev", Table: "test_generic_network_month_local"}}
}

func (f *fakeState) CollectNodes(context.Context) clusterstate.Result[clusterstate.NodeStatus] {
	return fakeResult(f.now, []clusterstate.NodeStatus{
		{
			Node: chclient.Node{
				ID:      "node-a",
				Shard:   "shard1",
				Replica: "replica1",
				Addr:    "127.0.0.1:9000",
			},
			Reachable:     true,
			ObservedAt:    f.now.Add(time.Second),
			Version:       "26.2.5.45",
			Timezone:      "UTC",
			UptimeSeconds: 3842,
		},
		{
			Node: chclient.Node{
				ID:      "node-b",
				Shard:   "shard1",
				Replica: "replica2",
				Addr:    "127.0.0.1:9001",
			},
			Reachable:  false,
			ObservedAt: f.now.Add(2 * time.Second),
			LastError:  "dial timeout",
		},
	})
}

func (f *noResponderState) CollectNodes(context.Context) clusterstate.Result[clusterstate.NodeStatus] {
	return noResponderResult[clusterstate.NodeStatus](f.now)
}

func (f *noResponderState) CollectDisks(context.Context) clusterstate.Result[clusterstate.Disk] {
	return noResponderResult[clusterstate.Disk](f.now)
}

func (f *noResponderState) CollectTables(context.Context) clusterstate.Result[clusterstate.TableState] {
	return noResponderResult[clusterstate.TableState](f.now)
}

func (f *noResponderState) CollectTableColumns(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.NodeColumns] {
	return noResponderResult[clusterstate.NodeColumns](f.now)
}

func (f *noResponderState) CollectParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	return noResponderResult[clusterstate.Part](f.now)
}

func (f *noResponderState) CollectActiveParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	return noResponderResult[clusterstate.Part](f.now)
}

func (f *noResponderState) CollectDetachedParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.DetachedPart] {
	return noResponderResult[clusterstate.DetachedPart](f.now)
}

func (f *noResponderState) CollectMutations(context.Context) clusterstate.Result[clusterstate.Mutation] {
	return noResponderResult[clusterstate.Mutation](f.now)
}

func (f *noResponderState) CollectReplicationQueue(context.Context) clusterstate.Result[clusterstate.ReplicationQueueItem] {
	return noResponderResult[clusterstate.ReplicationQueueItem](f.now)
}

func (f *noResponderState) CollectPartEvents(context.Context, *time.Time, *time.Time) clusterstate.Result[clusterstate.PartEvent] {
	return noResponderResult[clusterstate.PartEvent](f.now)
}

func (f *noResponderState) CollectOperations(context.Context) clusterstate.Result[clusterstate.Operation] {
	return noResponderResult[clusterstate.Operation](f.now)
}

func (f *noResponderState) CollectConditions(context.Context) clusterstate.Result[clusterstate.Condition] {
	return noResponderResult[clusterstate.Condition](f.now)
}

func noResponderResult[T any](now time.Time) clusterstate.Result[T] {
	return clusterstate.Result[T]{
		CollectedAt:        now,
		CollectionDuration: 12 * time.Millisecond,
		NodesExpected:      2,
		NodesResponded:     0,
		NodesFailed:        2,
		Warnings: []clusterstate.Warning{
			{Kind: "reachability", Code: "node_unreachable", Message: "dial timeout", NodeID: "node-a"},
			{Kind: "reachability", Code: "node_unreachable", Message: "dial timeout", NodeID: "node-b"},
		},
	}
}

func (f *unobservedTableState) CollectTables(context.Context) clusterstate.Result[clusterstate.TableState] {
	return fakeResult(f.now, []clusterstate.TableState(nil))
}

func (f *fakeState) CollectDisks(context.Context) clusterstate.Result[clusterstate.Disk] {
	return fakeResult(f.now, []clusterstate.Disk{
		{
			Node: chclient.Node{
				ID:      "node-a",
				Shard:   "shard1",
				Replica: "replica1",
			},
			Name:                 "default",
			Path:                 "/var/lib/clickhouse/",
			Type:                 "Local",
			CapacityKnown:        true,
			FreeSpaceBytes:       new(uint64(1000)),
			TotalSpaceBytes:      new(uint64(2000)),
			UnreservedSpaceBytes: new(uint64(900)),
			UsedByActiveParts:    123,
		},
		{
			Node: chclient.Node{
				ID:      "node-a",
				Shard:   "shard1",
				Replica: "replica1",
			},
			Name:              "s3_cache",
			Path:              "/var/lib/clickhouse/disks/s3/",
			CachePath:         "/var/lib/clickhouse/disks/s3_cache/",
			Type:              "ObjectStorage",
			ObjectStorageType: "S3",
			IsRemote:          true,
			CapacityKnown:     false,
			UsedByActiveParts: 456,
		},
	})
}

func (f *fakeState) CollectTables(context.Context) clusterstate.Result[clusterstate.TableState] {
	minPartition := "('mainnet',202601)"
	maxPartition := "('mainnet',202602)"
	lastModified := f.now.Add(3 * time.Second)

	return fakeResult(f.now, []clusterstate.TableState{
		{
			Node: chclient.Node{
				ID:      "node-b",
				Shard:   "shard1",
				Replica: "replica2",
			},
			Database:             "movoor_dev",
			Table:                "test_generic_network_month_local",
			UUID:                 "uuid-b",
			Engine:               "ReplicatedReplacingMergeTree",
			StoragePolicy:        "movoor_tiered",
			PartitionKey:         "(network_id, toYYYYMM(event_time))",
			SortingKey:           "(network_id, event_time, record_id)",
			PrimaryKey:           "(network_id, event_time, record_id)",
			IsReplicated:         true,
			ActivePartitions:     1,
			ActiveParts:          1,
			Rows:                 20,
			BytesOnDisk:          200,
			MinPartition:         &minPartition,
			MaxPartition:         &maxPartition,
			LastModificationTime: &lastModified,
			Replica: &clusterstate.ReplicaState{
				Readonly:             false,
				SessionExpired:       false,
				QueueSize:            0,
				AbsoluteDelaySeconds: 0,
				TotalReplicas:        2,
				ActiveReplicas:       2,
			},
		},
		{
			Node: chclient.Node{
				ID:      "node-a",
				Shard:   "shard1",
				Replica: "replica1",
			},
			Database:             "movoor_dev",
			Table:                "test_generic_network_month_local",
			UUID:                 "uuid-a",
			Engine:               "ReplicatedReplacingMergeTree",
			StoragePolicy:        "movoor_tiered",
			PartitionKey:         "(network_id, toYYYYMM(event_time))",
			SortingKey:           "(network_id, event_time, record_id)",
			PrimaryKey:           "(network_id, event_time, record_id)",
			SamplingKey:          "cityHash64(record_id)",
			IsReplicated:         true,
			ActivePartitions:     1,
			ActiveParts:          1,
			Rows:                 10,
			BytesOnDisk:          100,
			MinPartition:         &minPartition,
			MaxPartition:         &maxPartition,
			LastModificationTime: &lastModified,
			Replica: &clusterstate.ReplicaState{
				Readonly:             false,
				SessionExpired:       false,
				QueueSize:            1,
				AbsoluteDelaySeconds: 2,
				TotalReplicas:        2,
				ActiveReplicas:       2,
			},
		},
	})
}

func (f *sortState) CollectTables(context.Context) clusterstate.Result[clusterstate.TableState] {
	items := f.fakeState.CollectTables(context.Background()).Items
	other := items[0]
	other.Database = "movoor_dev"
	other.Table = "zz_second_table"
	other.UUID = "uuid-z"

	return fakeResult(f.now, append(items, other))
}

func (f *fakeState) CollectTableColumns(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.NodeColumns] {
	defaultKind := "DEFAULT"
	defaultExpression := "now64(3)"

	return fakeResult(f.now, []clusterstate.NodeColumns{
		{
			Node:     chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			Database: "movoor_dev",
			Table:    "test_generic_network_month_local",
			Columns: []clusterstate.Column{
				{
					Name:             "network_id",
					Position:         1,
					Type:             "LowCardinality(String)",
					Kind:             "DEFAULT",
					IsInPartitionKey: true,
					IsInSortingKey:   true,
					IsInPrimaryKey:   true,
				},
				{
					Name:              "updated_at",
					Position:          5,
					Type:              "DateTime64(3)",
					Kind:              "DEFAULT",
					DefaultKind:       &defaultKind,
					DefaultExpression: &defaultExpression,
					Comment:           "write timestamp",
				},
			},
			Conditions: []clusterstate.Condition{f.infoCondition()},
		},
	})
}

func (f *sortState) CollectTableColumns(_ context.Context, watch clusterstate.Watch) clusterstate.Result[clusterstate.NodeColumns] {
	items := f.fakeState.CollectTableColumns(context.Background(), watch).Items
	other := items[0]
	other.Node = chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}

	return fakeResult(f.now, append(items, other))
}

func (f *fakeState) CollectParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	removeTime := f.now.Add(-time.Hour)
	deleteTTLMin := f.now.Add(24 * time.Hour)
	deleteTTLMax := f.now.Add(48 * time.Hour)

	return fakeResult(f.now, []clusterstate.Part{
		f.part("node-a", "default", "part-a", 100, 10),
		{
			Node:                       chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			Database:                   "movoor_dev",
			Table:                      "test_generic_network_month_local",
			Partition:                  "('mainnet',202601)",
			PartitionID:                "p-mainnet-202601",
			Name:                       "part-old",
			UUID:                       "part-old-uuid",
			Active:                     false,
			Disk:                       "default",
			Path:                       "/var/lib/clickhouse/store/old",
			PartType:                   "Wide",
			Rows:                       5,
			Marks:                      1,
			BytesOnDisk:                50,
			ModificationTime:           f.now.Add(-time.Hour),
			RemoveTime:                 &removeTime,
			Refcount:                   1,
			MinBlockNumber:             0,
			MaxBlockNumber:             0,
			Level:                      0,
			DataVersion:                0,
			DeleteTTLInfoMin:           &deleteTTLMin,
			DeleteTTLInfoMax:           &deleteTTLMax,
			DefaultCompressionCodec:    "LZ4",
			DataCompressedBytes:        25,
			DataUncompressedBytes:      100,
			PrimaryKeyBytesInMemory:    10,
			SecondaryIndicesMarksBytes: 0,
		},
	})
}

func (f *fakeState) CollectActiveParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	return fakeResult(f.now, []clusterstate.Part{
		f.part("node-a", "default", "part-a", 100, 10),
		f.part("node-b", "s3_cache", "part-b", 200, 20),
	})
}

func (f *sortState) CollectActiveParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	first := f.part("node-a", "default", "part-a", 100, 10)
	second := f.part("node-b", "s3_cache", "part-b", 200, 20)
	second.Partition = "('mainnet',202602)"
	second.PartitionID = "p-mainnet-202602"

	return fakeResult(f.now, []clusterstate.Part{second, first})
}

func (f *fakeState) CollectDetachedParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.DetachedPart] {
	partitionID := "p-mainnet-202601"
	reason := "broken"
	minBlock := int64(1)
	maxBlock := int64(2)
	level := uint64(3)

	return fakeResult(f.now, []clusterstate.DetachedPart{
		{
			Node:             chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			Database:         "movoor_dev",
			Table:            "test_generic_network_month_local",
			PartitionID:      &partitionID,
			Name:             "detached-a",
			Disk:             "default",
			Reason:           &reason,
			Path:             "/var/lib/clickhouse/detached/detached-a",
			BytesOnDisk:      123,
			Rows:             10,
			MinBlockNumber:   &minBlock,
			MaxBlockNumber:   &maxBlock,
			Level:            &level,
			ModificationTime: f.now,
			Conditions:       []clusterstate.Condition{f.criticalCondition()},
		},
	})
}

func (f *sortState) CollectDetachedParts(ctx context.Context, watch clusterstate.Watch) clusterstate.Result[clusterstate.DetachedPart] {
	items := f.fakeState.CollectDetachedParts(ctx, watch).Items
	other := items[0]
	other.Node = chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}
	other.Name = "detached-b"

	return fakeResult(f.now, append(items, other))
}

func (f *fakeState) CollectMutations(context.Context) clusterstate.Result[clusterstate.Mutation] {
	failedPart := "part-a"
	failTime := f.now.Add(4 * time.Second)
	failReason := "Cannot parse UInt64"

	return fakeResult(f.now, []clusterstate.Mutation{
		{
			Node:             chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			Database:         "movoor_dev",
			Table:            "test_generic_network_month_local",
			MutationID:       "0000000000",
			Command:          "UPDATE value = toUInt64(payload) WHERE id = 1",
			CreateTime:       f.now,
			IsDone:           false,
			PartsToDo:        1,
			PartsToDoNames:   []string{"part-a"},
			BlockNumbers:     []clusterstate.MutationBlockNumber{{PartitionID: "p-mainnet-202601", Number: 42}},
			LatestFailedPart: &failedPart,
			LatestFailTime:   &failTime,
			LatestFailReason: &failReason,
			Conditions:       []clusterstate.Condition{f.criticalCondition()},
		},
	})
}

func (f *sortState) CollectMutations(ctx context.Context) clusterstate.Result[clusterstate.Mutation] {
	items := f.fakeState.CollectMutations(ctx).Items
	other := items[0]
	other.Node = chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}
	other.MutationID = "0000000001"
	other.IsDone = true
	other.LatestFailReason = nil

	return fakeResult(f.now, append(items, other))
}

func (f *fakeState) CollectReplicationQueue(context.Context) clusterstate.Result[clusterstate.ReplicationQueueItem] {
	sourceReplica := "replica2"
	newPartName := "part-a"
	lastAttempt := f.now.Add(5 * time.Second)
	lastPostpone := f.now.Add(6 * time.Second)
	postponeReason := "not enough space"
	lastException := "Code: 243. Not enough space"

	return fakeResult(f.now, []clusterstate.ReplicationQueueItem{
		{
			Node:                 chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			Database:             "movoor_dev",
			Table:                "test_generic_network_month_local",
			ReplicaName:          "replica1",
			Position:             7,
			NodeName:             "/queue/0000000007",
			Type:                 "GET_PART",
			CreateTime:           f.now,
			RequiredQuorum:       0,
			SourceReplica:        &sourceReplica,
			NewPartName:          &newPartName,
			PartsToMerge:         []string{"part-a", "part-b"},
			IsCurrentlyExecuting: true,
			NumTries:             2,
			LastAttemptTime:      &lastAttempt,
			LastPostponeTime:     &lastPostpone,
			NumPostponed:         3,
			PostponeReason:       &postponeReason,
			LastException:        &lastException,
			Conditions:           []clusterstate.Condition{f.criticalCondition()},
		},
	})
}

func (f *sortState) CollectReplicationQueue(ctx context.Context) clusterstate.Result[clusterstate.ReplicationQueueItem] {
	items := f.fakeState.CollectReplicationQueue(ctx).Items
	newPartName := "part-z"
	other := items[0]
	other.Node = chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}
	other.Position = 8
	other.NodeName = "/queue/0000000008"
	other.Type = "MERGE_PARTS"
	other.NewPartName = &newPartName
	other.IsCurrentlyExecuting = false
	other.LastException = nil

	return fakeResult(f.now, append(items, other))
}

func (f *fakeState) CollectPartEvents(context.Context, *time.Time, *time.Time) clusterstate.Result[clusterstate.PartEvent] {
	sourceDisk := "default"
	targetDisk := "s3_cache"
	exception := "move warning"

	return fakeResult(f.now, []clusterstate.PartEvent{
		{
			Node:                chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			Database:            "movoor_dev",
			Table:               "test_generic_network_month_local",
			Partition:           "('mainnet',202601)",
			PartitionID:         "p-mainnet-202601",
			PartName:            "part-a",
			EventType:           "MovePart",
			EventTime:           f.now.Add(5 * time.Second),
			DurationMs:          12,
			Rows:                10,
			BytesCompressed:     100,
			BytesUncompressed:   200,
			ReadRows:            10,
			ReadBytes:           200,
			MergedFrom:          []string{"part-a"},
			SourceDisk:          &sourceDisk,
			TargetDisk:          &targetDisk,
			Error:               1,
			Exception:           &exception,
			EventTimeMicrostamp: "2026-01-01 00:00:05.000001",
		},
	})
}

func (f *sortState) CollectPartEvents(ctx context.Context, from *time.Time, to *time.Time) clusterstate.Result[clusterstate.PartEvent] {
	items := f.fakeState.CollectPartEvents(ctx, from, to).Items
	other := items[0]
	other.Node = chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}
	other.PartName = "part-b"
	other.EventTime = f.now.Add(4 * time.Second)
	other.EventTimeMicrostamp = "2026-01-01 00:00:04.000001"
	other.Error = 0

	return fakeResult(f.now, append(items, other))
}

func (f *fakeState) CollectOperations(context.Context) clusterstate.Result[clusterstate.Operation] {
	partition := "('mainnet',202601)"
	partitionID := "p-mainnet-202601"
	elapsed := 1.5
	progress := 0.25
	sourceDisk := "default"
	targetDisk := "s3_cache"
	bytesTotal := uint64(100)
	bytesProcessed := uint64(25)
	message := "moving"
	startedAt := f.now.Add(-time.Second)

	return fakeResult(f.now, []clusterstate.Operation{
		{
			OperationID:    "move|node-a|part-a",
			Kind:           "move",
			NodeID:         "node-a",
			Database:       "movoor_dev",
			Table:          "test_generic_network_month_local",
			Partition:      &partition,
			PartitionID:    &partitionID,
			AttemptID:      "attempt-1",
			State:          "running",
			ElapsedSeconds: &elapsed,
			Progress:       &progress,
			SourceDisk:     &sourceDisk,
			TargetDisk:     &targetDisk,
			BytesTotal:     &bytesTotal,
			BytesProcessed: &bytesProcessed,
			LatestMessage:  &message,
			StartedAt:      &startedAt,
		},
	})
}

func (f *sortState) CollectOperations(ctx context.Context) clusterstate.Result[clusterstate.Operation] {
	items := f.fakeState.CollectOperations(ctx).Items
	other := items[0]
	other.OperationID = "fetch|node-b|part-b"
	other.Kind = "fetch"
	other.NodeID = "node-b"

	return fakeResult(f.now, append(items, other))
}

func (f *fakeState) CollectConditions(context.Context) clusterstate.Result[clusterstate.Condition] {
	return fakeResult(f.now, []clusterstate.Condition{
		f.criticalCondition(),
		f.partitionCondition(),
		f.infoCondition(),
	})
}

func (f *fakeState) TableConditions(context.Context, clusterstate.Result[clusterstate.TableState]) clusterstate.Result[clusterstate.Condition] {
	return fakeResult(f.now, []clusterstate.Condition{f.criticalCondition()})
}

func (f *fakeState) PartitionConditions(context.Context, clusterstate.Watch, clusterstate.Result[clusterstate.Part]) clusterstate.Result[clusterstate.Condition] {
	return fakeResult(f.now, []clusterstate.Condition{f.partitionCondition()})
}

func (f *fakeState) part(nodeID string, disk string, name string, bytesOnDisk uint64, rows uint64) clusterstate.Part {
	return clusterstate.Part{
		Node:                              chclient.Node{ID: nodeID, Shard: "shard1", Replica: strings.TrimPrefix(nodeID, "node-")},
		Database:                          "movoor_dev",
		Table:                             "test_generic_network_month_local",
		Partition:                         "('mainnet',202601)",
		PartitionID:                       "p-mainnet-202601",
		Name:                              name,
		UUID:                              name + "-uuid",
		Active:                            true,
		Disk:                              disk,
		Path:                              "/var/lib/clickhouse/store/" + name,
		PartType:                          "Wide",
		Rows:                              rows,
		Marks:                             1,
		BytesOnDisk:                       bytesOnDisk,
		DataCompressedBytes:               bytesOnDisk / 2,
		DataUncompressedBytes:             bytesOnDisk,
		MarksBytes:                        8,
		PrimaryKeyBytesInMemory:           16,
		PrimaryKeyBytesInMemoryAllocated:  32,
		SecondaryIndicesCompressedBytes:   0,
		SecondaryIndicesUncompressedBytes: 0,
		SecondaryIndicesMarksBytes:        0,
		ModificationTime:                  f.now.Add(3 * time.Second),
		Refcount:                          1,
		MinBlockNumber:                    1,
		MaxBlockNumber:                    2,
		Level:                             0,
		DataVersion:                       0,
		DefaultCompressionCodec:           "LZ4",
		Conditions:                        []clusterstate.Condition{f.partitionCondition()},
	}
}

func (f *fakeState) criticalCondition() clusterstate.Condition {
	database := "movoor_dev"
	table := "test_generic_network_month_local"
	nodeID := "node-a"

	return clusterstate.Condition{
		ConditionID: "condition-critical",
		Severity:    "critical",
		Code:        "stuck_mutation",
		Message:     "mutation is failing",
		ObservedAt:  f.now,
		Database:    &database,
		Table:       &table,
		NodeID:      &nodeID,
		Evidence:    map[string]any{"mutationId": "0000000000"},
		Links:       map[string]string{"mutations": "/api/v1/operations/mutations"},
	}
}

func (f *fakeState) partitionCondition() clusterstate.Condition {
	database := "movoor_dev"
	table := "test_generic_network_month_local"
	partition := "('mainnet',202601)"
	partitionID := "p-mainnet-202601"
	nodeID := "node-a"

	return clusterstate.Condition{
		ConditionID: "condition-partition",
		Severity:    "warning",
		Code:        "split_partition",
		Message:     "partition is split",
		ObservedAt:  f.now,
		Database:    &database,
		Table:       &table,
		Partition:   &partition,
		PartitionID: &partitionID,
		NodeID:      &nodeID,
		Evidence:    map[string]any{"diskCount": float64(2)},
	}
}

func (f *fakeState) infoCondition() clusterstate.Condition {
	database := "movoor_dev"
	table := "test_generic_network_month_local"

	return clusterstate.Condition{
		ConditionID: "condition-info",
		Severity:    "info",
		Code:        "schema_observed",
		Message:     "schema observed",
		ObservedAt:  f.now,
		Database:    &database,
		Table:       &table,
	}
}

func fakeResult[T any](now time.Time, items []T) clusterstate.Result[T] {
	return clusterstate.Result[T]{
		CollectedAt:        now,
		CollectionDuration: 12 * time.Millisecond,
		NodesExpected:      2,
		NodesResponded:     2,
		NodesFailed:        0,
		Items:              items,
	}
}

type singleFileFS struct {
	file fs.File
}

func (f singleFileFS) Open(name string) (fs.File, error) {
	if name != "index.html" {
		return nil, fs.ErrNotExist
	}

	return f.file, nil
}

type statErrorFile struct {
	reader *strings.Reader
}

func (f *statErrorFile) Stat() (fs.FileInfo, error) {
	return nil, errors.New("stat failed")
}

func (f *statErrorFile) Read(p []byte) (int, error) {
	return f.reader.Read(p)
}

func (*statErrorFile) Close() error {
	return nil
}

type nonSeekFile struct {
	reader *strings.Reader
}

func (f *nonSeekFile) Stat() (fs.FileInfo, error) {
	return testFileInfo{size: int64(f.reader.Len())}, nil
}

func (f *nonSeekFile) Read(p []byte) (int, error) {
	return f.reader.Read(p)
}

func (*nonSeekFile) Close() error {
	return nil
}

type testFileInfo struct {
	size int64
}

func (f testFileInfo) Name() string {
	return "index.html"
}

func (f testFileInfo) Size() int64 {
	return f.size
}

func (testFileInfo) Mode() fs.FileMode {
	return 0o444
}

func (testFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (testFileInfo) IsDir() bool {
	return false
}

func (testFileInfo) Sys() any {
	return nil
}
