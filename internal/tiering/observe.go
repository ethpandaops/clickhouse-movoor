package tiering

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

type Observer interface {
	ObserveTable(ctx context.Context, client chclient.Client, watch EffectiveWatch) (TableObservation, error)
	RefreshPartition(ctx context.Context, client chclient.Client, table TableObservation, partitionID string) (PartitionObservation, error)
}

type SQLObserver struct {
	QueryTimeout time.Duration

	// flushMu/lastFlush coalesce the guard's SYSTEM FLUSH LOGS per node: at
	// short tick intervals every watched table's guard would otherwise issue
	// its own flush against the same node within the same tick.
	flushMu   sync.Mutex
	lastFlush map[string]time.Time
}

type TableObservation struct {
	Node              chclient.Node
	Database          string
	Table             string
	UUID              string
	Engine            string
	IsReplicated      bool
	StoragePolicy     string
	PartitionKey      string
	Layout            TableLayout
	Settings          TierSettings
	EffectiveMode     Mode
	TargetDiskFound   bool
	HotVolume         string
	Version           string
	ObservedAt        time.Time
	PartLogMinTime    *time.Time
	Replica           *ReplicaObservation
	Mutations         []MutationObservation
	Partitions        []PartitionObservation
	InFlightPartNames []string
	Heads             map[string]int64
	ResplitQuiet      map[string]time.Duration
	Conditions        []Condition
}

type PartitionObservation struct {
	Partition           string
	PartitionID         string
	GroupKey            string
	AgeString           string
	AgeInteger          int64
	ActiveParts         uint64
	Rows                uint64
	BytesOnDisk         uint64
	MaxModificationTime time.Time
	Disks               []DiskPart
	LatestNewPart       *time.Time
	LatestPartLogEvent  *time.Time
	Hashes              []PartHash
	// MergeInFlight reports a merge currently running in system.merges for
	// this partition — movoor's own, an orphaned leg's, or a foreign one.
	MergeInFlight bool
}

type DiskPart struct {
	Disk  string
	Parts uint64
}

type PartHash struct {
	Name string
	Hash string
	Disk string
}

type ReplicaObservation struct {
	Readonly             bool
	SessionExpired       bool
	QueueSize            uint64
	AbsoluteDelaySeconds uint64
	MergeQueue           []string
}

type MutationObservation struct {
	MutationID       string
	Command          string
	IsDone           bool
	PartsToDo        uint64
	PartsToDoNames   []string
	LatestFailedPart string
	LatestFailReason string
}

type StorageVolume struct {
	Policy     string
	Volume     string
	Priority   uint64
	Disks      []string
	MoveFactor float64
}

type ForeignMoveObservation struct {
	DuplicateInstance bool
	ForeignActivity   bool
	Message           string
}

// safetyObserver enumerates every optional capability the controller and
// executor probe for via anonymous interface assertions. Those assertions
// degrade gracefully so test fakes can implement subsets — but for the real
// observer, degradation means a safety rail silently no-ops (the foreign-move
// guard's fallback is "clean"). This compile-time pin turns signature drift
// in *SQLObserver into a build error instead.
type safetyObserver interface {
	Observer
	CaptureBootTime(context.Context, chclient.Client) (time.Time, error)
	SeedMovedBytesToday(context.Context, chclient.Client, []EffectiveWatch) (uint64, error)
	ObserveForeignMoves(context.Context, chclient.Client, TableObservation, string, time.Time) (ForeignMoveObservation, error)
	ObserveForeignMovesSince(context.Context, chclient.Client, TableObservation, string, time.Time, time.Time) (ForeignMoveObservation, error)
	ProbeColdPartition(context.Context, chclient.Client, TableObservation, PartitionObservation) error
	CountColdSideMerges(context.Context, chclient.Client, TableObservation, time.Time) (uint64, error)
	CheckTableIdentity(context.Context, chclient.Client, TableObservation) error
	CheckMutationsClear(context.Context, chclient.Client, TableObservation) error
	CheckOptimizeFreeSpace(context.Context, chclient.Client, TableObservation, Verdict) error
	CountInsertsSince(context.Context, chclient.Client, TableObservation, string, time.Time) (uint64, error)
}

var _ safetyObserver = (*SQLObserver)(nil)

func NewSQLObserver(queryTimeout time.Duration) *SQLObserver {
	return &SQLObserver{QueryTimeout: queryTimeout}
}

