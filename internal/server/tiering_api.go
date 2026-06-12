package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

func (s *server) handleTieringPlan(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTieringStore(w, r)
	if !ok {
		return
	}
	if !validateClosedQuery(w, r, "status", map[string]struct{}{
		"hot": {}, "ready": {}, "tiered": {}, "split": {}, "stalled": {}, "misconfigured": {},
	}) {
		return
	}
	if !validateClosedQuery(w, r, "decision", map[string]struct{}{
		"none": {}, "keep": {}, "hold": {}, "consolidate": {}, "optimize": {}, "tier": {}, "append": {},
	}) {
		return
	}
	filters := newTieringPlanFilters(r)
	snapshot := store.Snapshot()
	// Slices start non-nil: the contract requires arrays, and nil marshals as
	// null — which the generated client's validation rejects outright. Empty
	// plans (fresh boot, zero watches) and filtered-empty plans are routine.
	response := tieringPlanResponse{
		Tables: []tieringTablePlanResponse{},
		Items:  []tieringPartitionResponse{},
	}
	for _, table := range snapshot.Tables {
		if !filters.matchesTable(table) {
			continue
		}
		tableResponse := tieringTablePlanResponse{
			NodeID:         table.NodeID,
			Database:       table.Database,
			Table:          table.Table,
			ReconciledAt:   table.ReconciledAt,
			TickDurationMs: int(table.TickDuration.Milliseconds()),
			Generation:     table.Generation,
			LastError:      nilIfEmpty(table.LastError),
			Partitions:     len(table.Verdicts),
			Conditions:     apiTieringConditions(table.Conditions),
		}
		for _, verdict := range table.Verdicts {
			if !filters.matchesVerdict(verdict) {
				continue
			}
			if tieringDecisionIsActionable(verdict.Decision) {
				tableResponse.Actionable++
			}
			response.Items = append(response.Items, apiTieringPartition(verdict))
		}
		response.Tables = append(response.Tables, tableResponse)
	}
	s.writeJSON(w, r, response)
}

type tieringPlanFilters struct {
	nodeID   string
	database string
	table    string
	status   string
	decision string
}

func newTieringPlanFilters(r *http.Request) tieringPlanFilters {
	query := r.URL.Query()
	return tieringPlanFilters{
		nodeID:   query.Get("nodeId"),
		database: query.Get("database"),
		table:    query.Get("table"),
		status:   query.Get("status"),
		decision: query.Get("decision"),
	}
}

func (f tieringPlanFilters) matchesTable(table tiering.TablePlan) bool {
	return (f.nodeID == "" || table.NodeID == f.nodeID) &&
		(f.database == "" || table.Database == f.database) &&
		(f.table == "" || table.Table == f.table)
}

func (f tieringPlanFilters) matchesVerdict(verdict tiering.Verdict) bool {
	return (f.status == "" || string(verdict.Status) == f.status) &&
		(f.decision == "" || string(verdict.Decision) == f.decision)
}

func (s *server) handleTieringStatus(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTieringStore(w, r)
	if !ok {
		return
	}
	var legs []tiering.InFlightLeg
	if s.tiering != nil {
		legs = s.tiering.InFlight()
	}
	s.writeJSON(w, r, apiTieringStatus(store.Status(), legs))
}

func (s *server) handleTieringHistory(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTieringStore(w, r)
	if !ok {
		return
	}
	filters := newTieringHistoryFilters(r)
	entries := store.History()
	response := tieringHistoryResponse{Items: make([]tieringHistoryEntryResponse, 0, len(entries))}
	for _, entry := range entries {
		if !filters.matches(entry) {
			continue
		}
		response.Items = append(response.Items, apiTieringHistory(entry))
	}
	s.writeJSON(w, r, response)
}

type tieringHistoryFilters struct {
	nodeID      string
	database    string
	table       string
	partitionID string
}

func newTieringHistoryFilters(r *http.Request) tieringHistoryFilters {
	query := r.URL.Query()
	return tieringHistoryFilters{
		nodeID:      query.Get("nodeId"),
		database:    query.Get("database"),
		table:       query.Get("table"),
		partitionID: query.Get("partitionId"),
	}
}

func (f tieringHistoryFilters) matches(entry tiering.HistoryEntry) bool {
	return (f.nodeID == "" || entry.NodeID == f.nodeID) &&
		(f.database == "" || entry.Database == f.database) &&
		(f.table == "" || entry.Table == f.table) &&
		(f.partitionID == "" || entry.PartitionID == f.partitionID)
}

func (s *server) handleTieringPause(w http.ResponseWriter, r *http.Request) {
	controller, ok := s.requireTieringController(w, r)
	if !ok {
		return
	}
	s.writeJSONStatus(w, r, http.StatusAccepted, apiTieringStatus(controller.Pause(tiering.PauseReasonOperator), controller.InFlight()))
}

