//nolint:modernize // Pointer helpers keep decision fixtures readable.
package tiering

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

func TestDecideTableAndPartitionScenarios(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	obs := frontierObservation(now)

	verdicts := DecideTable(obs, now)
	require.Len(t, verdicts, 5)
	byID := map[string]Verdict{}
	for _, verdict := range verdicts {
		byID[verdict.PartitionID] = verdict
		require.NotEmpty(t, verdict.Token)
	}
	require.Equal(t, DecisionNone, byID["cold"].Decision)
	require.Equal(t, StatusTiered, byID["cold"].Status)
	// The default remerge strategy starts the consolidation chain with its
	// first leg only; optimize and tier follow from re-classification.
	require.Equal(t, DecisionConsolidate, byID["split"].Decision)
	require.Equal(t, StatusSplit, byID["split"].Status)
	require.Equal(t, DecisionTier, byID["hot-old"].Decision)
	require.Equal(t, StatusReady, byID["hot-old"].Status)
	require.Equal(t, DecisionKeep, byID["hot-young"].Decision)
	require.Equal(t, DecisionNone, byID["excluded"].Decision)
}

func TestDecideHotLegSelection(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	obs := frontierObservation(now)
	hot := obs.Partitions[2] // "hot-old": age-eligible, sealed, all on default

	// Multi-part hot partition merges before it ships.
	hot.ActiveParts = 3
	require.Equal(t, DecisionOptimize, DecidePartition(obs, hot, now).Decision)

	// At the part target the optimize leg is skipped: move only.
	hot.ActiveParts = 1
	require.Equal(t, DecisionTier, DecidePartition(obs, hot, now).Decision)

	// skipOptimize tables never merge, regardless of part count.
	obs.Settings.SkipOptimize = true
	hot.ActiveParts = 3
	require.Equal(t, DecisionTier, DecidePartition(obs, hot, now).Decision)

	// Oversized partitions move as-is rather than forcing a giant merge.
	obs.Settings.SkipOptimize = false
	obs.Settings.OptimizeSkipAboveBytes = Bytes{Value: 1}
	hot.BytesOnDisk = 2
	require.Equal(t, DecisionTier, DecidePartition(obs, hot, now).Decision)
}

func TestDecideHoldsMergeInFlight(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	obs := frontierObservation(now)
	hot := obs.Partitions[2] // "hot-old": age-eligible, sealed, all on default

	require.Equal(t, DecisionTier, DecidePartition(obs, hot, now).Decision)

	hot.MergeInFlight = true
	verdict := DecidePartition(obs, hot, now)
	require.Equal(t, DecisionHold, verdict.Decision)
	require.Contains(t, verdict.Reason, "merge is currently running")
	require.NotNil(t, verdict.Hold)
	require.Equal(t, "merge-in-flight", verdict.Hold.Gate)

	// The gate guards split repairs and cold-side merges too — sealed() is the
	// shared admission path.
	split := obs.Partitions[1] // "split"
	split.MergeInFlight = true
	verdict = DecidePartition(obs, split, now)
	require.Equal(t, DecisionHold, verdict.Decision)
	require.Equal(t, "merge-in-flight", verdict.Hold.Gate)
}