//nolint:gocognit // Observation intentionally keeps the node-scoped query sequence explicit.
func (o *SQLObserver) ObserveTable(ctx context.Context, client chclient.Client, watch EffectiveWatch) (TableObservation, error) {
	if watch.Settings == nil {
		return TableObservation{}, errors.New("watch has no tier settings")
	}
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	obs := TableObservation{
		Node:          client.Node,
		Database:      watch.Database,
		Table:         watch.Table,
		Settings:      watch.Settings.Clone(),
		ObservedAt:    time.Now().UTC(),
		EffectiveMode: watch.Settings.Mode,
		Heads:         make(map[string]int64),
	}
	if obs.EffectiveMode == "" {
		obs.EffectiveMode = ModePlan
	}

	if err := o.collectTableMetadata(queryCtx, client.DB, &obs); err != nil {
		return TableObservation{}, err
	}
	applyVersionGate(&obs)
	if err := validateTieringEngine(obs.Engine); err != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityCritical, "unsupported_engine", err.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
		return obs, nil
	}
	layout, err := ParseLayout(obs.Database, obs.Table, obs.PartitionKey, obs.Settings)
	if err != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityCritical, "misconfigured", err.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
		return obs, nil
	}
	if tzErr := o.applyColumnTimezone(queryCtx, client.DB, &layout); tzErr != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityCritical, "misconfigured", tzErr.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
		return obs, nil
	}
	obs.Layout = layout

	if policyErr := o.collectStoragePolicy(queryCtx, client.DB, &obs); policyErr != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "storage_policy_unreadable", policyErr.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
	}
	if obs.Settings.Age.Basis == AgeBasisFrontier {
		heads, headsErr := o.collectFrontierHeads(queryCtx, client.DB, obs.Layout)
		if headsErr != nil {
			obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "frontier_head_query_failed", headsErr.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
		} else {
			obs.Heads = heads
		}
	}
	replica, err := o.collectReplica(queryCtx, client.DB, obs.Database, obs.Table, obs.IsReplicated)
	if err != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "replica_health_unreadable", err.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
	} else {
		obs.Replica = replica
	}
	mutations, err := o.collectMutations(queryCtx, client.DB, obs.Database, obs.Table)
	if err != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "mutations_unreadable", err.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
	} else {
		obs.Mutations = mutations
	}
	events, minEvent, err := o.collectPartLogEvidence(queryCtx, client.DB, obs.Database, obs.Table, partLogLookbackDays(obs.Settings))
	if err != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "part_log_unreadable", err.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
	} else {
		obs.PartLogMinTime = minEvent
	}
	merging, err := o.collectRunningMerges(queryCtx, client.DB, obs.Database, obs.Table)
	if err != nil {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "merges_unreadable", err.Error(), obs.Node.ID, obs.Database, obs.Table, "", ""))
	}
	partitions, err := o.collectPartitionRollup(queryCtx, client.DB, obs)
	if err != nil {
		return TableObservation{}, err
	}
	for i := range partitions {
		if event, ok := events[partitions[i].PartitionID]; ok {
			partitions[i].LatestNewPart = event.LatestNewPart
			partitions[i].LatestPartLogEvent = event.LatestAny
		}
		if _, ok := merging[partitions[i].PartitionID]; ok {
			partitions[i].MergeInFlight = true
		}
	}
	obs.Partitions = partitions

	return obs, nil
}