func (s *server) handleTieringResume(w http.ResponseWriter, r *http.Request) {
	controller, ok := s.requireTieringController(w, r)
	if !ok {
		return
	}
	s.writeJSONStatus(w, r, http.StatusAccepted, apiTieringStatus(controller.Resume(), controller.InFlight()))
}

func (s *server) handleTieringApply(w http.ResponseWriter, r *http.Request) {
	s.handleTieringAction(w, r, false)
}

func (s *server) handleTieringRetry(w http.ResponseWriter, r *http.Request) {
	s.handleTieringAction(w, r, true)
}

func (s *server) handleTieringAction(w http.ResponseWriter, r *http.Request, retry bool) {
	controller, ok := s.requireTieringController(w, r)
	if !ok {
		return
	}
	nodeID := r.URL.Query().Get("nodeId")
	if nodeID == "" {
		writeBadParameter(w, r, "nodeId", "nodeId is required")
		return
	}
	var body tieringApplyRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeBadParameter(w, r, "body", "request body must be JSON with stateToken")
			return
		}
	}
	if body.StateToken == "" {
		writeBadParameter(w, r, "stateToken", "stateToken is required")
		return
	}

	var (
		entry tiering.HistoryEntry
		err   error
	)
	if retry {
		entry, err = controller.Retry(r.Context(), nodeID, r.PathValue("database"), r.PathValue("table"), r.PathValue("partitionId"), body.StateToken)
	} else {
		entry, err = controller.Apply(r.Context(), nodeID, r.PathValue("database"), r.PathValue("table"), r.PathValue("partitionId"), body.StateToken)
	}
	// "started" is the detached-leg acknowledgement: the action was admitted
	// and runs in the background; completion lands in history and the leg is
	// visible via the status endpoint's in-flight list until it converges.
	if err == nil && entry.Outcome != "" && entry.Outcome != "success" && entry.Outcome != "started" {
		err = tieringEntryError(entry)
	}
	if err != nil {
		status := tieringActionStatus(err)
		s.writeProblem(w, r, problemDetails{
			Type:     "about:blank",
			Title:    http.StatusText(status),
			Status:   status,
			Detail:   err.Error(),
			Instance: r.URL.RequestURI(),
		})
		return
	}

	s.writeJSONStatus(w, r, http.StatusAccepted, tieringApplyResponse{Item: apiTieringHistory(entry)})
}

func tieringEntryError(entry tiering.HistoryEntry) error {
	if entry.Error != "" {
		return fmt.Errorf("%w: %s", tiering.ErrActionFailed, entry.Error)
	}
	if entry.Outcome != "" {
		return fmt.Errorf("%w: outcome %q", tiering.ErrActionFailed, entry.Outcome)
	}
	return tiering.ErrActionFailed
}

func (s *server) requireTieringStore(w http.ResponseWriter, r *http.Request) (*tiering.Store, bool) {
	if s.tieringStore != nil {
		return s.tieringStore, true
	}
	s.writeProblem(w, r, problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusServiceUnavailable),
		Status:   http.StatusServiceUnavailable,
		Detail:   "tiering controller is not configured",
		Instance: r.URL.RequestURI(),
	})
	return nil, false
}

func (s *server) requireTieringController(w http.ResponseWriter, r *http.Request) (TieringController, bool) {
	if s.tiering != nil {
		return s.tiering, true
	}
	s.writeProblem(w, r, problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusServiceUnavailable),
		Status:   http.StatusServiceUnavailable,
		Detail:   "tiering controller is not configured",
		Instance: r.URL.RequestURI(),
	})
	return nil, false
}

func apiTieringPartition(verdict tiering.Verdict) tieringPartitionResponse {
	disks := make([]tieringDiskPartResponse, 0, len(verdict.Disks))
	for _, disk := range verdict.Disks {
		disks = append(disks, tieringDiskPartResponse{
			Disk:  disk.Disk,
			Parts: uint64String(disk.Parts),
		})
	}
	return tieringPartitionResponse{
		NodeID:        verdict.NodeID,
		Shard:         verdict.Shard,
		Replica:       verdict.Replica,
		Database:      verdict.Database,
		Table:         verdict.Table,
		Partition:     verdict.Partition,
		PartitionID:   verdict.PartitionID,
		Status:        string(verdict.Status),
		Decision:      string(verdict.Decision),
		Reason:        verdict.Reason,
		Rows:          uint64String(verdict.Rows),
		BytesOnDisk:   uint64String(verdict.BytesOnDisk),
		ActiveParts:   uint64String(verdict.ActiveParts),
		Disks:         disks,
		TargetDisk:    verdict.TargetDisk,
		HotVolume:     verdict.HotVolume,
		Policy:        apiTieringPolicy(verdict.Policy),
		Conditions:    apiTieringConditions(verdict.Conditions),
		Hold:          apiTieringHold(verdict.Hold),
		StateToken:    verdict.Token,
		ReconciledAt:  verdict.ReconciledAt,
		EffectiveMode: string(verdict.EffectiveMode),
	}
}