func TestDecideOptimizeOnColdMatrix(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	obs := frontierObservation(now)
	obs.Settings.OptimizeOn = OptimizeOnCold

	// First tier ships hot parts as-is; the merge happens on the cold side.
	hot := obs.Partitions[2]
	hot.ActiveParts = 3
	require.Equal(t, DecisionTier, DecidePartition(obs, hot, now).Decision)

	// A fragmented on-target partition merges in place instead of resting.
	cold := obs.Partitions[0]
	cold.ActiveParts = 3
	verdict := DecidePartition(obs, cold, now)
	require.Equal(t, StatusTiered, verdict.Status)
	require.Equal(t, DecisionOptimize, verdict.Decision)

	// At the part target the ratchet behaves exactly as before.
	cold.ActiveParts = 1
	verdict = DecidePartition(obs, cold, now)
	require.Equal(t, DecisionNone, verdict.Decision)

	// Splits consolidate toward the cold side (no hot round-trip).
	split := obs.Partitions[1]
	verdict = DecidePartition(obs, split, now)
	require.Equal(t, DecisionConsolidate, verdict.Decision)
	require.Contains(t, verdict.Reason, "cold side")

	// Unsealed fragmented cold partitions hold instead of merging.
	obs.PartLogMinTime = ptrTime(now.Add(-time.Minute))
	cold.ActiveParts = 3
	cold.MaxModificationTime = now.Add(-time.Minute)
	verdict = DecidePartition(obs, cold, now)
	require.Equal(t, DecisionHold, verdict.Decision)
	require.Contains(t, verdict.Reason, "waiting to merge")
}

func TestDecideTableCriticalConditionDisables(t *testing.T) {
	now := time.Now()
	obs := frontierObservation(now)
	obs.Conditions = []Condition{
		NewCondition(ConditionSeverityWarning, "warn", "warning", "n1", "db", "tbl", "", ""),
		NewCondition(ConditionSeverityCritical, "bad", "critical", "n1", "db", "tbl", "", ""),
	}
	verdicts := DecideTable(obs, now)
	require.NotEmpty(t, verdicts)
	for _, verdict := range verdicts {
		require.Equal(t, StatusMisconfigured, verdict.Status)
		require.Equal(t, DecisionHold, verdict.Decision)
		require.Len(t, verdict.Conditions, 1)
	}
	obs.Partitions = nil
	require.Len(t, DecideTable(obs, now), 1)
}

func TestDecideSplitStrategies(t *testing.T) {
	now := time.Now()
	obs := frontierObservation(now)
	split := obs.Partitions[1]

	for _, tt := range []struct {
		strategy     ResplitStrategy
		parts        uint64
		skipOptimize bool
		decision     Decision
		conds        int
	}{
		{strategy: ResplitStrategyHold, decision: DecisionHold},
		{strategy: ResplitStrategyAppend, parts: 8, decision: DecisionAppend, conds: 1},
		{strategy: ResplitStrategyRemerge, parts: 2, decision: DecisionConsolidate},
		{strategy: ResplitStrategyAuto, parts: 8, decision: DecisionConsolidate},
		{strategy: ResplitStrategyAuto, parts: 2, decision: DecisionAppend},
		// Consolidation that could never optimize degrades to append loudly.
		{strategy: ResplitStrategyRemerge, parts: 2, skipOptimize: true, decision: DecisionAppend, conds: 1},
		{strategy: "weird", parts: 2, decision: DecisionHold},
	} {
		obs.Settings.Resplit.Strategy = tt.strategy
		obs.Settings.SkipOptimize = tt.skipOptimize
		if tt.parts > 0 {
			split.ActiveParts = tt.parts
		}
		verdict := DecidePartition(obs, split, now)
		require.Equal(t, tt.decision, verdict.Decision)
		require.Len(t, verdict.Conditions, tt.conds)
	}
	obs.Settings.SkipOptimize = false

	split.LatestNewPart = ptrTime(now.Add(-time.Second))
	obs.Settings.Resplit.QuietFor = Duration{Duration: time.Hour}
	verdict := DecidePartition(obs, split, now)
	require.Equal(t, DecisionHold, verdict.Decision)
	require.Contains(t, verdict.Reason, "resplit quiet")

	obs.ResplitQuiet = map[string]time.Duration{split.PartitionID: 3 * time.Hour}
	split.MaxModificationTime = now.Add(-2 * time.Hour)
	verdict = DecidePartition(obs, split, now)
	require.Equal(t, DecisionHold, verdict.Decision)
}

