package tiering

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"
)

type Status string

const (
	StatusHot           Status = "hot"
	StatusReady         Status = "ready"
	StatusTiered        Status = "tiered"
	StatusSplit         Status = "split"
	StatusStalled       Status = "stalled"
	StatusMisconfigured Status = "misconfigured"
)

type Decision string

// Decisions are single legs, never journeys: each actionable decision maps to
// exactly one ClickHouse statement plus its convergence check. Multi-step
// flows (the old compound "remerge") emerge from re-classification — every
// cycle re-derives the next leg from observed disk placement and part counts,
// so a crash anywhere leaves no in-flight plan to recover.
const (
	DecisionNone Decision = "none"
	DecisionKeep Decision = "keep"
	DecisionHold Decision = "hold"
	// DecisionConsolidate co-locates a split partition's parts on the table's
	// optimize side so a later optimize leg can merge them: optimizeOn: hot
	// pulls cold strays back (the round-trip), optimizeOn: cold pushes hot
	// strays to the target disk and merges in place.
	DecisionConsolidate Decision = "consolidate"
	DecisionOptimize    Decision = "optimize"
	DecisionTier        Decision = "tier"
	DecisionAppend      Decision = "append"
)

// Replica-health gate thresholds. A replica above either bound is lagging
// hard enough that acting on it is wasted churn, so the partition defers (not
// errors) this cycle. These match the design's "~20" queue guidance.
const (
	replicaQueueSizeThreshold   uint64 = 20
	replicaAbsoluteDelayMaxSecs uint64 = 300
)

type ConditionSeverity string

const (
	ConditionSeverityInfo     ConditionSeverity = "info"
	ConditionSeverityWarning  ConditionSeverity = "warning"
	ConditionSeverityCritical ConditionSeverity = "critical"
)

type Condition struct {
	Severity    ConditionSeverity `json:"severity"`
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	ObservedAt  time.Time         `json:"observedAt"`
	NodeID      string            `json:"nodeId,omitempty"`
	Database    string            `json:"database,omitempty"`
	Table       string            `json:"table,omitempty"`
	Partition   string            `json:"partition,omitempty"`
	PartitionID string            `json:"partitionId,omitempty"`
}

// HoldDetail explains a hold/keep verdict structurally so clients can render
// the gate, the clock it is waiting on, and when it releases — the prose
// reason alone answers "why" but not "until when".
type HoldDetail struct {
	// Gate identifies the clause holding the partition: replica-health,
	// mutation, part-log-required, insert-quiet, physical-quiet,
	// successor-activity, age, or stalled.
	Gate       string
	Window     string
	LastInsert *time.Time
	LastChange *time.Time
	ReleasesAt *time.Time
	RetryAt    *time.Time
	Failures   int
}

type Verdict struct {
	NodeID           string
	Shard            string
	Replica          string
	Database         string
	Table            string
	Partition        string
	PartitionID      string
	Status           Status
	Decision         Decision
	Reason           string
	Rows             uint64
	BytesOnDisk      uint64
	ActiveParts      uint64
	Disks            []DiskPart
	TargetDisk       string
	HotVolume        string
	Policy           PolicySnapshot
	Conditions       []Condition
	Hold             *HoldDetail
	Token            string
	ReconciledAt     time.Time
	TableUUID        string
	LayoutGeneration string
	EffectiveMode    Mode
}

type PolicySnapshot struct {
	Mode                   Mode
	AgeBasis               AgeBasis
	OlderThan              string
	Field                  string
	KeepLast               uint64
	QuietFor               string
	TierFrozenAfter        string
	TargetDisk             string
	HotVolume              string
	OptimizeToParts        uint64
	SkipOptimize           bool
	OptimizeOn             OptimizeSide
	OptimizeSkipAboveBytes string
	ResplitStrategy        ResplitStrategy
	ResplitQuietFor        string
	FragmentAbovePartCount uint64
}

func NewCondition(severity ConditionSeverity, code string, message string, nodeID string, database string, table string, partition string, partitionID string) Condition {
	return Condition{
		Severity:    severity,
		Code:        code,
		Message:     message,
		ObservedAt:  time.Now().UTC(),
		NodeID:      nodeID,
		Database:    database,
		Table:       table,
		Partition:   partition,
		PartitionID: partitionID,
	}
}