func apiTieringHold(detail *tiering.HoldDetail) *tieringHoldDetailResponse {
	if detail == nil {
		return nil
	}
	return &tieringHoldDetailResponse{
		Gate:         detail.Gate,
		Window:       detail.Window,
		LastInsertAt: detail.LastInsert,
		LastChangeAt: detail.LastChange,
		ReleasesAt:   detail.ReleasesAt,
		RetryAt:      detail.RetryAt,
		Failures:     detail.Failures,
	}
}

func apiTieringPolicy(policy tiering.PolicySnapshot) tieringPolicyResponse {
	return tieringPolicyResponse{
		Mode:                   string(policy.Mode),
		AgeBasis:               string(policy.AgeBasis),
		OlderThan:              policy.OlderThan,
		Field:                  policy.Field,
		KeepLast:               uint64String(policy.KeepLast),
		QuietFor:               policy.QuietFor,
		TierFrozenAfter:        policy.TierFrozenAfter,
		TargetDisk:             policy.TargetDisk,
		HotVolume:              policy.HotVolume,
		OptimizeToParts:        uint64String(policy.OptimizeToParts),
		SkipOptimize:           policy.SkipOptimize,
		OptimizeOn:             string(policy.OptimizeOn),
		OptimizeSkipAboveBytes: policy.OptimizeSkipAboveBytes,
		ResplitStrategy:        string(policy.ResplitStrategy),
		ResplitQuietFor:        policy.ResplitQuietFor,
		FragmentAbovePartCount: uint64String(policy.FragmentAbovePartCount),
	}
}

func apiTieringConditions(conditions []tiering.Condition) []tieringConditionResponse {
	items := make([]tieringConditionResponse, 0, len(conditions))
	for _, condition := range conditions {
		items = append(items, tieringConditionResponse{
			Severity:    string(condition.Severity),
			Code:        condition.Code,
			Message:     condition.Message,
			ObservedAt:  condition.ObservedAt,
			NodeID:      condition.NodeID,
			Database:    condition.Database,
			Table:       condition.Table,
			Partition:   condition.Partition,
			PartitionID: condition.PartitionID,
		})
	}
	return items
}

func apiTieringStatus(status tiering.StatusSnapshot, legs []tiering.InFlightLeg) tieringStatusResponse {
	inFlight := make([]tieringInFlightLegResponse, 0, len(legs))
	for _, leg := range legs {
		inFlight = append(inFlight, tieringInFlightLegResponse{
			NodeID:      leg.NodeID,
			Database:    leg.Database,
			Table:       leg.Table,
			Partition:   leg.Partition,
			PartitionID: leg.PartitionID,
			Action:      string(leg.Action),
			Bytes:       uint64String(leg.Bytes),
			StartedAt:   leg.StartedAt,
			Source:      leg.Source,
		})
	}
	return tieringStatusResponse{
		InFlight:                inFlight,
		Mode:                    string(status.Mode),
		PauseState:              string(status.PauseState),
		PauseReason:             string(status.PauseReason),
		MaxConcurrentPartitions: status.MaxConcurrentPartitions,
		MaxMovesPerCycle:        status.MaxMovesPerCycle,
		MaxBytesInFlight:        uint64String(status.MaxBytesInFlight),
		BytesInFlight:           uint64String(status.BytesInFlight),
		MaxBytesPerDay:          uint64String(status.MaxBytesPerDay),
		BytesMovedToday:         uint64String(status.BytesMovedToday),
		UpdatedAt:               status.UpdatedAt,
	}
}

func apiTieringHistory(entry tiering.HistoryEntry) tieringHistoryEntryResponse {
	return tieringHistoryEntryResponse{
		Time:        entry.Time,
		NodeID:      entry.NodeID,
		Database:    entry.Database,
		Table:       entry.Table,
		Partition:   entry.Partition,
		PartitionID: entry.PartitionID,
		Action:      string(entry.Action),
		Outcome:     entry.Outcome,
		DurationMs:  int(entry.Duration.Milliseconds()),
		Bytes:       uint64String(entry.Bytes),
		Error:       entry.Error,
		AttemptID:   entry.AttemptID,
	}
}

// tieringDecisionIsActionable delegates to the tiering package — the decision
// taxonomy has a single owner; a copy here would drift when legs change.
func tieringDecisionIsActionable(decision tiering.Decision) bool {
	return tiering.IsActionableDecision(decision)
}

// tieringActionStatus maps a supervised apply/retry error to an HTTP status:
// missing resources are 404, state-token mismatch / not-actionable / executor
// failures are 409 (the action cannot apply to the current plan).
func tieringActionStatus(err error) int {
	switch {
	case errors.Is(err, tiering.ErrNodeNotConfigured),
		errors.Is(err, tiering.ErrWatchNotConfigured),
		errors.Is(err, tiering.ErrPartitionNotFound):
		return http.StatusNotFound
	default:
		return http.StatusConflict
	}
}