// collectRunningMerges reports the partition IDs with a merge currently
// executing on this node. Decide holds those partitions: a MOVE of parts that
// are mid-merge is rejected by ClickHouse, and a second OPTIMIZE is at best a
// no-op — surfacing the merge keeps the plan (and its apply buttons) honest.
func (o *SQLObserver) collectRunningMerges(ctx context.Context, db *sql.DB, database string, table string) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT partition_id
		FROM system.merges
		WHERE database = ? AND table = ?
	`, database, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	merging := make(map[string]struct{})
	for rows.Next() {
		var partitionID string
		if scanErr := rows.Scan(&partitionID); scanErr != nil {
			return nil, scanErr
		}
		merging[partitionID] = struct{}{}
	}
	return merging, rows.Err()
}

func (o *SQLObserver) CaptureBootTime(ctx context.Context, client chclient.Client) (time.Time, error) {
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	var boot time.Time
	if err := client.DB.QueryRowContext(queryCtx, "SELECT now64(6)").Scan(&boot); err != nil {
		return time.Time{}, fmt.Errorf("capture ClickHouse boot reference: %w", err)
	}
	return boot, nil
}

func (o *SQLObserver) SeedMovedBytesToday(ctx context.Context, client chclient.Client, watches []EffectiveWatch) (uint64, error) {
	pairs := make([]string, 0, len(watches))
	args := make([]any, 0, len(watches)*2)
	for _, watch := range watches {
		if watch.Settings == nil {
			continue
		}
		pairs = append(pairs, "(?, ?)")
		args = append(args, watch.Database, watch.Table)
	}
	if len(pairs) == 0 {
		return 0, nil
	}

	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	// One batched query: per-watch round-trips do not fit inside a single
	// queryTimeout once the watch count grows.
	var total uint64
	if err := client.DB.QueryRowContext(queryCtx, fmt.Sprintf(`
		SELECT coalesce(sum(size_in_bytes), 0)
		FROM system.part_log
		WHERE event_type = 'MovePart'
			AND event_date = today()
			AND (database, table) IN (%s)
	`, strings.Join(pairs, ", ")), args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("seed moved bytes: %w", err)
	}
	return total, nil
}

func (o *SQLObserver) ObserveForeignMoves(ctx context.Context, client chclient.Client, table TableObservation, instanceID string, bootTime time.Time) (ForeignMoveObservation, error) {
	return o.ObserveForeignMovesSince(ctx, client, table, instanceID, bootTime, bootTime)
}

func (o *SQLObserver) ObserveForeignMovesSince(ctx context.Context, client chclient.Client, table TableObservation, instanceID string, bootTime time.Time, since time.Time) (ForeignMoveObservation, error) {
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	if err := o.flushGuardLogs(queryCtx, client); err != nil {
		return ForeignMoveObservation{}, err
	}

	live, err := o.collectLiveMoveStatements(queryCtx, client.DB, table, instanceID)
	if err != nil {
		return ForeignMoveObservation{}, err
	}
	if live.DuplicateInstance || live.ForeignActivity {
		return live, nil
	}
	recent, err := o.collectRecentMoveStatements(queryCtx, client.DB, table, instanceID, since)
	if err != nil {
		return ForeignMoveObservation{}, err
	}
	if recent.DuplicateInstance || recent.ForeignActivity {
		return recent, nil
	}
	active, err := o.collectActiveMoveEvents(queryCtx, client.DB, table)
	if err != nil {
		return ForeignMoveObservation{}, err
	}
	if active.ForeignActivity {
		return active, nil
	}
	anonymous, err := o.collectAnonymousMovePartEventsSince(queryCtx, client.DB, table, bootTime, since)
	if err != nil {
		return ForeignMoveObservation{}, err
	}
	return anonymous, nil
}

func (o *SQLObserver) CheckTableIdentity(ctx context.Context, client chclient.Client, table TableObservation) error {
	if table.UUID == "" && table.Layout.Generation == "" {
		return nil
	}
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	var uuid string
	var partitionKey string
	if err := client.DB.QueryRowContext(queryCtx, `
		SELECT uuid, partition_key
		FROM system.tables
		WHERE database = ? AND name = ?
		LIMIT 1
	`, table.Database, table.Table).Scan(&uuid, &partitionKey); err != nil {
		return fmt.Errorf("check table identity for %s.%s: %w", table.Database, table.Table, err)
	}
	if table.UUID != "" && uuid != table.UUID {
		return fmt.Errorf("table identity changed for %s.%s: uuid was %s, now %s", table.Database, table.Table, table.UUID, uuid)
	}
	if table.Layout.Generation != "" {
		currentGeneration := strings.TrimSpace(partitionKey) + "|" + string(table.Settings.Age.Basis)
		if currentGeneration != table.Layout.Generation {
			return fmt.Errorf("table layout changed for %s.%s: generation was %q, now %q", table.Database, table.Table, table.Layout.Generation, currentGeneration)
		}
	}
	return nil
}

func (o *SQLObserver) CheckMutationsClear(ctx context.Context, client chclient.Client, table TableObservation) error {
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	mutations, err := o.collectMutations(queryCtx, client.DB, table.Database, table.Table)
	if err != nil {
		return fmt.Errorf("re-check mutations for %s.%s: %w", table.Database, table.Table, err)
	}
	for _, mutation := range mutations {
		if mutation.PartsToDo == 0 {
			continue
		}
		message := mutation.MutationID
		if mutation.LatestFailReason != "" {
			message += ": " + mutation.LatestFailReason
		}
		return fmt.Errorf("unfinished mutation blocks move for %s.%s: %s", table.Database, table.Table, message)
	}
	return nil
}

// CountInsertsSince returns how many NewPart insert events landed for the
// partition after `since`. The executor uses it as the only live re-check
// during an attempt: fresh inserts mean the partition genuinely unsealed and
// the attempt must abort. Our own OPTIMIZE/MOVE emit MergeParts/MovePart
// events, never NewPart, so this never trips on the attempt's own work.
func (o *SQLObserver) CountInsertsSince(ctx context.Context, client chclient.Client, table TableObservation, partitionID string, since time.Time) (uint64, error) {
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	var count uint64
	if err := client.DB.QueryRowContext(queryCtx, `
		SELECT count()
		FROM system.part_log
		WHERE database = ?
			AND table = ?
			AND partition_id = ?
			AND event_type = 'NewPart'
			AND event_time_microseconds > toDateTime64(?, 6)
	`, table.Database, table.Table, partitionID, dateTime64MicrosParam(since)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count fresh inserts for %s.%s partition %s: %w", table.Database, table.Table, partitionID, err)
	}
	return count, nil
}

// CheckOptimizeFreeSpace verifies the disks holding the partition have
// roughly 2× its bytes free before an OPTIMIZE FINAL — merge selection
// silently no-ops without it. The optimize leg only runs once all parts sit
// on one side, so summing free space over the part-holding disks is correct
// for hot- and cold-side merges alike (remote disks that report effectively
// unlimited free space naturally no-op this check).
// Applicability gating (skipOptimize, part count, size cap) lives in the
// classifier; this is purely the space pre-flight.
func (o *SQLObserver) CheckOptimizeFreeSpace(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict) error {
	free, err := o.collectDiskFreeSpace(ctx, client.DB)
	if err != nil {
		return fmt.Errorf("check free space before optimize for %s.%s: %w", table.Database, table.Table, err)
	}
	var available uint64
	for _, disk := range verdict.Disks {
		available += free[disk.Disk]
	}
	required := saturatingDouble(verdict.BytesOnDisk)
	if available < required {
		return fmt.Errorf("not enough free space before optimize for %s.%s partition %s: need %d bytes, have %d", table.Database, table.Table, verdict.PartitionID, required, available)
	}
	return nil
}

func (o *SQLObserver) CountColdSideMerges(ctx context.Context, client chclient.Client, table TableObservation, since time.Time) (uint64, error) {
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	var count uint64
	if err := client.DB.QueryRowContext(queryCtx, `
		SELECT count()
		FROM system.part_log
		WHERE database = ?
			AND table = ?
			AND event_type = 'MergeParts'
			AND disk_name = ?
			AND event_time > ?
	`, table.Database, table.Table, table.Settings.TargetDisk, since).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (o *SQLObserver) ProbeColdPartition(ctx context.Context, client chclient.Client, table TableObservation, partition PartitionObservation) error {
	partNames := make([]string, 0, len(partition.Hashes))
	for _, part := range partition.Hashes {
		if part.Disk == table.Settings.TargetDisk {
			partNames = append(partNames, part.Name)
		}
	}
	if len(partNames) == 0 {
		return nil
	}

	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	var column string
	if err := client.DB.QueryRowContext(queryCtx, `
		SELECT name
		FROM system.columns
		WHERE database = ? AND table = ?
		ORDER BY if(default_kind = '', 0, 1), position
		LIMIT 1
	`, table.Database, table.Table).Scan(&column); err != nil {
		return fmt.Errorf("select column for cold read probe: %w", err)
	}
	query := "SELECT toString(" + QuoteIdent(column) + ") FROM " + QuoteQualified(table.Database, table.Table) +
		" WHERE _part = ? LIMIT 1 SETTINGS enable_filesystem_cache = 0"
	for _, partName := range partNames {
		var value string
		if err := client.DB.QueryRowContext(queryCtx, query, partName).Scan(&value); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("cold read probe failed for part %s: %w", partName, err)
		}
		return nil
	}
	return nil
}

func (o *SQLObserver) collectDiskFreeSpace(ctx context.Context, db *sql.DB) (map[string]uint64, error) {
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	rows, err := db.QueryContext(queryCtx, `
		SELECT name, free_space
		FROM system.disks
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]uint64)
	for rows.Next() {
		var name string
		var free uint64
		if scanErr := rows.Scan(&name, &free); scanErr != nil {
			return nil, scanErr
		}
		out[name] = free
	}
	return out, rows.Err()
}