func TestAgeEligibilityPartitionTime(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	obs := timeObservation(now, "toYYYYMM", "202604")
	eligible, reason := ageEligible(obs, obs.Partitions[0], now)
	require.True(t, eligible, reason)

	for _, tt := range []struct {
		fn    string
		value string
		ok    bool
	}{
		{fn: "toYYYYMM", value: "202606"},
		{fn: "toYYYYMMDD", value: "20260401", ok: true},
		{fn: "toYYYYMMDD", value: "20260609"},
		{fn: "toDate", value: "2026-04-01", ok: true},
		{fn: "toStartOfMonth", value: "2026-06-01"},
		{fn: "toStartOfWeek", value: "2026-04-01 00:00:00", ok: true},
		{fn: "unknown", value: "202604"},
		{fn: "toYYYYMM", value: "nope"},
		{fn: "toYYYYMMDD", value: "20261399"},
		{fn: "toDate", value: "bad"},
	} {
		obs = timeObservation(now, tt.fn, tt.value)
		got, _ := ageEligible(obs, obs.Partitions[0], now)
		require.Equal(t, tt.ok, got, tt)
	}

	obs = timeObservation(now, "toYYYYMM", "202605")
	got, reason := ageEligible(obs, obs.Partitions[0], now)
	require.False(t, got)
	require.Contains(t, reason, "overlaps")
	hold := keepHoldDetail(obs, obs.Partitions[0])
	require.Equal(t, "age", hold.Gate)
	require.Equal(t, obs.Settings.Age.OlderThan.String(), hold.Window)
	require.NotNil(t, hold.ReleasesAt)

	tokyoNow := time.Date(2026, 6, 1, 0, 30, 0, 0, time.FixedZone("JST", 9*60*60))
	obs = timeObservation(tokyoNow.UTC(), "toYYYYMM", "202605")
	obs.Layout.TimeZone = "Asia/Tokyo"
	obs.Settings.Age.OlderThan = Duration{Duration: 0}
	got, reason = ageEligible(obs, obs.Partitions[0], tokyoNow.UTC())
	require.True(t, got, reason)

	obs.Layout.TimeZone = "No/SuchZone"
	got, reason = ageEligible(obs, obs.Partitions[0], now)
	require.False(t, got)
	require.Contains(t, reason, "timezone")
	hold = keepHoldDetail(obs, obs.Partitions[0])
	require.Equal(t, "age", hold.Gate)
	require.Empty(t, hold.Window)

	obs.Layout.TimeZone = ""
	obs.Partitions[0].AgeString = "bad"
	hold = keepHoldDetail(obs, obs.Partitions[0])
	require.Equal(t, "age", hold.Gate)
	require.Empty(t, hold.Window)
}

