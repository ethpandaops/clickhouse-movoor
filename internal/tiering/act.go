package tiering

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

type Actuator interface {
	Apply(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict) HistoryEntry
}

type Executor struct {
	Log          *slog.Logger
	Store        *Store
	Observer     Observer
	PollInterval time.Duration
	InstanceID   string
	Instrumenter Instrumenter
}

func NewExecutor(log *slog.Logger, store *Store, observer Observer, instanceID string) *Executor {
	if log == nil {
		log = slog.Default()
	}
	return &Executor{
		Log:          log.With(slog.String("component", "tiering.executor")),
		Store:        store,
		Observer:     observer,
		PollInterval: 2 * time.Second,
		InstanceID:   instanceID,
		Instrumenter: noopInstrumenter{},
	}
}

func (e *Executor) Apply(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict) HistoryEntry {
	start := time.Now()
	ctx, span := tracer().Start(ctx, "tiering.action")
	span.SetAttributes(
		attribute.String("node", verdict.NodeID),
		attribute.String("table", verdict.Database+"."+verdict.Table),
		attribute.String("partition_id", verdict.PartitionID),
		attribute.String("action", string(verdict.Decision)),
	)
	defer span.End()
	entry := HistoryEntry{
		Time:        start.UTC(),
		NodeID:      verdict.NodeID,
		Database:    verdict.Database,
		Table:       verdict.Table,
		Partition:   verdict.Partition,
		PartitionID: verdict.PartitionID,
		Action:      verdict.Decision,
		Bytes:       verdict.BytesOnDisk,
		AttemptID:   e.attemptID(verdict),
		Direction:   legDirection(table.Settings, verdict.Decision),
	}

	err := e.apply(ctx, client, table, verdict, entry.AttemptID)
	entry.Duration = time.Since(start)
	if err != nil {
		entry.Outcome = "error"
		entry.Error = err.Error()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		entry.Outcome = "success"
	}
	span.SetAttributes(attribute.String("outcome", entry.Outcome), attribute.Int64("duration_ms", entry.Duration.Milliseconds()))
	if e.Store != nil {
		e.Store.AppendHistory(entry)
	}
	if e.Instrumenter != nil {
		e.Instrumenter.RecordAction(ctx, entry)
	}
	e.Log.InfoContext(ctx, "tiering action finished",
		slog.String("node_id", entry.NodeID),
		slog.String("database", entry.Database),
		slog.String("table", entry.Table),
		slog.String("partition_id", entry.PartitionID),
		slog.String("action", string(entry.Action)),
		slog.String("outcome", entry.Outcome),
		slog.Duration("duration", entry.Duration),
		slog.String("error", entry.Error),
	)
	return entry
}

// legDirection reports which way a leg ships bytes: "up" toward the cold
// target disk, "down" toward the hot volume, empty for in-place or
// non-actionable legs.
func legDirection(settings TierSettings, decision Decision) string {
	switch decision {
	case DecisionTier, DecisionAppend:
		return "up"
	case DecisionConsolidate:
		if settings.OptimizeOn == OptimizeOnCold {
			return "up"
		}
		return "down"
	case DecisionNone, DecisionKeep, DecisionHold, DecisionOptimize:
		return ""
	default:
		return ""
	}
}

// apply executes exactly one convergence leg. Every actionable decision maps
// to a single ClickHouse statement plus its convergence poll; multi-leg flows
// chain through re-classification, never through executor state.
func (e *Executor) apply(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict, attemptID string) error {
	switch verdict.Decision {
	case DecisionConsolidate:
		// Consolidate co-locates parts on the table's optimize side; the side
		// setting picks the direction of this one move.
		if table.Settings.OptimizeOn == OptimizeOnCold {
			return e.moveToCold(ctx, client, table, verdict, attemptID)
		}
		if table.HotVolume == "" {
			return errors.New("hot volume is not known to consolidate toward")
		}
		return e.moveToHot(ctx, client, table, verdict, attemptID)
	case DecisionOptimize:
		return e.optimize(ctx, client, table, verdict, attemptID)
	case DecisionTier, DecisionAppend:
		return e.moveToCold(ctx, client, table, verdict, attemptID)
	case DecisionNone, DecisionKeep, DecisionHold:
		return fmt.Errorf("decision %q is not actionable", verdict.Decision)
	default:
		return fmt.Errorf("decision %q is not actionable", verdict.Decision)
	}
}

