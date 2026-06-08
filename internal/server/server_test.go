package server_test

import (
	"context"
	"io"
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
				"detail": "request parameter validation failed",
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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+srv.Addr()+"/api/v1/nodes", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	require.JSONEq(t, `{
		"type": "about:blank",
		"title": "Service Unavailable",
		"status": 503,
		"detail": "no configured ClickHouse node responded",
		"instance": "/api/v1/nodes"
	}`, string(body))
}

type fakeState struct {
	now time.Time
}

type noResponderState struct {
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
	return clusterstate.Result[clusterstate.NodeStatus]{
		CollectedAt:        f.now,
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
			FreeSpaceBytes:       uint64Ptr(1000),
			TotalSpaceBytes:      uint64Ptr(2000),
			UnreservedSpaceBytes: uint64Ptr(900),
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
	return fakeResult(f.now, []clusterstate.TableState{})
}

func (f *fakeState) CollectTableColumns(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.NodeColumns] {
	return fakeResult(f.now, []clusterstate.NodeColumns{})
}

func (f *fakeState) CollectParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	return fakeResult(f.now, []clusterstate.Part{})
}

func (f *fakeState) CollectDetachedParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.DetachedPart] {
	return fakeResult(f.now, []clusterstate.DetachedPart{})
}

func (f *fakeState) CollectMutations(context.Context) clusterstate.Result[clusterstate.Mutation] {
	return fakeResult(f.now, []clusterstate.Mutation{})
}

func (f *fakeState) CollectReplicationQueue(context.Context) clusterstate.Result[clusterstate.ReplicationQueueItem] {
	return fakeResult(f.now, []clusterstate.ReplicationQueueItem{})
}

func (f *fakeState) CollectPartEvents(context.Context) clusterstate.Result[clusterstate.PartEvent] {
	return fakeResult(f.now, []clusterstate.PartEvent{})
}

func (f *fakeState) CollectOperations(context.Context) clusterstate.Result[clusterstate.Operation] {
	return fakeResult(f.now, []clusterstate.Operation{})
}

func (f *fakeState) CollectConditions(context.Context) clusterstate.Result[clusterstate.Condition] {
	return fakeResult(f.now, []clusterstate.Condition{})
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

//nolint:modernize // Test fixture pointer helper keeps fake payloads readable.
func uint64Ptr(value uint64) *uint64 {
	return &value
}