func saturatingDouble(value uint64) uint64 {
	if value > ^uint64(0)/2 {
		return ^uint64(0)
	}
	return value * 2
}

// collectLiveMoveStatements matches only query_kind='Alter' — the guard's
// own surveillance SELECTs contain the literal 'MOVE PARTITION' in their
// WHERE clauses and would otherwise be detected as foreign movers.
// flushGuardLogs flushes the two log tables the guard reads, scoped (not a
// full SYSTEM FLUSH LOGS) and at most once per node per coalescing window.
// The guard tolerates sub-second staleness: its statement layer looks back to
// boot, and the event layer auto-resumes on clean ticks.
func (o *SQLObserver) flushGuardLogs(ctx context.Context, client chclient.Client) error {
	const flushCoalesceWindow = 500 * time.Millisecond

	o.flushMu.Lock()
	if o.lastFlush == nil {
		o.lastFlush = make(map[string]time.Time)
	}
	if last, ok := o.lastFlush[client.Node.ID]; ok && time.Since(last) < flushCoalesceWindow {
		o.flushMu.Unlock()
		return nil
	}
	o.flushMu.Unlock()

	if _, err := client.DB.ExecContext(ctx, "SYSTEM FLUSH LOGS query_log, part_log"); err != nil {
		return fmt.Errorf("flush query logs before foreign-mover guard: %w", err)
	}
	o.flushMu.Lock()
	o.lastFlush[client.Node.ID] = time.Now()
	o.flushMu.Unlock()
	return nil
}

func (o *SQLObserver) collectLiveMoveStatements(ctx context.Context, db *sql.DB, table TableObservation, instanceID string) (ForeignMoveObservation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			query_id,
			if(mapContains(Settings, 'log_comment'), Settings['log_comment'], '') AS log_comment,
			query
		FROM system.processes
		WHERE query_kind = 'Alter'
			AND positionCaseInsensitive(query, 'MOVE PARTITION') > 0
			AND positionCaseInsensitive(query, ?) > 0
	`, table.Table)
	if err != nil {
		return ForeignMoveObservation{}, fmt.Errorf("read system.processes for foreign moves: %w", err)
	}
	defer rows.Close()

	return classifyMoveStatementRows(rows, instanceID)
}

func (o *SQLObserver) collectActiveMoveEvents(ctx context.Context, db *sql.DB, table TableObservation) (ForeignMoveObservation, error) {
	allowed := makeStringSet(table.InFlightPartNames)
	rows, err := db.QueryContext(ctx, `
		SELECT part_name, target_disk_name
		FROM system.moves
		WHERE database = ? AND table = ?
	`, table.Database, table.Table)
	if err != nil {
		return ForeignMoveObservation{}, fmt.Errorf("read system.moves for active move events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var partName string
		var targetDisk string
		if scanErr := rows.Scan(&partName, &targetDisk); scanErr != nil {
			return ForeignMoveObservation{}, scanErr
		}
		if _, ok := allowed[partName]; ok {
			continue
		}
		return ForeignMoveObservation{ForeignActivity: true, Message: fmt.Sprintf("anonymous active move of part %s to disk %s", partName, targetDisk)}, nil
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return ForeignMoveObservation{}, rowsErr
	}
	return ForeignMoveObservation{}, nil
}

// dateTime64MicrosParam renders a timestamp at full microsecond precision for
// comparison against DateTime64(6) columns via toDateTime64(?, 6). Binding a
// time.Time directly truncates to whole seconds, which let events from the
// same second as the cutoff leak across it (observed: a predecessor's final
// MOVE, 66ms before boot, classified as a live duplicate instance — a
// permanent hard pause). Every *_microseconds predicate must use this; a
// source-scan test enforces it.
func dateTime64MicrosParam(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05.000000")
}

// collectRecentMoveStatements looks for MOVE statements that started AFTER
// this reconciler's boot reference. Pre-boot statements are history, not live
// overlap — a predecessor's finished moves (or the fixture's own seeding)
// must never wedge the controller into a permanent pause. A predecessor's
// still-running moves are caught by the event layer (system.moves) instead,
// which drains naturally.
func (o *SQLObserver) collectRecentMoveStatements(ctx context.Context, db *sql.DB, table TableObservation, instanceID string, bootTime time.Time) (ForeignMoveObservation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			query_id,
			coalesce(log_comment, '') AS log_comment,
			query
		FROM system.query_log
		WHERE type = 'QueryStart'
			AND query_kind = 'Alter'
			AND event_date >= today() - 1
			AND query_start_time_microseconds >= toDateTime64(?, 6)
			AND positionCaseInsensitive(query, 'MOVE PARTITION') > 0
			AND positionCaseInsensitive(query, ?) > 0
		ORDER BY query_start_time_microseconds DESC
		LIMIT 20
	`, dateTime64MicrosParam(bootTime), table.Table)
	if err != nil {
		return ForeignMoveObservation{}, fmt.Errorf("read system.query_log for foreign moves: %w", err)
	}
	defer rows.Close()

	return classifyMoveStatementRows(rows, instanceID)
}