func (e *Executor) optimize(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict, attemptID string) error {
	if err := e.checkTableIdentity(ctx, client, table); err != nil {
		return err
	}
	if err := e.guardFreshInserts(ctx, client, table, verdict.PartitionID); err != nil {
		return err
	}
	if err := e.checkOptimizeFreeSpace(ctx, client, table, verdict); err != nil {
		return err
	}
	//nolint:gosec // ClickHouse cannot bind identifiers or partition IDs; all dynamic fragments are quoted.
	query := "OPTIMIZE TABLE " + QuoteQualified(verdict.Database, verdict.Table) +
		" PARTITION ID " + QuoteString(verdict.PartitionID) +
		" FINAL SETTINGS alter_sync = 0, optimize_throw_if_noop = 1, optimize_skip_merged_partitions = 1"
	optimizeCtx := e.statementContext(ctx, attemptID, "optimize", nil)
	if !table.IsReplicated {
		optimizeCtx = e.statementContext(ctx, attemptID, "optimize", clickhouse.Settings{"max_execution_time": 0})
	}
	if _, err := client.DB.ExecContext(optimizeCtx, query); err != nil && !isNoopOptimize(err) {
		return classifyClickHouseError("optimize", err)
	}
	return e.waitForPartCount(ctx, client, table, verdict.PartitionID, table.Settings.OptimizeToParts, table.Settings.OptimizeStallAfter.Duration)
}

// moveToCold ships the partition's parts to the cold target disk (the tier
// and append legs — mechanically identical, they differ only in how many
// parts they accept shipping).
func (e *Executor) moveToCold(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict, attemptID string) error {
	onTarget := func(disk string) bool { return disk == table.Settings.TargetDisk }
	return e.moveLoop(ctx, client, table, verdict, attemptID, "TO DISK "+QuoteString(table.Settings.TargetDisk), onTarget)
}

// moveToHot pulls the partition's parts back onto the hot volume so a later
// optimize leg can merge them on local disk. Converged when no part remains
// on the cold target disk.
func (e *Executor) moveToHot(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict, attemptID string) error {
	onTarget := func(disk string) bool { return disk != table.Settings.TargetDisk }
	return e.moveLoop(ctx, client, table, verdict, attemptID, "TO VOLUME "+QuoteString(table.HotVolume), onTarget)
}

func (e *Executor) moveLoop(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict, attemptID string, targetClause string, onTarget func(string) bool) error {
	for attempt := range 3 {
		attemptErr := e.moveAttempt(ctx, client, table, verdict, attemptID, targetClause, onTarget, attempt)
		if attemptErr == nil {
			return nil
		}
		if errors.Is(attemptErr, errVerifyNotOnTarget) || errors.Is(attemptErr, errVerifyBaselineDrift) {
			if e.Instrumenter != nil {
				e.Instrumenter.RecordRetry(ctx, verdict.NodeID, verdict.Database, verdict.Table, verdict.Decision)
			}
			continue
		}
		return attemptErr
	}
	return errors.New("move did not converge after retries")
}