func DecideTable(obs TableObservation, now time.Time) []Verdict {
	verdicts := make([]Verdict, 0, len(obs.Partitions))
	critical := criticalConditions(obs.Conditions)
	if len(critical) > 0 {
		for _, partition := range obs.Partitions {
			verdicts = append(verdicts, baseVerdict(obs, partition, StatusMisconfigured, DecisionHold, "table is misconfigured", critical))
		}
		if len(obs.Partitions) == 0 {
			verdicts = append(verdicts, baseVerdict(obs, PartitionObservation{}, StatusMisconfigured, DecisionHold, "table is misconfigured", critical))
		}
		return verdicts
	}
	for _, partition := range obs.Partitions {
		verdicts = append(verdicts, DecidePartition(obs, partition, now))
	}
	return verdicts
}

func criticalConditions(conditions []Condition) []Condition {
	out := make([]Condition, 0, len(conditions))
	for _, condition := range conditions {
		if condition.Severity == ConditionSeverityCritical {
			out = append(out, condition)
		}
	}
	return out
}

func DecidePartition(obs TableObservation, partition PartitionObservation, now time.Time) Verdict {
	if isExcluded(obs.Settings.ExcludePartitions, partition.PartitionID, partition.Partition) {
		return baseVerdict(obs, partition, observedStatus(obs.Settings.TargetDisk, partition), DecisionNone, "excluded by config", nil)
	}

	status := observedStatus(obs.Settings.TargetDisk, partition)
	if status == StatusTiered {
		return decideTiered(obs, partition, now)
	}
	if status == StatusSplit {
		return decideSplit(obs, partition, now)
	}

	eligible, ageReason := ageEligible(obs, partition, now)
	if !eligible {
		verdict := baseVerdict(obs, partition, StatusHot, DecisionKeep, ageReason, nil)
		verdict.Hold = keepHoldDetail(obs, partition)
		return verdict
	}
	seal := sealed(obs, partition, obs.Settings.QuietFor.Duration, now)
	if !seal.ok {
		verdict := baseVerdict(obs, partition, StatusHot, DecisionHold, seal.reason, seal.conditions)
		verdict.Hold = seal.detail
		return verdict
	}
	if obs.Settings.OptimizeOn == OptimizeOnHot && shouldOptimize(obs.Settings, partition) {
		return baseVerdict(obs, partition, StatusReady, DecisionOptimize, "partition is cold and sealed; merge hot parts before tiering", nil)
	}
	return baseVerdict(obs, partition, StatusReady, DecisionTier, "partition is cold, sealed, and ready to move to the target disk", nil)
}

// decideTiered handles partitions already wholly on the target disk. The
// one-way ratchet holds for moves — tiered parts never travel hot-ward except
// as leg 1 of a split repair — but tables that optimize on the cold side may
// still merge fragmented partitions in place.
func decideTiered(obs TableObservation, partition PartitionObservation, now time.Time) Verdict {
	if obs.Settings.OptimizeOn != OptimizeOnCold || !shouldOptimize(obs.Settings, partition) {
		return baseVerdict(obs, partition, StatusTiered, DecisionNone, "all active parts are already on the target disk", nil)
	}
	seal := sealed(obs, partition, obs.Settings.QuietFor.Duration, now)
	if !seal.ok {
		verdict := baseVerdict(obs, partition, StatusTiered, DecisionHold, "fragmented cold partition is waiting to merge: "+seal.reason, seal.conditions)
		verdict.Hold = seal.detail
		return verdict
	}
	return baseVerdict(obs, partition, StatusTiered, DecisionOptimize, "merge fragmented parts in place on the cold side", nil)
}

// keepHoldDetail gives keep verdicts a release horizon where one is
// computable: a partitionTime partition becomes age-eligible exactly at
// period end + olderThan. Frontier keeps depend on head movement and carry no
// ETA.
func keepHoldDetail(obs TableObservation, partition PartitionObservation) *HoldDetail {
	if obs.Settings.Age.Basis != AgeBasisPartitionTime {
		return &HoldDetail{Gate: "age"}
	}
	loc, err := partitionLocation(obs.Layout.TimeZone)
	if err != nil {
		return &HoldDetail{Gate: "age"}
	}
	end, err := partitionPeriodEndForValue(obs.Layout, partition.AgeString, loc)
	if err != nil {
		return &HoldDetail{Gate: "age"}
	}
	releases := end.Add(obs.Settings.Age.OlderThan.Duration)
	return &HoldDetail{Gate: "age", Window: obs.Settings.Age.OlderThan.String(), ReleasesAt: &releases}
}

