package tiering

import (
	"cmp"
	"slices"
)

// orderOldestFirst sorts actionable verdicts so the controller converges the
// oldest partitions first within a table (design: "oldest-first within a
// table"). Rank derives from the partition's age — frontier bucket index, or
// the partitionTime period end — falling back to partition_id for ties and for
// partitions whose age could not be parsed.
func orderOldestFirst(table TableObservation, verdicts []Verdict) {
	rank := make(map[string]int64, len(table.Partitions))
	for _, partition := range table.Partitions {
		rank[partition.PartitionID] = agePartitionRank(table.Layout, partition)
	}
	slices.SortStableFunc(verdicts, func(a Verdict, b Verdict) int {
		return cmp.Or(
			cmp.Compare(rank[a.PartitionID], rank[b.PartitionID]),
			cmp.Compare(a.PartitionID, b.PartitionID),
		)
	})
}

func agePartitionRank(layout TableLayout, partition PartitionObservation) int64 {
	if layout.Basis == AgeBasisFrontier {
		return partition.AgeInteger
	}
	loc, err := partitionLocation(layout.TimeZone)
	if err != nil {
		return 0
	}
	end, err := partitionPeriodEndForValue(layout, partition.AgeString, loc)
	if err != nil {
		return 0
	}
	return end.Unix()
}

func actionableVerdicts(verdicts []Verdict) []Verdict {
	out := make([]Verdict, 0, len(verdicts))
	for _, verdict := range verdicts {
		if isActionable(verdict.Decision) {
			out = append(out, verdict)
		}
	}
	return out
}

// IsActionableDecision reports whether a decision dispatches a write leg. It
// is the single owner of the actionable set — the API layer delegates here.
func IsActionableDecision(decision Decision) bool {
	return decision == DecisionConsolidate || decision == DecisionOptimize ||
		decision == DecisionTier || decision == DecisionAppend
}

func isActionable(decision Decision) bool {
	return IsActionableDecision(decision)
}