// moveAttempt runs one statement-plus-convergence cycle of a move leg under
// its own span. A nil return means the leg converged; errVerifyNotOnTarget
// and errVerifyBaselineDrift are retryable, everything else aborts the leg.
func (e *Executor) moveAttempt(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict, attemptID string, targetClause string, onTarget func(string) bool, attempt int) (err error) {
	ctx, span := tracer().Start(ctx, "tiering.action.move_attempt",
		trace.WithAttributes(attribute.Int("tiering.attempt", attempt+1)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	if err = e.checkTableIdentity(ctx, client, table); err != nil {
		return err
	}
	if err = e.guardFreshInserts(ctx, client, table, verdict.PartitionID); err != nil {
		return err
	}
	baseline, baselineErr := e.captureMoveBaseline(ctx, client, table, verdict.PartitionID)
	if baselineErr != nil {
		err = baselineErr
		return err
	}
	if err = e.checkMutationsClear(ctx, client, table); err != nil {
		return err
	}
	//nolint:gosec // ClickHouse cannot bind identifiers or partition IDs; all dynamic fragments are quoted.
	query := "ALTER TABLE " + QuoteQualified(verdict.Database, verdict.Table) +
		" MOVE PARTITION ID " + QuoteString(verdict.PartitionID) +
		" " + targetClause +
		" SETTINGS alter_move_to_space_execute_async = 1"
	if _, moveErr := client.DB.ExecContext(e.statementContext(ctx, attemptID, fmt.Sprintf("move-%d", attempt+1), nil), query); moveErr != nil && !isAlreadyMoved(moveErr) {
		err = classifyClickHouseError("move", moveErr)
		return err
	}
	err = e.waitForVerifiedDisk(ctx, client, table, verdict.PartitionID, onTarget, baseline, table.Settings.OptimizeStallAfter.Duration)
	return err
}

func (e *Executor) captureMoveBaseline(ctx context.Context, client chclient.Client, table TableObservation, partitionID string) ([]PartHash, error) {
	partition, err := e.Observer.RefreshPartition(ctx, client, table, partitionID)
	if err != nil {
		return nil, fmt.Errorf("capture move baseline: %w", err)
	}
	if len(partition.Hashes) == 0 {
		return nil, errVerifyNoParts
	}
	return copyPartHashes(partition.Hashes), nil
}

func (e *Executor) checkTableIdentity(ctx context.Context, client chclient.Client, table TableObservation) error {
	checker, ok := e.Observer.(interface {
		CheckTableIdentity(context.Context, chclient.Client, TableObservation) error
	})
	if !ok {
		return nil
	}
	return checker.CheckTableIdentity(ctx, client, table)
}

func (e *Executor) checkMutationsClear(ctx context.Context, client chclient.Client, table TableObservation) error {
	checker, ok := e.Observer.(interface {
		CheckMutationsClear(context.Context, chclient.Client, TableObservation) error
	})
	if !ok {
		return nil
	}
	return checker.CheckMutationsClear(ctx, client, table)
}

func (e *Executor) checkOptimizeFreeSpace(ctx context.Context, client chclient.Client, table TableObservation, verdict Verdict) error {
	checker, ok := e.Observer.(interface {
		CheckOptimizeFreeSpace(context.Context, chclient.Client, TableObservation, Verdict) error
	})
	if !ok {
		return nil
	}
	return checker.CheckOptimizeFreeSpace(ctx, client, table, verdict)
}

// guardFreshInserts aborts the attempt if the partition received NewPart insert
// events after the decision-time observation — the only live unseal re-check
// during an attempt (design: "fresh NewPart events for the partition abort the
// attempt"). Our own OPTIMIZE/MOVE never emit NewPart, so this is safe to call
// between steps without tripping on the attempt's own work.
func (e *Executor) guardFreshInserts(ctx context.Context, client chclient.Client, table TableObservation, partitionID string) error {
	checker, ok := e.Observer.(interface {
		CountInsertsSince(context.Context, chclient.Client, TableObservation, string, time.Time) (uint64, error)
	})
	if !ok || table.ObservedAt.IsZero() {
		return nil
	}
	count, err := checker.CountInsertsSince(ctx, client, table, partitionID, table.ObservedAt)
	if err != nil {
		return err
	}
	if count > 0 {
		return errVerifyUnsealed
	}
	return nil
}

func (e *Executor) statementContext(ctx context.Context, attemptID string, step string, settings clickhouse.Settings) context.Context {
	if settings == nil {
		settings = clickhouse.Settings{}
	}
	settings["log_comment"] = "movoor:" + e.InstanceID
	queryID := "movoor-" + e.sanitizeQueryID(attemptID+"-"+step)
	return clickhouse.Context(ctx, clickhouse.WithQueryID(queryID), clickhouse.WithSettings(settings))
}

func (e *Executor) sanitizeQueryID(value string) string {
	cleaned := strings.NewReplacer(" ", "_", "/", "_", ".", "_", ":", "_", "'", "_").Replace(value)
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

var (
	errVerifyNotOnTarget   = errors.New("partition still has parts away from the move destination")
	errVerifyBaselineDrift = errors.New("partition part set changed during move")
	errVerifyHashMismatch  = errors.New("partition part hash changed during move")
	errVerifyNoParts       = errors.New("partition has no active parts to verify")
	errVerifyUnsealed      = errors.New("partition received fresh inserts during the attempt")
)

func (e *Executor) waitForPartCount(ctx context.Context, client chclient.Client, table TableObservation, partitionID string, target uint64, timeout time.Duration) (err error) {
	ctx, span := tracer().Start(ctx, "tiering.action.converge",
		trace.WithAttributes(attribute.String("tiering.converge", "part_count")))
	polls := 0
	defer func() {
		span.SetAttributes(attribute.Int("tiering.polls", polls))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	pollCtx := withoutQuerySpans(ctx)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(e.pollInterval())
	defer ticker.Stop()
	for {
		polls++
		partition, refreshErr := e.Observer.RefreshPartition(pollCtx, client, table, partitionID)
		if refreshErr == nil && partition.ActiveParts <= target {
			return nil
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return err
		case <-deadline.C:
			err = errors.New("optimize stalled before reaching target part count")
			return err
		case <-ticker.C:
		}
	}
}

func (e *Executor) waitForVerifiedDisk(ctx context.Context, client chclient.Client, table TableObservation, partitionID string, onTarget func(string) bool, baseline []PartHash, timeout time.Duration) (err error) {
	ctx, span := tracer().Start(ctx, "tiering.action.converge",
		trace.WithAttributes(attribute.String("tiering.converge", "verified_disk")))
	polls := 0
	defer func() {
		span.SetAttributes(attribute.Int("tiering.polls", polls))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	pollCtx := withoutQuerySpans(ctx)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(e.pollInterval())
	defer ticker.Stop()
	var lastErr error
	for {
		polls++
		partition, refreshErr := e.Observer.RefreshPartition(pollCtx, client, table, partitionID)
		switch {
		case refreshErr == nil:
			verifyErr := verifyPartHashes(baseline, partition.Hashes, onTarget)
			if verifyErr == nil {
				return nil
			}
			if errors.Is(verifyErr, errVerifyBaselineDrift) || errors.Is(verifyErr, errVerifyHashMismatch) {
				err = verifyErr
				return err
			}
			lastErr = verifyErr
		case errors.Is(refreshErr, sql.ErrNoRows):
			lastErr = errVerifyNoParts
		default:
			err = refreshErr
			return err
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return err
		case <-deadline.C:
			err = lastErr
			return err
		case <-ticker.C:
		}
	}
}

// verifyPartHashes confirms a move leg: every surviving baseline part must
// keep its hash (data integrity) and satisfy the destination predicate.
// Part-set drift (a merge landed mid-move) re-baselines and retries upstream.
func verifyPartHashes(baseline []PartHash, current []PartHash, onTarget func(string) bool) error {
	if len(baseline) == 0 || len(current) == 0 {
		return errVerifyNoParts
	}
	baseByName := make(map[string]PartHash, len(baseline))
	for _, part := range baseline {
		baseByName[part.Name] = part
	}
	currentByName := make(map[string]PartHash, len(current))
	for _, part := range current {
		currentByName[part.Name] = part
	}
	drift := false
	for name, base := range baseByName {
		now, ok := currentByName[name]
		if !ok {
			drift = true
			continue
		}
		if now.Hash != base.Hash {
			return fmt.Errorf("%w: part %s hash was %s, now %s", errVerifyHashMismatch, name, base.Hash, now.Hash)
		}
		if !onTarget(now.Disk) {
			return errVerifyNotOnTarget
		}
	}
	for name := range currentByName {
		if _, ok := baseByName[name]; !ok {
			drift = true
			break
		}
	}
	if drift {
		return errVerifyBaselineDrift
	}
	return nil
}

func copyPartHashes(parts []PartHash) []PartHash {
	out := make([]PartHash, len(parts))
	copy(out, parts)
	return out
}

func (e *Executor) pollInterval() time.Duration {
	if e.PollInterval > 0 {
		return e.PollInterval
	}
	return 2 * time.Second
}

func (e *Executor) attemptID(verdict Verdict) string {
	base := strings.Join([]string{e.InstanceID, verdict.NodeID, verdict.Database, verdict.Table, verdict.PartitionID, time.Now().UTC().Format("20060102T150405.000000000")}, "-")
	return strings.NewReplacer(" ", "_", "/", "_", ".", "_").Replace(base)
}

func isNoopOptimize(err error) bool {
	var ex *clickhouse.Exception
	if errors.As(err, &ex) {
		return ex.Code == 388 && strings.Contains(strings.ToLower(ex.Message), "already")
	}
	return false
}

func isAlreadyMoved(err error) bool {
	var ex *clickhouse.Exception
	if errors.As(err, &ex) {
		message := strings.ToLower(ex.Message)
		return ex.Code == 479 && (strings.Contains(message, "already on disk") || strings.Contains(message, "already on volume"))
	}
	return false
}

func classifyClickHouseError(action string, err error) error {
	var ex *clickhouse.Exception
	if !errors.As(err, &ex) {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	message := strings.ToLower(ex.Message)
	switch {
	case ex.Code == 479 && strings.Contains(message, "no such disk"):
		return fmt.Errorf("%s misconfigured target disk: %w", action, err)
	case ex.Code == 548:
		return fmt.Errorf("%s misconfigured target volume: %w", action, err)
	case ex.Code == 384:
		return fmt.Errorf("%s deferred because part is locked: %w", action, err)
	case ex.Code == 243:
		return fmt.Errorf("%s failed because there is not enough space: %w", action, err)
	case ex.Code == 236:
		return fmt.Errorf("%s blocked because moves are disabled: %w", action, err)
	case ex.Code == 84:
		return fmt.Errorf("%s failed because destination directory already exists: %w", action, err)
	case ex.Code == 439:
		return fmt.Errorf("%s deferred because ClickHouse cannot schedule the task: %w", action, err)
	case ex.Code == 159:
		return fmt.Errorf("%s failed because max_execution_time killed the statement: %w", action, err)
	default:
		return fmt.Errorf("%s failed with ClickHouse code %d: %w", action, ex.Code, err)
	}
}