func (o *SQLObserver) collectAnonymousMovePartEvents(ctx context.Context, db *sql.DB, table TableObservation, bootTime time.Time) (ForeignMoveObservation, error) {
	return o.collectAnonymousMovePartEventsSince(ctx, db, table, bootTime, bootTime)
}

func (o *SQLObserver) collectAnonymousMovePartEventsSince(ctx context.Context, db *sql.DB, table TableObservation, statementSince time.Time, eventSince time.Time) (ForeignMoveObservation, error) {
	statementPartitions, err := o.collectRecentMoveStatementPartitions(ctx, db, table, statementSince)
	if err != nil {
		return ForeignMoveObservation{}, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT partition_id, part_name
		FROM system.part_log
		WHERE database = ?
				AND table = ?
				AND event_type = 'MovePart'
				AND event_time_microseconds >= toDateTime64(?, 6)
			ORDER BY event_time_microseconds DESC
			LIMIT 20
	`, table.Database, table.Table, dateTime64MicrosParam(eventSince))
	if err != nil {
		return ForeignMoveObservation{}, fmt.Errorf("read system.part_log for anonymous move events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var partitionID string
		var partName string
		if scanErr := rows.Scan(&partitionID, &partName); scanErr != nil {
			return ForeignMoveObservation{}, scanErr
		}
		if _, ok := statementPartitions[partitionID]; ok {
			continue
		}
		return ForeignMoveObservation{ForeignActivity: true, Message: fmt.Sprintf("anonymous MovePart event for partition %s part %s", partitionID, partName)}, nil
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return ForeignMoveObservation{}, rowsErr
	}
	return ForeignMoveObservation{}, nil
}

func (o *SQLObserver) collectRecentMoveStatementPartitions(ctx context.Context, db *sql.DB, table TableObservation, bootTime time.Time) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT query
		FROM system.query_log
		WHERE type = 'QueryStart'
			AND query_kind = 'Alter'
			AND event_date >= today() - 1
			AND query_start_time_microseconds >= toDateTime64(?, 6)
			AND positionCaseInsensitive(query, 'MOVE PARTITION') > 0
			AND positionCaseInsensitive(query, ?) > 0
		LIMIT 100
	`, dateTime64MicrosParam(bootTime), table.Table)
	if err != nil {
		return nil, fmt.Errorf("read system.query_log for move statement partitions: %w", err)
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var query string
		if scanErr := rows.Scan(&query); scanErr != nil {
			return nil, scanErr
		}
		if partitionID, ok := extractMovePartitionID(query); ok {
			out[partitionID] = struct{}{}
		}
	}
	return out, rows.Err()
}

func classifyMoveStatementRows(rows *sql.Rows, instanceID string) (ForeignMoveObservation, error) {
	for rows.Next() {
		var queryID string
		var logComment string
		var query string
		if err := rows.Scan(&queryID, &logComment, &query); err != nil {
			return ForeignMoveObservation{}, err
		}
		switch classifyMoveStatement(queryID, logComment, instanceID) {
		case moveStatementOurs:
			continue
		case moveStatementDuplicate:
			return ForeignMoveObservation{DuplicateInstance: true, Message: "another movoor instance issued ALTER MOVE on this node"}, nil
		case moveStatementForeign:
			return ForeignMoveObservation{ForeignActivity: true, Message: "foreign ALTER MOVE activity observed on this node"}, nil
		}
	}
	if err := rows.Err(); err != nil {
		return ForeignMoveObservation{}, err
	}
	return ForeignMoveObservation{}, nil
}

type moveStatementClass int

const (
	moveStatementOurs moveStatementClass = iota
	moveStatementDuplicate
	moveStatementForeign
)

func classifyMoveStatement(queryID string, logComment string, instanceID string) moveStatementClass {
	comment := strings.TrimSpace(logComment)
	queryIsMovoor := strings.HasPrefix(queryID, "movoor-")
	commentIsMovoor := strings.HasPrefix(comment, "movoor:")
	if !queryIsMovoor && !commentIsMovoor {
		return moveStatementForeign
	}
	if comment == "movoor:"+instanceID || strings.Contains(queryID, instanceID) {
		return moveStatementOurs
	}
	return moveStatementDuplicate
}