//nolint:funlen // Sealed-gate fixtures stay together so hold precedence remains visible.
func TestSealedHoldsAndFallbacks(t *testing.T) {
	now := time.Now()
	obs := frontierObservation(now)
	part := obs.Partitions[2]

	obs.Replica = &ReplicaObservation{Readonly: true}
	ok, reason, conditions := sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "readonly")
	require.Len(t, conditions, 1)

	obs.Replica = &ReplicaObservation{SessionExpired: true}
	ok, _, conditions = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Equal(t, "replica_session_expired", conditions[0].Code)

	obs.Replica = &ReplicaObservation{QueueSize: 21}
	ok, _, conditions = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Equal(t, "replica_queue_large", conditions[0].Code)

	obs.Replica = &ReplicaObservation{AbsoluteDelaySeconds: 301}
	ok, _, conditions = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Equal(t, "replica_delay_large", conditions[0].Code)

	obs.Replica = &ReplicaObservation{MergeQueue: []string{"hot-old-part"}}
	part.Hashes = []PartHash{{Name: "hot-old-part"}}
	ok, reason, conditions = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "queued merge")
	require.Equal(t, "replica_merge_queued", conditions[0].Code)
	require.False(t, replicaMergeQueued(part, []string{"other-part"}))
	ok, _, _ = replicaGate(TableObservation{Replica: &ReplicaObservation{}}, part)
	require.True(t, ok)

	obs.Replica = nil
	obs.Mutations = []MutationObservation{{MutationID: "m1", PartsToDo: 1, LatestFailReason: "bad mutation"}}
	ok, _, conditions = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Equal(t, "bad mutation", conditions[0].Message)
	ok, _, _ = mutationGate(TableObservation{Mutations: []MutationObservation{{PartsToDo: 0}}}, part)
	require.True(t, ok)

	obs.Mutations = nil
	part.LatestNewPart = ptrTime(now.Add(-time.Minute))
	ok, reason, _ = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "NewPart")

	// With insert evidence covering the window, the partition's own
	// modification time is ignored — convergence chains are never re-blocked
	// by their own previous legs.
	part.LatestNewPart = nil
	part.MaxModificationTime = now.Add(-time.Minute)
	ok, reason, _ = sealedTriple(obs, part, time.Hour, now)
	require.True(t, ok)
	require.Contains(t, reason, "insert evidence")

	// When part_log coverage is shorter than the window, the gate degrades to
	// the modification-time clock and holds on recent physical change.
	savedMin := obs.PartLogMinTime
	obs.PartLogMinTime = ptrTime(now.Add(-time.Minute))
	ok, reason, _ = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "physical")
	obs.PartLogMinTime = savedMin

	part.MaxModificationTime = now.Add(-2 * time.Hour)
	obs.PartLogMinTime = nil
	ok, reason, conditions = sealedTriple(obs, part, time.Hour, now)
	require.True(t, ok)
	require.Contains(t, reason, "fallback")
	require.Equal(t, "part_log_coverage_shortfall", conditions[0].Code)

	obs.Settings.SealedSignal = SealedSignalModificationTime
	ok, reason, conditions = sealedTriple(obs, part, time.Hour, now)
	require.True(t, ok)
	require.Contains(t, reason, "quiet")
	require.Empty(t, conditions)

	obs.Settings.SealedSignal = SealedSignalPartLog
	obs.Conditions = []Condition{{Code: "part_log_unreadable"}}
	ok, reason, conditions = sealedTriple(obs, part, time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "unavailable")
	require.Equal(t, "part_log_unreadable", conditions[0].Code)

	obs.Settings.SealedSignal = SealedSignalPartLog
	obs.Conditions = nil
	obs.PartLogMinTime = ptrTime(now.Add(-2 * time.Hour))
	ok, reason, conditions = sealedTriple(obs, part, time.Hour, now)
	require.True(t, ok)
	require.Contains(t, reason, "quiet")
	require.Empty(t, conditions)

	timeObs := timeObservation(now, "toYYYYMM", "202604")
	timeObs.Partitions = append(timeObs.Partitions, PartitionObservation{
		Partition:           "202605",
		PartitionID:         "next",
		AgeString:           "202605",
		ActiveParts:         1,
		MaxModificationTime: now.Add(-2 * time.Hour),
		LatestNewPart:       ptrTime(now.Add(-time.Hour)),
		Disks:               []DiskPart{{Disk: "default", Parts: 1}},
	})
	ok, reason, conditions = sealedTriple(timeObs, timeObs.Partitions[0], time.Hour, now)
	require.True(t, ok, reason)
	require.Empty(t, conditions)

	timeObs.Partitions = timeObs.Partitions[:1]
	ok, reason, conditions = sealedTriple(timeObs, timeObs.Partitions[0], time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "newer partition")
	require.Equal(t, "successor_activity_missing", conditions[0].Code)

	timeObs.Partitions = append(timeObs.Partitions, PartitionObservation{
		Partition:           "202605",
		PartitionID:         "stale-next",
		AgeString:           "202605",
		ActiveParts:         1,
		MaxModificationTime: now.Add(-2 * time.Hour),
		LatestNewPart:       ptrTime(now.Add(-2 * timeObs.Settings.TierFrozenAfter.Duration)),
		Disks:               []DiskPart{{Disk: "default", Parts: 1}},
	})
	ok, reason, conditions = sealedTriple(timeObs, timeObs.Partitions[0], time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "newer partition")
	require.Equal(t, "successor_activity_missing", conditions[0].Code)

	timeObs.Layout.TimeZone = "No/SuchZone"
	ok, reason, conditions = sealedTriple(timeObs, timeObs.Partitions[0], time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "timezone")
	require.Equal(t, "partition_time_unreadable", conditions[0].Code)

	timeObs.Layout.TimeZone = ""
	timeObs.Partitions[0].AgeString = "bad"
	ok, reason, conditions = sealedTriple(timeObs, timeObs.Partitions[0], time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "YYYYMM")
	require.Equal(t, "partition_time_unreadable", conditions[0].Code)

	timeObs = timeObservation(now, "toYYYYMM", "202604")
	timeObs.Partitions[0].MaxModificationTime = now.Add(-2 * timeObs.Settings.TierFrozenAfter.Duration)
	ok, reason, conditions = sealedTriple(timeObs, timeObs.Partitions[0], time.Hour, now)
	require.True(t, ok, reason)
	require.Empty(t, conditions)

	timeObs.PartLogMinTime = nil
	timeObs.Partitions[0].MaxModificationTime = now.Add(-2 * time.Hour)
	ok, reason, conditions = sealedTriple(timeObs, timeObs.Partitions[0], time.Hour, now)
	require.False(t, ok)
	require.Contains(t, reason, "newer partition")
	require.Equal(t, "successor_activity_missing", conditions[0].Code)
	require.Equal(t, "successor-activity", sealed(timeObs, timeObs.Partitions[0], time.Hour, now).detail.Gate)

	otherGroup := timeObs
	otherGroup.Partitions = append(otherGroup.Partitions, PartitionObservation{
		Partition:           "202605",
		PartitionID:         "other-group",
		GroupKey:            "other",
		AgeString:           "202605",
		LatestNewPart:       ptrTime(now.Add(-time.Hour)),
		MaxModificationTime: now,
	})
	detail := successorHoldDetail(otherGroup, otherGroup.Partitions[0], now)
	require.NotNil(t, detail.ReleasesAt)

	withInsert := timeObs
	withInsert.PartLogMinTime = ptrTime(now.Add(-2 * withInsert.Settings.TierFrozenAfter.Duration))
	withInsert.Partitions[0].LatestNewPart = ptrTime(now.Add(-time.Hour))
	detail = successorHoldDetail(withInsert, withInsert.Partitions[0], now)
	require.NotNil(t, detail.ReleasesAt)

	unreadable := timeObs
	unreadable.Conditions = []Condition{{Code: "part_log_unreadable"}}
	require.False(t, insertEvidenceCovers(unreadable, time.Hour, now))
}