// shouldOptimize reports whether the optimize leg applies: the partition has
// more active parts than the convergence target and the table policy permits
// merging it on local disk.
func shouldOptimize(settings TierSettings, partition PartitionObservation) bool {
	return !settings.SkipOptimize &&
		partition.ActiveParts > settings.OptimizeToParts &&
		partition.BytesOnDisk <= settings.OptimizeSkipAboveBytes.Value
}

func decideSplit(obs TableObservation, partition PartitionObservation, now time.Time) Verdict {
	quietFor := obs.Settings.Resplit.QuietFor.Duration
	if override, ok := obs.ResplitQuiet[partition.PartitionID]; ok && override > quietFor {
		quietFor = override
	}
	seal := sealed(obs, partition, quietFor, now)
	if !seal.ok {
		verdict := baseVerdict(obs, partition, StatusSplit, DecisionHold, "split partition is waiting for resplit quiet window: "+seal.reason, seal.conditions)
		verdict.Hold = seal.detail
		return verdict
	}
	switch obs.Settings.Resplit.Strategy {
	case ResplitStrategyHold:
		return baseVerdict(obs, partition, StatusSplit, DecisionHold, "resplit strategy is hold", nil)
	case ResplitStrategyAppend:
		conditions := []Condition(nil)
		if partition.ActiveParts > obs.Settings.Resplit.FragmentAbovePartCount {
			conditions = append(conditions, NewCondition(ConditionSeverityWarning, "fragmentation_ceiling_exceeded", "append strategy is leaving a fragmented cold partition", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID))
		}
		return baseVerdict(obs, partition, StatusSplit, DecisionAppend, "append hot strays to target disk", conditions)
	case ResplitStrategyRemerge, ResplitStrategyAuto:
		consolidate := obs.Settings.Resplit.Strategy == ResplitStrategyRemerge ||
			partition.ActiveParts > obs.Settings.Resplit.FragmentAbovePartCount
		if !consolidate {
			return baseVerdict(obs, partition, StatusSplit, DecisionAppend, "split partition is under fragmentation ceiling", nil)
		}
		// A consolidation chain whose optimize leg can never run (skipOptimize,
		// oversized partition, or already at the part target) would be a
		// pointless round-trip — append the strays instead and say why.
		if !shouldOptimize(obs.Settings, partition) {
			conditions := []Condition{NewCondition(ConditionSeverityWarning, "consolidation_skipped", "consolidation wants a remerge but the optimize leg is not applicable; appending hot strays instead", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
			return baseVerdict(obs, partition, StatusSplit, DecisionAppend, "consolidation skipped: optimize is not applicable to this partition", conditions)
		}
		// The optimize side decides the co-location direction: cold-side
		// tables push the hot strays to the target disk and merge in place;
		// hot-side tables pull cold parts back for the round-trip.
		if obs.Settings.OptimizeOn == OptimizeOnCold {
			return baseVerdict(obs, partition, StatusSplit, DecisionConsolidate, "move hot strays to the target disk, then merge in place on the cold side", nil)
		}
		return baseVerdict(obs, partition, StatusSplit, DecisionConsolidate, "pull cold parts back to the hot volume for a controlled remerge", nil)
	default:
		return baseVerdict(obs, partition, StatusSplit, DecisionHold, "unknown resplit strategy", nil)
	}
}

func observedStatus(targetDisk string, partition PartitionObservation) Status {
	if allOnDisk(partition.Disks, targetDisk) {
		return StatusTiered
	}
	if hasDisk(partition.Disks, targetDisk) {
		return StatusSplit
	}
	return StatusHot
}

func ageEligible(obs TableObservation, partition PartitionObservation, now time.Time) (bool, string) {
	switch obs.Settings.Age.Basis {
	case AgeBasisFrontier:
		head, ok := obs.Heads[partition.GroupKey]
		if !ok {
			return false, "frontier head is unavailable for this group"
		}
		if obs.Settings.Age.KeepLast > uint64(math.MaxInt64) {
			return false, "frontier keepLast exceeds supported int64 range"
		}
		keepLast := int64(obs.Settings.Age.KeepLast)
		// Hostile or corrupt partition values can push (AgeInteger+1)*divisor
		// past int64, wrapping negative and flipping eligibility. Refuse to
		// tier what we cannot compute — the safe failure direction.
		divisor := obs.Layout.FrontierDivisor
		if divisor <= 0 || partition.AgeInteger < 0 || partition.AgeInteger >= math.MaxInt64/divisor {
			return false, "frontier bucket arithmetic would overflow; refusing to evaluate"
		}
		end := (partition.AgeInteger + 1) * divisor
		if end <= head-keepLast {
			return true, "bucket is below the frontier keepLast window"
		}
		if groupFrozen(obs, partition.GroupKey, now) {
			return true, "group is frozen past tierFrozenAfter"
		}
		return false, "bucket is within the frontier keepLast window"
	case AgeBasisPartitionTime:
		eligible, reason := partitionTimeEligible(obs.Layout, partition.AgeString, obs.Settings.Age.OlderThan.Duration, now)
		if eligible {
			return true, reason
		}
		if groupFrozen(obs, partition.GroupKey, now) {
			return true, "group is frozen past tierFrozenAfter"
		}
		return false, reason
	default:
		return false, "age basis is not configured"
	}
}

func partitionTimeEligible(layout TableLayout, value string, olderThan time.Duration, now time.Time) (bool, string) {
	loc, err := partitionLocation(layout.TimeZone)
	if err != nil {
		return false, err.Error()
	}
	cutoff := now.In(loc).Add(-olderThan)
	end, err := partitionPeriodEndForValue(layout, value, loc)
	if err != nil {
		return false, err.Error()
	}
	if !end.After(cutoff) {
		return true, "partition period ended before olderThan cutoff"
	}
	return false, "partition period overlaps olderThan window"
}

func partitionPeriodEndForValue(layout TableLayout, value string, loc *time.Location) (time.Time, error) {
	switch layout.TimeFunction {
	case "toYYYYMM":
		month, parseErr := parseYYYYMM(value)
		if parseErr != nil {
			return time.Time{}, parseErr
		}
		return time.Date(month/100, time.Month(month%100), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0), nil
	case "toYYYYMMDD":
		day, parseErr := parseYYYYMMDD(value)
		if parseErr != nil {
			return time.Time{}, parseErr
		}
		return time.Date(day/10000, time.Month(day/100%100), day%100, 0, 0, 0, 0, loc).AddDate(0, 0, 1), nil
	case "toDate", "toStartOfMonth", "toStartOfWeek", "toStartOfDay":
		parsed, parseErr := parseDateish(value)
		if parseErr != nil {
			return time.Time{}, parseErr
		}
		start := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), loc)
		return partitionPeriodEnd(layout.TimeFunction, start), nil
	default:
		return time.Time{}, errors.New("time function is not supported")
	}
}

func partitionLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("partition timezone %q is not available: %w", name, err)
	}
	return loc, nil
}

func partitionPeriodEnd(function string, start time.Time) time.Time {
	switch function {
	case "toStartOfMonth":
		return start.AddDate(0, 1, 0)
	case "toStartOfWeek":
		return start.AddDate(0, 0, 7)
	default:
		return start.AddDate(0, 0, 1)
	}
}

// sealed gates admission to a single action leg. Insert evidence (part_log
// NewPart events) is the primary quiet clock: the controller's own moves and
// merges never emit NewPart, so a multi-leg convergence chain (consolidate →
// optimize → tier) is not re-blocked by its own previous legs. The
// modification-time clock — which every leg resets — is consulted only when
// insert evidence cannot gate the window (sealedSignal: modificationTime,
// unreadable part_log, or part_log coverage shorter than the window).
// sealCheck is the structured result of the sealed gate: a verdict reason for
// humans plus a HoldDetail for clients to render gates and countdowns.
type sealCheck struct {
	ok         bool
	reason     string
	detail     *HoldDetail
	conditions []Condition
}

func sealed(obs TableObservation, partition PartitionObservation, quietFor time.Duration, now time.Time) sealCheck {
	window := Duration{Duration: quietFor}.String()
	if ok, reason, conditions := replicaGate(obs, partition); !ok {
		return sealCheck{reason: reason, detail: &HoldDetail{Gate: "replica-health"}, conditions: conditions}
	}
	if ok, reason, conditions := mutationGate(obs, partition); !ok {
		return sealCheck{reason: reason, detail: &HoldDetail{Gate: "mutation"}, conditions: conditions}
	}
	if partition.MergeInFlight {
		// A running merge means the partition's parts are not movable and a
		// further OPTIMIZE is at best a no-op — whether the merge is our own
		// (possibly from a disowned supervised leg) or foreign. Wait it out.
		return sealCheck{
			reason: "a merge is currently running for this partition",
			detail: &HoldDetail{Gate: "merge-in-flight"},
		}
	}
	if obs.Settings.SealedSignal == SealedSignalPartLog && conditionPresent(obs.Conditions, "part_log_unreadable") {
		return sealCheck{
			reason:     "part_log evidence is unavailable",
			detail:     &HoldDetail{Gate: "part-log-required"},
			conditions: []Condition{NewCondition(ConditionSeverityWarning, "part_log_unreadable", "part_log evidence is required by sealedSignal=partLog", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)},
		}
	}
	if obs.Settings.SealedSignal != SealedSignalModificationTime && partition.LatestNewPart != nil && now.Sub(*partition.LatestNewPart) < quietFor {
		releases := partition.LatestNewPart.Add(quietFor)
		return sealCheck{
			reason: "recent NewPart insert evidence",
			detail: &HoldDetail{Gate: "insert-quiet", Window: window, LastInsert: partition.LatestNewPart, ReleasesAt: &releases},
		}
	}
	if insertEvidenceCovers(obs, quietFor, now) {
		if ok, reason, conditions := partitionTimeSuccessorGate(obs, partition, now); !ok {
			return sealCheck{reason: reason, detail: successorHoldDetail(obs, partition, now), conditions: conditions}
		}
		return sealCheck{ok: true, reason: "insert evidence quiet window has elapsed"}
	}
	// Degraded path: the modification-time clock stands in for missing insert
	// evidence. It means "last physical change" — merges, fetches, and the
	// controller's own legs all reset it — so the cause is unattributable and
	// chains pay a fresh quiet window per leg in this mode.
	if !partition.MaxModificationTime.IsZero() && now.Sub(partition.MaxModificationTime) < quietFor {
		lastChange := partition.MaxModificationTime
		releases := lastChange.Add(quietFor)
		return sealCheck{
			reason: "recent physical change, cause unattributable",
			detail: &HoldDetail{Gate: "physical-quiet", Window: window, LastChange: &lastChange, ReleasesAt: &releases},
		}
	}
	if ok, reason, conditions := partitionTimeSuccessorGate(obs, partition, now); !ok {
		return sealCheck{reason: reason, detail: successorHoldDetail(obs, partition, now), conditions: conditions}
	}
	if obs.Settings.SealedSignal != SealedSignalModificationTime {
		return sealCheck{
			ok:         true,
			reason:     "sealed by modification-time fallback",
			conditions: []Condition{NewCondition(ConditionSeverityInfo, "part_log_coverage_shortfall", "part_log coverage is shorter than the gate window; using modification-time fallback", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)},
		}
	}
	return sealCheck{ok: true, reason: "quiet window has elapsed"}
}