func extractMovePartitionID(query string) (string, bool) {
	idx := strings.Index(strings.ToLower(query), "partition id")
	if idx < 0 {
		return "", false
	}
	tail := strings.TrimSpace(query[idx+len("partition id"):])
	if !strings.HasPrefix(tail, "'") {
		return "", false
	}
	value, err := readSQLString(tail)
	if err != nil {
		return "", false
	}
	return value, true
}

func readSQLString(raw string) (string, error) {
	var b strings.Builder
	for i := 1; i < len(raw); i++ {
		ch := raw[i]
		switch ch {
		case '\'':
			if i+1 < len(raw) && raw[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), nil
		case '\\':
			if i+1 < len(raw) {
				i++
				b.WriteByte(raw[i])
				continue
			}
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	return "", fmt.Errorf("SQL string %q is unterminated", raw)
}

func makeStringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func (o *SQLObserver) RefreshPartition(ctx context.Context, client chclient.Client, table TableObservation, partitionID string) (PartitionObservation, error) {
	queryCtx, cancel := o.queryContext(ctx)
	defer cancel()

	parts, err := o.collectPartitionRollup(queryCtx, client.DB, table)
	if err != nil {
		return PartitionObservation{}, err
	}
	for _, part := range parts {
		if part.PartitionID == partitionID {
			return part, nil
		}
	}
	return PartitionObservation{}, sql.ErrNoRows
}

func (o *SQLObserver) collectTableMetadata(ctx context.Context, db *sql.DB, obs *TableObservation) error {
	var isReplicated uint8
	if err := db.QueryRowContext(ctx, `
		SELECT
			uuid,
			engine,
			storage_policy,
			partition_key,
			startsWith(engine, 'Replicated') AS is_replicated,
			version(),
			now64(6)
		FROM system.tables
		WHERE database = ? AND name = ?
		LIMIT 1
	`, obs.Database, obs.Table).Scan(
		&obs.UUID,
		&obs.Engine,
		&obs.StoragePolicy,
		&obs.PartitionKey,
		&isReplicated,
		&obs.Version,
		&obs.ObservedAt,
	); err != nil {
		return fmt.Errorf("read system.tables for %s.%s: %w", obs.Database, obs.Table, err)
	}
	obs.IsReplicated = isReplicated == 1
	return nil
}

func (o *SQLObserver) applyColumnTimezone(ctx context.Context, db *sql.DB, layout *TableLayout) error {
	if layout.Basis != AgeBasisPartitionTime || layout.TimeZone != "" || layout.AgeField == "" {
		return nil
	}
	var columnType string
	if err := db.QueryRowContext(ctx, `
		SELECT type
		FROM system.columns
		WHERE database = ? AND table = ? AND name = ?
		LIMIT 1
	`, layout.Database, layout.Table, layout.AgeField).Scan(&columnType); err != nil {
		return fmt.Errorf("read timezone for partition column %s.%s.%s: %w", layout.Database, layout.Table, layout.AgeField, err)
	}
	layout.TimeZone = parseColumnTimezone(columnType)
	if layout.TimeZone == "" {
		return nil
	}
	if _, err := time.LoadLocation(layout.TimeZone); err != nil {
		return fmt.Errorf("partition column %s.%s.%s uses unsupported timezone %q: %w", layout.Database, layout.Table, layout.AgeField, layout.TimeZone, err)
	}
	return nil
}

func parseColumnTimezone(columnType string) string {
	start := strings.Index(columnType, "'")
	if start < 0 {
		return ""
	}
	value, tail, err := readQuotedTupleString(columnType[start:])
	if err != nil || !strings.Contains(strings.TrimSpace(tail), ")") {
		return ""
	}
	return value
}

func applyVersionGate(obs *TableObservation) {
	major, minor, ok := clickHouseMajorMinor(obs.Version)
	if !ok {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "clickhouse_version_unknown", "ClickHouse version could not be parsed; enforce remains allowed but this version is outside the validated corpus", obs.Node.ID, obs.Database, obs.Table, "", ""))
		return
	}
	if major != 26 {
		if obs.EffectiveMode == ModeEnforce {
			obs.EffectiveMode = ModePlan
		}
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "clickhouse_major_unvalidated", "ClickHouse major version is outside the validated 26.2.x executor corpus; enforce is downgraded to plan", obs.Node.ID, obs.Database, obs.Table, "", ""))
		return
	}
	if minor != 2 {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "clickhouse_minor_unvalidated", "ClickHouse minor version differs from the validated 26.2.x executor corpus", obs.Node.ID, obs.Database, obs.Table, "", ""))
	}
}

func validateTieringEngine(engine string) error {
	if !strings.Contains(engine, "MergeTree") {
		return fmt.Errorf("engine %q is not a supported local MergeTree-family engine", engine)
	}
	return nil
}

func clickHouseMajorMinor(version string) (int, int, bool) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