func TestParseTimeHelpers(t *testing.T) {
	_, err := parseYYYYMM("202613")
	require.Error(t, err)
	_, err = parseYYYYMMDD("20260299")
	require.Error(t, err)
	_, err = parseDateish("not-a-date")
	require.Error(t, err)
}

func frontierObservation(now time.Time) TableObservation {
	settings := DefaultTierSettings()
	settings.Age = AgeSettings{Basis: AgeBasisFrontier, Field: "block_number", KeepLast: 50}
	settings.ExcludePartitions = []string{"excluded"}
	settings.QuietFor = Duration{Duration: time.Hour}
	settings.Resplit.QuietFor = Duration{Duration: time.Hour}
	layout, err := ParseLayout("db", "tbl", "(network_id, intDiv(block_number, 100))", settings)
	if err != nil {
		panic(err)
	}
	old := now.Add(-2 * time.Hour)
	return TableObservation{
		Node:           chclient.Node{ID: "n1", Shard: "s1", Replica: "r1"},
		Database:       "db",
		Table:          "tbl",
		UUID:           "uuid",
		Layout:         layout,
		Settings:       settings,
		EffectiveMode:  ModePlan,
		HotVolume:      "hot",
		ObservedAt:     now,
		PartLogMinTime: ptrTime(now.Add(-3 * time.Hour)),
		Heads:          map[string]int64{"mainnet": 590},
		Partitions: []PartitionObservation{
			{Partition: "('mainnet',0)", PartitionID: "cold", GroupKey: "mainnet", AgeInteger: 0, ActiveParts: 1, Rows: 1, BytesOnDisk: 1, MaxModificationTime: old, Disks: []DiskPart{{Disk: "s3_cache", Parts: 1}}},
			{Partition: "('mainnet',1)", PartitionID: "split", GroupKey: "mainnet", AgeInteger: 1, ActiveParts: 2, Rows: 2, BytesOnDisk: 2, MaxModificationTime: old, Disks: []DiskPart{{Disk: "default", Parts: 1}, {Disk: "s3_cache", Parts: 1}}},
			{Partition: "('mainnet',2)", PartitionID: "hot-old", GroupKey: "mainnet", AgeInteger: 2, ActiveParts: 1, Rows: 3, BytesOnDisk: 3, MaxModificationTime: old, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
			{Partition: "('mainnet',5)", PartitionID: "hot-young", GroupKey: "mainnet", AgeInteger: 5, ActiveParts: 1, Rows: 4, BytesOnDisk: 4, MaxModificationTime: old, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
			{Partition: "('mainnet',3)", PartitionID: "excluded", GroupKey: "mainnet", AgeInteger: 3, ActiveParts: 1, Rows: 5, BytesOnDisk: 5, MaxModificationTime: old, Disks: []DiskPart{{Disk: "default", Parts: 1}}},
		},
	}
}

func timeObservation(now time.Time, fn string, value string) TableObservation {
	settings := DefaultTierSettings()
	settings.Age = AgeSettings{Basis: AgeBasisPartitionTime, OlderThan: Duration{Duration: 35 * 24 * time.Hour}}
	layout := TableLayout{Database: "db", Table: "tbl", Basis: AgeBasisPartitionTime, TimeFunction: fn, Generation: fn}
	old := now.Add(-2 * time.Hour)
	return TableObservation{
		Node:           chclient.Node{ID: "n1"},
		Database:       "db",
		Table:          "tbl",
		Layout:         layout,
		Settings:       settings,
		EffectiveMode:  ModePlan,
		ObservedAt:     now,
		PartLogMinTime: ptrTime(now.Add(-3 * time.Hour)),
		Partitions: []PartitionObservation{{
			Partition: value, PartitionID: "p", AgeString: value, ActiveParts: 1, MaxModificationTime: old, Disks: []DiskPart{{Disk: "default", Parts: 1}},
		}},
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

// sealedTriple adapts the structured sealCheck back to the historical triple
// shape so the dense gate-precedence assertions below stay readable.
//
//nolint:unparam // The gate-precedence cases all use the same one-hour window.
func sealedTriple(obs TableObservation, partition PartitionObservation, quietFor time.Duration, now time.Time) (bool, string, []Condition) {
	result := sealed(obs, partition, quietFor, now)
	return result.ok, result.reason, result.conditions
}

func TestFrontierOverflowRefusesEligibility(t *testing.T) {
	now := time.Now().UTC()
	obs := frontierObservation(now)
	hostile := obs.Partitions[2]
	hostile.AgeInteger = math.MaxInt64 / obs.Layout.FrontierDivisor
	eligible, reason := ageEligible(obs, hostile, now)
	require.False(t, eligible)
	require.Contains(t, reason, "overflow")

	hostile.AgeInteger = -1
	eligible, _ = ageEligible(obs, hostile, now)
	require.False(t, eligible)
}