// successorHoldDetail explains the successor-activity gate, including when the
// dead-group escape would release the partition regardless: the group's newest
// evidence plus tierFrozenAfter. Fresh inserts to a newer sibling release it
// sooner; this is the worst-case horizon.
func successorHoldDetail(obs TableObservation, partition PartitionObservation, now time.Time) *HoldDetail {
	window := obs.Settings.TierFrozenAfter.Duration
	insertOnly := insertEvidenceCovers(obs, window, now)
	var latest time.Time
	for _, candidate := range obs.Partitions {
		if candidate.GroupKey != partition.GroupKey {
			continue
		}
		if candidate.LatestNewPart != nil && candidate.LatestNewPart.After(latest) {
			latest = *candidate.LatestNewPart
		}
		if !insertOnly && candidate.MaxModificationTime.After(latest) {
			latest = candidate.MaxModificationTime
		}
	}
	detail := &HoldDetail{Gate: "successor-activity", Window: Duration{Duration: window}.String(), LastInsert: partition.LatestNewPart}
	if !latest.IsZero() {
		releases := latest.Add(window)
		detail.ReleasesAt = &releases
	}
	return detail
}

// insertEvidenceCovers reports whether part_log NewPart evidence alone can
// gate the given quiet window: the signal must be configured, readable, and
// the observed part_log retention must span the window.
func insertEvidenceCovers(obs TableObservation, window time.Duration, now time.Time) bool {
	if obs.Settings.SealedSignal == SealedSignalModificationTime {
		return false
	}
	if conditionPresent(obs.Conditions, "part_log_unreadable") {
		return false
	}
	return obs.PartLogMinTime != nil && now.Sub(*obs.PartLogMinTime) >= window
}