func (o *SQLObserver) collectStoragePolicy(ctx context.Context, db *sql.DB, obs *TableObservation) error {
	rows, err := db.QueryContext(ctx, `
		SELECT
			policy_name,
			volume_name,
			volume_priority,
			arrayStringConcat(disks, ',') AS disks_csv,
			move_factor
		FROM system.storage_policies
		WHERE policy_name = ?
		ORDER BY volume_priority
	`, obs.StoragePolicy)
	if err != nil {
		return err
	}
	defer rows.Close()

	var volumes []StorageVolume
	for rows.Next() {
		var volume StorageVolume
		var disksCSV string
		if scanErr := rows.Scan(&volume.Policy, &volume.Volume, &volume.Priority, &disksCSV, &volume.MoveFactor); scanErr != nil {
			return scanErr
		}
		if disksCSV != "" {
			volume.Disks = strings.Split(disksCSV, ",")
		}
		volumes = append(volumes, volume)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return rowsErr
	}
	if len(volumes) == 0 {
		return fmt.Errorf("storage policy %q was not observed", obs.StoragePolicy)
	}

	targetVolumeIndex := -1
	for i, volume := range volumes {
		if slices.Contains(volume.Disks, obs.Settings.TargetDisk) {
			obs.TargetDiskFound = true
			targetVolumeIndex = i
			break
		}
	}
	if !obs.TargetDiskFound {
		obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityCritical, "target_disk_missing", "target disk is not present in the table storage policy", obs.Node.ID, obs.Database, obs.Table, "", ""))
	}
	if obs.Settings.HotVolume != "" {
		// A typo'd hotVolume must surface at observation time as a critical
		// condition, not hours later when the first hot-ward ALTER fails.
		obs.HotVolume = obs.Settings.HotVolume
		if !slices.ContainsFunc(volumes, func(v StorageVolume) bool { return v.Volume == obs.Settings.HotVolume }) {
			obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityCritical, "hot_volume_missing", fmt.Sprintf("configured hotVolume %q is not present in storage policy %q", obs.Settings.HotVolume, obs.StoragePolicy), obs.Node.ID, obs.Database, obs.Table, "", ""))
		}
	} else if targetVolumeIndex > 0 {
		obs.HotVolume = volumes[targetVolumeIndex-1].Volume
	}
	for _, volume := range volumes {
		if volume.Volume == obs.HotVolume && volume.MoveFactor > 0 {
			obs.Conditions = append(obs.Conditions, NewCondition(ConditionSeverityWarning, "hot_volume_move_factor", "hot volume move_factor is greater than zero", obs.Node.ID, obs.Database, obs.Table, "", ""))
		}
	}
	return nil
}

func (o *SQLObserver) collectFrontierHeads(ctx context.Context, db *sql.DB, layout TableLayout) (map[string]int64, error) {
	groupExpr := "''"
	if len(layout.GroupColumns) > 0 {
		quoted := make([]string, 0, len(layout.GroupColumns))
		for _, group := range layout.GroupColumns {
			quoted = append(quoted, "toString("+QuoteIdent(group)+")")
		}
		groupExpr = strings.Join(quoted, ", '\\x00', ")
		if len(quoted) > 1 {
			groupExpr = "concat(" + groupExpr + ")"
		}
	}
	//nolint:gosec // ClickHouse cannot bind identifiers; all dynamic fragments are quoted expressions.
	query := "SELECT " + groupExpr + " AS group_key, max(" + QuoteIdent(layout.AgeField) + ") AS head FROM " + QuoteQualified(layout.Database, layout.Table) + " GROUP BY group_key"
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	heads := make(map[string]int64)
	for rows.Next() {
		var group string
		var head int64
		if scanErr := rows.Scan(&group, &head); scanErr != nil {
			return nil, scanErr
		}
		heads[group] = head
	}
	return heads, rows.Err()
}

func (o *SQLObserver) collectReplica(ctx context.Context, db *sql.DB, database string, table string, replicated bool) (*ReplicaObservation, error) {
	if !replicated {
		return nil, nil
	}
	var obs ReplicaObservation
	if err := db.QueryRowContext(ctx, `
		SELECT
			is_readonly,
			is_session_expired,
			queue_size,
			absolute_delay
		FROM system.replicas
		WHERE database = ? AND table = ?
		LIMIT 1
	`, database, table).Scan(&obs.Readonly, &obs.SessionExpired, &obs.QueueSize, &obs.AbsoluteDelaySeconds); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT arrayStringConcat(parts_to_merge, ',') AS parts_to_merge_csv
		FROM system.replication_queue
		WHERE database = ? AND table = ? AND type = 'MERGE_PARTS'
	`, database, table)
	if err != nil {
		return &obs, nil
	}
	defer rows.Close()
	for rows.Next() {
		var partsCSV string
		if scanErr := rows.Scan(&partsCSV); scanErr != nil {
			return &obs, nil
		}
		if partsCSV != "" {
			obs.MergeQueue = append(obs.MergeQueue, strings.Split(partsCSV, ",")...)
		}
	}
	return &obs, nil
}

func (o *SQLObserver) collectMutations(ctx context.Context, db *sql.DB, database string, table string) ([]MutationObservation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			mutation_id,
			command,
			is_done,
			parts_to_do,
			arrayStringConcat(parts_to_do_names, ',') AS parts_to_do_names_csv,
			coalesce(latest_failed_part, ''),
			coalesce(latest_fail_reason, '')
		FROM system.mutations
		WHERE database = ? AND table = ? AND is_done = 0
		ORDER BY create_time
	`, database, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MutationObservation
	for rows.Next() {
		var mutation MutationObservation
		var partsToDoCSV string
		if scanErr := rows.Scan(
			&mutation.MutationID,
			&mutation.Command,
			&mutation.IsDone,
			&mutation.PartsToDo,
			&partsToDoCSV,
			&mutation.LatestFailedPart,
			&mutation.LatestFailReason,
		); scanErr != nil {
			return nil, scanErr
		}
		if partsToDoCSV != "" {
			mutation.PartsToDoNames = strings.Split(partsToDoCSV, ",")
		}
		out = append(out, mutation)
	}
	return out, rows.Err()
}

