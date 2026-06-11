package tiering

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStorePlanStatusHistoryAndTokens(t *testing.T) {
	store := NewStore(2)
	now := time.Now()
	v1 := Verdict{NodeID: "n2", Database: "db", Table: "b", PartitionID: "p2", Token: "tok2", Decision: DecisionTier}
	v2 := Verdict{NodeID: "n1", Database: "db", Table: "a", PartitionID: "p1", Token: "tok1", Decision: DecisionKeep}
	store.Publish(TablePlan{NodeID: "n2", Database: "db", Table: "b", ReconciledAt: now, Verdicts: []Verdict{v1}})
	store.Publish(TablePlan{NodeID: "n1", Database: "db", Table: "a", ReconciledAt: now, Verdicts: []Verdict{v2}})
	snapshot := store.Snapshot()
	require.Equal(t, "n1", snapshot.Tables[0].NodeID)
	found, ok := store.FindVerdict("n2", "db", "b", "p2")
	require.True(t, ok)
	require.Equal(t, "tok2", found.Token)
	_, ok = store.FindVerdict("missing", "db", "b", "p2")
	require.False(t, ok)
	_, ok = store.FindVerdict("n2", "db", "b", "missing")
	require.False(t, ok)

	store.PublishError("n3", "db", "c", "gen", errors.New("boom"), time.Second)
	require.Equal(t, "boom", store.Snapshot().Tables[2].LastError)

	store.AppendHistory(HistoryEntry{Time: now, PartitionID: "old"})
	store.AppendHistory(HistoryEntry{Time: now.Add(time.Second), PartitionID: "new"})
	store.AppendHistory(HistoryEntry{Time: now.Add(2 * time.Second), PartitionID: "newest"})
	history := store.History()
	require.Len(t, history, 2)
	require.Equal(t, "newest", history[0].PartitionID)

	status := StatusSnapshot{Mode: ModeEnforce, PauseState: PauseRunning, MaxBytesInFlight: 10}
	store.SetStatus(status)
	require.Equal(t, uint64(10), store.Status().MaxBytesInFlight)
	require.Equal(t, PauseReasonOperator, store.Pause(PauseReasonOperator).PauseReason)
	require.Equal(t, PauseRunning, store.Resume().PauseState)

	require.NoError(t, CheckToken(v1, "tok2"))
	require.Error(t, CheckToken(v1, ""))
	require.Error(t, CheckToken(v1, "bad"))
}