func replicaGate(obs TableObservation, partition PartitionObservation) (bool, string, []Condition) {
	if obs.Replica == nil {
		return true, "", nil
	}
	switch {
	case obs.Replica.Readonly:
		return false, "replica is readonly", []Condition{NewCondition(ConditionSeverityWarning, "replica_readonly", "replica is readonly", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	case obs.Replica.SessionExpired:
		return false, "replica session is expired", []Condition{NewCondition(ConditionSeverityWarning, "replica_session_expired", "replica session is expired", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	case obs.Replica.QueueSize > replicaQueueSizeThreshold:
		return false, "replica queue is above the safety threshold", []Condition{NewCondition(ConditionSeverityWarning, "replica_queue_large", "replica queue is above the safety threshold", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	case obs.Replica.AbsoluteDelaySeconds > replicaAbsoluteDelayMaxSecs:
		return false, "replica absolute delay is above the safety threshold", []Condition{NewCondition(ConditionSeverityWarning, "replica_delay_large", "replica absolute delay is above the safety threshold", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	case replicaMergeQueued(partition, obs.Replica.MergeQueue):
		return false, "replica already has a queued merge for this partition", []Condition{NewCondition(ConditionSeverityWarning, "replica_merge_queued", "replica already has a queued merge for this partition", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	default:
		return true, "", nil
	}
}

func mutationGate(obs TableObservation, partition PartitionObservation) (bool, string, []Condition) {
	for _, mutation := range obs.Mutations {
		if mutation.PartsToDo == 0 {
			continue
		}
		msg := "unfinished mutation blocks partition"
		if mutation.LatestFailReason != "" {
			msg = mutation.LatestFailReason
		}
		return false, "unfinished mutation blocks partition", []Condition{NewCondition(ConditionSeverityWarning, "mutation_in_progress", msg, obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	}
	return true, "", nil
}

func replicaMergeQueued(partition PartitionObservation, mergeQueue []string) bool {
	for _, part := range partition.Hashes {
		if slices.Contains(mergeQueue, part.Name) {
			return true
		}
	}
	return false
}

func partitionTimeSuccessorGate(obs TableObservation, partition PartitionObservation, now time.Time) (bool, string, []Condition) {
	if obs.Settings.Age.Basis != AgeBasisPartitionTime {
		return true, "", nil
	}
	if groupFrozen(obs, partition.GroupKey, now) {
		return true, "", nil
	}
	loc, err := partitionLocation(obs.Layout.TimeZone)
	if err != nil {
		return false, err.Error(), []Condition{NewCondition(ConditionSeverityWarning, "partition_time_unreadable", err.Error(), obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	}
	currentEnd, err := partitionPeriodEndForValue(obs.Layout, partition.AgeString, loc)
	if err != nil {
		return false, err.Error(), []Condition{NewCondition(ConditionSeverityWarning, "partition_time_unreadable", err.Error(), obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
	}
	for _, candidate := range obs.Partitions {
		if candidate.PartitionID == partition.PartitionID || candidate.GroupKey != partition.GroupKey || candidate.LatestNewPart == nil {
			continue
		}
		if now.Sub(*candidate.LatestNewPart) > obs.Settings.TierFrozenAfter.Duration {
			continue
		}
		candidateEnd, parseErr := partitionPeriodEndForValue(obs.Layout, candidate.AgeString, loc)
		if parseErr == nil && candidateEnd.After(currentEnd) {
			return true, "", nil
		}
	}
	return false, "waiting for newer partition insert evidence", []Condition{NewCondition(ConditionSeverityInfo, "successor_activity_missing", "no newer partition for this group has recent insert evidence", obs.Node.ID, obs.Database, obs.Table, partition.Partition, partition.PartitionID)}
}

func conditionPresent(conditions []Condition, code string) bool {
	for _, condition := range conditions {
		if condition.Code == code {
			return true
		}
	}
	return false
}

// groupFrozen applies the dead-group escape with the same evidence rule as
// sealed: when insert evidence covers the tierFrozenAfter window, only NewPart
// events count — otherwise the controller's own convergence legs would reset
// the modification-time clock and re-freeze-gate a dead chain on every leg.
func groupFrozen(obs TableObservation, groupKey string, now time.Time) bool {
	window := obs.Settings.TierFrozenAfter.Duration
	insertOnly := insertEvidenceCovers(obs, window, now)
	for _, partition := range obs.Partitions {
		if partition.GroupKey != groupKey {
			continue
		}
		if partition.LatestNewPart != nil && now.Sub(*partition.LatestNewPart) < window {
			return false
		}
		if !insertOnly && !partition.MaxModificationTime.IsZero() && now.Sub(partition.MaxModificationTime) < window {
			return false
		}
	}
	return true
}

func baseVerdict(obs TableObservation, partition PartitionObservation, status Status, decision Decision, reason string, conditions []Condition) Verdict {
	verdict := Verdict{
		NodeID:           obs.Node.ID,
		Shard:            obs.Node.Shard,
		Replica:          obs.Node.Replica,
		Database:         obs.Database,
		Table:            obs.Table,
		Partition:        partition.Partition,
		PartitionID:      partition.PartitionID,
		Status:           status,
		Decision:         decision,
		Reason:           reason,
		Rows:             partition.Rows,
		BytesOnDisk:      partition.BytesOnDisk,
		ActiveParts:      partition.ActiveParts,
		Disks:            append([]DiskPart(nil), partition.Disks...),
		TargetDisk:       obs.Settings.TargetDisk,
		HotVolume:        obs.HotVolume,
		Policy:           snapshotPolicy(obs.Settings, obs.EffectiveMode, obs.HotVolume),
		Conditions:       append([]Condition(nil), conditions...),
		ReconciledAt:     obs.ObservedAt,
		TableUUID:        obs.UUID,
		LayoutGeneration: obs.Layout.Generation,
		EffectiveMode:    obs.EffectiveMode,
	}
	verdict.Token = verdictToken(verdict)
	return verdict
}

func snapshotPolicy(settings TierSettings, mode Mode, hotVolume string) PolicySnapshot {
	return PolicySnapshot{
		Mode:                   mode,
		AgeBasis:               settings.Age.Basis,
		OlderThan:              settings.Age.OlderThan.String(),
		Field:                  settings.Age.Field,
		KeepLast:               settings.Age.KeepLast,
		QuietFor:               settings.QuietFor.String(),
		TierFrozenAfter:        settings.TierFrozenAfter.String(),
		TargetDisk:             settings.TargetDisk,
		HotVolume:              hotVolume,
		OptimizeToParts:        settings.OptimizeToParts,
		SkipOptimize:           settings.SkipOptimize,
		OptimizeOn:             settings.OptimizeOn,
		OptimizeSkipAboveBytes: strconv.FormatUint(settings.OptimizeSkipAboveBytes.Value, 10),
		ResplitStrategy:        settings.Resplit.Strategy,
		ResplitQuietFor:        settings.Resplit.QuietFor.String(),
		FragmentAbovePartCount: settings.Resplit.FragmentAbovePartCount,
	}
}

func verdictToken(verdict Verdict) string {
	buf := fmt.Appendf(nil, "%s/%s/%s/%s/%s/%s/%d/%d/%s",
		verdict.NodeID,
		verdict.Database,
		verdict.Table,
		verdict.PartitionID,
		verdict.Status,
		verdict.Decision,
		verdict.ActiveParts,
		verdict.BytesOnDisk,
		verdict.LayoutGeneration,
	)
	hash := sha256.Sum256(buf)
	return hex.EncodeToString(hash[:12])
}

func isExcluded(exclusions []string, partitionID string, partition string) bool {
	return slices.Contains(exclusions, partitionID) || slices.Contains(exclusions, partition)
}

func parseYYYYMM(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 100001 || value > 999912 || value%100 < 1 || value%100 > 12 {
		return 0, fmt.Errorf("partition month %q is not YYYYMM", raw)
	}
	return value, nil
}

func parseYYYYMMDD(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	day := value % 100
	month := value / 100 % 100
	if err != nil || value < 10000101 || month < 1 || month > 12 || day < 1 || day > 31 {
		return 0, fmt.Errorf("partition day %q is not YYYYMMDD", raw)
	}
	return value, nil
}

func parseDateish(raw string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		parsed, err := time.ParseInLocation(layout, raw, time.UTC)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("partition date %q is not a supported date rendering", raw)
}
