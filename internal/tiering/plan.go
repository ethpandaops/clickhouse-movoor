package tiering

import (
	"cmp"
	"errors"
	"slices"
	"sync"
	"time"
)

type PauseState string

const (
	PauseRunning  PauseState = "running"
	PauseStopping PauseState = "stopping"
	PauseStopped  PauseState = "stopped"
)

type PauseReason string

const (
	PauseReasonNone              PauseReason = ""
	PauseReasonOperator          PauseReason = "operator"
	PauseReasonForeignMover      PauseReason = "foreign-mover"
	PauseReasonDuplicateInstance PauseReason = "duplicate-instance"
	PauseReasonTrainingWheels    PauseReason = "training-wheels"
)

type Store struct {
	mu       sync.RWMutex
	tables   map[TableKey]TablePlan
	history  []HistoryEntry
	capacity int
	status   StatusSnapshot
}

type TableKey struct {
	NodeID   string
	Database string
	Table    string
}

type TablePlan struct {
	NodeID       string
	Database     string
	Table        string
	ReconciledAt time.Time
	TickDuration time.Duration
	Generation   string
	LastError    string
	Verdicts     []Verdict
	Conditions   []Condition
}

type PlanSnapshot struct {
	Tables []TablePlan
}

type StatusSnapshot struct {
	Mode                    Mode
	PauseState              PauseState
	PauseReason             PauseReason
	MaxConcurrentPartitions int
	MaxMovesPerCycle        int
	MaxBytesInFlight        uint64
	BytesInFlight           uint64
	MaxBytesPerDay          uint64
	BytesMovedToday         uint64
	UpdatedAt               time.Time
}

type HistoryEntry struct {
	Time        time.Time
	NodeID      string
	Database    string
	Table       string
	Partition   string
	PartitionID string
	Action      Decision
	Outcome     string
	Duration    time.Duration
	Bytes       uint64
	Error       string
	AttemptID   string
	// Direction records which way a move leg shipped bytes ("up" toward the
	// cold target, "down" toward hot, empty for in-place legs) — the
	// consolidate decision's direction depends on the table's optimizeOn
	// setting, so it cannot be derived from the action name alone.
	Direction string
}

func NewStore(historyCapacity int) *Store {
	if historyCapacity <= 0 {
		historyCapacity = 1000
	}
	return &Store{
		tables:   make(map[TableKey]TablePlan),
		capacity: historyCapacity,
		status: StatusSnapshot{
			PauseState: PauseRunning,
			UpdatedAt:  time.Now().UTC(),
		},
	}
}

func (s *Store) Publish(plan TablePlan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := TableKey{NodeID: plan.NodeID, Database: plan.Database, Table: plan.Table}
	plan.Verdicts = append([]Verdict(nil), plan.Verdicts...)
	plan.Conditions = append([]Condition(nil), plan.Conditions...)
	s.tables[key] = plan
}

func (s *Store) PublishError(nodeID string, database string, table string, generation string, err error, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := TableKey{NodeID: nodeID, Database: database, Table: table}
	previous := s.tables[key]
	previous.NodeID = nodeID
	previous.Database = database
	previous.Table = table
	previous.Generation = generation
	previous.LastError = err.Error()
	previous.TickDuration = duration
	previous.ReconciledAt = time.Now().UTC()
	s.tables[key] = previous
}

func (s *Store) Snapshot() PlanSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tables := make([]TablePlan, 0, len(s.tables))
	for _, table := range s.tables {
		table.Verdicts = append([]Verdict(nil), table.Verdicts...)
		table.Conditions = append([]Condition(nil), table.Conditions...)
		tables = append(tables, table)
	}
	slices.SortFunc(tables, func(a TablePlan, b TablePlan) int {
		return cmp.Or(cmp.Compare(a.NodeID, b.NodeID), cmp.Compare(a.Database, b.Database), cmp.Compare(a.Table, b.Table))
	})
	return PlanSnapshot{Tables: tables}
}

func (s *Store) FindVerdict(nodeID string, database string, table string, partitionID string) (Verdict, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tablePlan, ok := s.tables[TableKey{NodeID: nodeID, Database: database, Table: table}]
	if !ok {
		return Verdict{}, false
	}
	for _, verdict := range tablePlan.Verdicts {
		if verdict.PartitionID == partitionID {
			return verdict, true
		}
	}
	return Verdict{}, false
}

func (s *Store) AppendHistory(entry HistoryEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, entry)
	if len(s.history) > s.capacity {
		copy(s.history, s.history[len(s.history)-s.capacity:])
		s.history = s.history[:s.capacity]
	}
}

func (s *Store) History() []HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]HistoryEntry(nil), s.history...)
	slices.SortFunc(out, func(a HistoryEntry, b HistoryEntry) int {
		return b.Time.Compare(a.Time)
	})
	return out
}

func (s *Store) SetStatus(status StatusSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status.UpdatedAt = time.Now().UTC()
	s.status = status
}

func (s *Store) Status() StatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Store) Pause(reason PauseReason) StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.PauseState = PauseStopped
	s.status.PauseReason = reason
	s.status.UpdatedAt = time.Now().UTC()
	return s.status
}

func (s *Store) Resume() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.PauseState = PauseRunning
	s.status.PauseReason = PauseReasonNone
	s.status.UpdatedAt = time.Now().UTC()
	return s.status
}

// Errors returned by the supervised apply/retry control surface. They are
// sentinel values so the API layer can map each cause to the right HTTP status
// (not-found vs conflict) instead of collapsing every failure into one code.
var (
	ErrStateTokenRequired = errors.New("state token is required")
	ErrStateTokenMismatch = errors.New("state token does not match the current plan")
	ErrNodeNotConfigured  = errors.New("node is not configured")
	ErrWatchNotConfigured = errors.New("tiering watch is not configured")
	ErrPartitionNotFound  = errors.New("partition was not found in the current plan")
	ErrNotActionable      = errors.New("partition decision is not actionable")
	// ErrTieringPaused gates supervised applies too: an operator pause must
	// stop ALL writes — autonomous and human-triggered alike — or "pause
	// during backups" is a lie.
	ErrTieringPaused = errors.New("tiering is paused")
	// ErrLegInFlight rejects a supervised apply for a partition that already
	// has a leg executing (autonomous or another operator's click).
	ErrLegInFlight  = errors.New("a tiering leg is already in flight for this partition")
	ErrActionFailed = errors.New("tiering action failed")
)

func CheckToken(verdict Verdict, token string) error {
	if token == "" {
		return ErrStateTokenRequired
	}
	if verdict.Token != token {
		return ErrStateTokenMismatch
	}
	return nil
}