type partLogEvidence struct {
	LatestNewPart *time.Time
	LatestAny     *time.Time
}

// partLogLookbackDays bounds the evidence scan to the largest window any gate
// compares against (quiet windows, the adaptive resplit ceiling, the dead-group
// horizon) plus a margin. Events older than every gate window are equivalent
// to "quiet" for all consumers; the one behavioral edge is a table with zero
// part_log activity inside the bound, whose coverage probe reads as absent and
// seals through the documented modification-time fallback instead.
func partLogLookbackDays(settings TierSettings) int {
	lookback := max(
		settings.QuietFor.Duration,
		settings.Resplit.QuietFor.Duration,
		settings.TierFrozenAfter.Duration,
		maxResplitQuiet,
	) + 48*time.Hour
	return int(lookback/(24*time.Hour)) + 1
}

func (o *SQLObserver) collectPartLogEvidence(ctx context.Context, db *sql.DB, database string, table string, lookbackDays int) (map[string]partLogEvidence, *time.Time, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			partition_id,
			maxIf(event_time, event_type = 'NewPart') AS latest_new_part,
			max(event_time) AS latest_any,
			min(event_time) AS min_event_time
		FROM system.part_log
		WHERE database = ? AND table = ?
			AND event_date >= today() - ?
		GROUP BY partition_id
	`, database, table, lookbackDays)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	out := make(map[string]partLogEvidence)
	var minObserved *time.Time
	for rows.Next() {
		var id string
		var latestNew sql.NullTime
		var latestAny sql.NullTime
		var minEvent sql.NullTime
		if scanErr := rows.Scan(&id, &latestNew, &latestAny, &minEvent); scanErr != nil {
			return nil, nil, scanErr
		}
		evidence := partLogEvidence{}
		// maxIf returns the epoch zero when no rows match the condition — a
		// partition with merge/move events but no inserts must read as
		// "no insert evidence", not "inserted in 1970".
		if latestNew.Valid && latestNew.Time.Unix() > 0 {
			evidence.LatestNewPart = &latestNew.Time
		}
		if latestAny.Valid && latestAny.Time.Unix() > 0 {
			evidence.LatestAny = &latestAny.Time
		}
		if minEvent.Valid && (minObserved == nil || minEvent.Time.Before(*minObserved)) {
			t := minEvent.Time
			minObserved = &t
		}
		out[id] = evidence
	}
	return out, minObserved, rows.Err()
}

func (o *SQLObserver) collectPartitionRollup(ctx context.Context, db *sql.DB, table TableObservation) ([]PartitionObservation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			partition,
			partition_id,
			name,
			hash_of_all_files,
			disk_name,
			rows,
			bytes_on_disk,
			modification_time
		FROM system.parts
		WHERE database = ? AND table = ? AND active
		ORDER BY partition_id, name
	`, table.Database, table.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := make(map[string]*PartitionObservation)
	order := make([]string, 0)
	for rows.Next() {
		var partition string
		var partitionID string
		var partName string
		var hash string
		var disk string
		var partRows uint64
		var bytesOnDisk uint64
		var modificationTime time.Time
		if scanErr := rows.Scan(
			&partition,
			&partitionID,
			&partName,
			&hash,
			&disk,
			&partRows,
			&bytesOnDisk,
			&modificationTime,
		); scanErr != nil {
			return nil, scanErr
		}
		item, ok := byID[partitionID]
		if !ok {
			parsed, parseErr := table.Layout.ParsePartition(partition)
			item = &PartitionObservation{
				Partition:   partition,
				PartitionID: partitionID,
			}
			if parseErr == nil {
				item.GroupKey = parsed.GroupKey
				item.AgeString = parsed.AgeString
				item.AgeInteger = parsed.AgeInteger
			}
			byID[partitionID] = item
			order = append(order, partitionID)
		}
		item.ActiveParts++
		item.Rows += partRows
		item.BytesOnDisk += bytesOnDisk
		if modificationTime.After(item.MaxModificationTime) {
			item.MaxModificationTime = modificationTime
		}
		addDiskPart(item, disk)
		item.Hashes = append(item.Hashes, PartHash{Name: partName, Hash: hash, Disk: disk})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}
	out := make([]PartitionObservation, 0, len(order))
	for _, id := range order {
		item := byID[id]
		slices.SortFunc(item.Disks, func(a DiskPart, b DiskPart) int {
			return strings.Compare(a.Disk, b.Disk)
		})
		out = append(out, *item)
	}
	return out, nil
}

func addDiskPart(item *PartitionObservation, disk string) {
	for i := range item.Disks {
		if item.Disks[i].Disk == disk {
			item.Disks[i].Parts++
			return
		}
	}
	item.Disks = append(item.Disks, DiskPart{Disk: disk, Parts: 1})
}

func (o *SQLObserver) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if o.QueryTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, o.QueryTimeout)
}
