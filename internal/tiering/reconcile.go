package tiering

import (
	"context"
	"hash/fnv"
	"log/slog"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

const (
	minObserveFailureBackoff = time.Second
	maxObserveFailureBackoff = time.Minute
	repeatedFailureLogEvery  = 5 * time.Minute
	backoffJitterPermille    = 200
)

type failureLogState struct {
	message    string
	lastLogged time.Time
	suppressed uint64
}

type reconcileBackoff struct {
	key      string
	normal   time.Duration
	initial  time.Duration
	maxDelay time.Duration
	failures int
}

func (c *controller) reconcileLoop(ctx context.Context, client chclient.Client, watch EffectiveWatch) {
	interval := c.cfg.Tiering.Interval.Duration
	if interval <= 0 {
		interval = DefaultInterval
	}
	if c.usesEnforceMode(watch) {
		offset := replicaPhaseOffset(client.Node, interval)
		if offset > 0 {
			// Publish-only warm-up: the phase offset staggers WRITES so
			// replicas of a shard never act simultaneously, but it also left
			// this node×table invisible in the plan for up to a full interval
			// after startup. Observe and publish immediately; dispatch still
			// waits out the offset.
			c.republishTable(ctx, client, watch)
			select {
			case <-ctx.Done():
				return
			case <-time.After(offset):
			}
		}
	}
	kick := c.registerKick(client.Node.ID, watch.Database, watch.Table)
	backoff := newReconcileBackoff(tableLogKey(client.Node.ID, watch.Database, watch.Table), interval)
	for {
		success := c.reconcileOnce(ctx, client, watch)
		if !waitForNextReconcile(ctx, backoff.next(success), kick) {
			return
		}
	}
}

func waitForNextReconcile(ctx context.Context, delay time.Duration, kick <-chan struct{}) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	case <-kick:
		// A leg just converged for this node×table: re-observe now so the
		// next leg of the chain dispatches without waiting a full tick.
		// Going back through reconcileOnce keeps every safety rail (pause,
		// breaker, budgets, single-flight, foreign-mover guard) in the
		// path, and routing the kick through this loop bounds chaining to
		// one in-flight reconcile per table — a misbehaving executor can
		// never recurse the controller into a tight loop.
		return true
	}
}

func newReconcileBackoff(key string, interval time.Duration) reconcileBackoff {
	if interval <= 0 {
		interval = DefaultInterval
	}
	initial := interval
	initial = max(initial, minObserveFailureBackoff)
	maxDelay := maxObserveFailureBackoff
	maxDelay = max(maxDelay, interval)
	return reconcileBackoff{key: key, normal: interval, initial: initial, maxDelay: maxDelay}
}

func (b *reconcileBackoff) next(success bool) time.Duration {
	if success {
		b.failures = 0
		return b.normal
	}
	b.failures++
	delay := b.initial
	for i := 1; i < b.failures; i++ {
		if delay >= b.maxDelay/2 {
			delay = b.maxDelay
			break
		}
		delay *= 2
	}
	delay = jitterBackoff(delay, b.key, b.failures)
	if delay > b.maxDelay {
		return b.maxDelay
	}
	return delay
}

func jitterBackoff(delay time.Duration, key string, failures int) time.Duration {
	if delay <= 0 {
		return delay
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(failures)))
	offset := int64(h.Sum64()%uint64(backoffJitterPermille*2+1)) - backoffJitterPermille
	return time.Duration(int64(delay) * (1000 + offset) / 1000)
}

// registerKick creates (or returns) the fast-follow channel for one
// node×table reconcile loop. The buffer of one coalesces bursts: multiple leg
// completions before the loop wakes still produce a single early reconcile.
func (c *controller) registerKick(nodeID string, database string, table string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kicks == nil {
		c.kicks = make(map[string]chan struct{})
	}
	key := tableLogKey(nodeID, database, table)
	if existing, ok := c.kicks[key]; ok {
		return existing
	}
	kick := make(chan struct{}, 1)
	c.kicks[key] = kick
	return kick
}

// kickReconcile nudges a table's reconcile loop to run early, without
// blocking and without requiring the loop to exist (tests and supervised
// applies in off/plan deployments simply drop the signal).
func (c *controller) kickReconcile(nodeID string, database string, table string) {
	c.mu.Lock()
	kick := c.kicks[tableLogKey(nodeID, database, table)]
	c.mu.Unlock()
	if kick == nil {
		return
	}
	select {
	case kick <- struct{}{}:
	default:
	}
}

func (c *controller) reconcileOnce(ctx context.Context, client chclient.Client, watch EffectiveWatch) bool {
	start := time.Now()
	ctx, span := otel.Tracer("github.com/ethpandaops/clickhouse-movoor/internal/tiering").Start(ctx, "tiering.reconcile")
	span.SetAttributes(
		attribute.String("node", client.Node.ID),
		attribute.String("table", watch.Database+"."+watch.Table),
	)
	defer span.End()
	obs, err := c.observeTable(ctx, client, watch)
	if err != nil {
		if isContextCanceled(ctx, err) {
			return true
		}
		duration := time.Since(start)
		c.store.PublishError(client.Node.ID, watch.Database, watch.Table, "", err, duration)
		c.recordReconcile(ctx, client.Node.ID, watch.Database, watch.Table, "error", duration)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.warnRepeatedFailure("observe:"+tableLogKey(client.Node.ID, watch.Database, watch.Table), err.Error(), func(attrs []any) {
			attrs = append(attrs, slog.String("node_id", client.Node.ID), slog.String("database", watch.Database), slog.String("table", watch.Table), slog.Any("error", err))
			c.log.WarnContext(ctx, "tiering observe failed", attrs...)
		})
		return false
	}
	c.clearRepeatedFailure("observe:" + tableLogKey(client.Node.ID, watch.Database, watch.Table))
	c.applyAdaptiveResplitQuiet(&obs)
	verdicts := c.overlayStalled(DecideTable(obs, decisionTime(obs)), time.Now().UTC())
	probeConditions := c.maybeProbeColdPartition(ctx, client, obs)
	if len(probeConditions) > 0 {
		obs.Conditions = append(obs.Conditions, probeConditions...)
	}
	c.recordColdSideMerges(ctx, client, obs)
	plan := TablePlan{
		NodeID:       client.Node.ID,
		Database:     watch.Database,
		Table:        watch.Table,
		ReconciledAt: obs.ObservedAt,
		TickDuration: time.Since(start),
		Generation:   obs.Layout.Generation,
		Verdicts:     verdicts,
		Conditions:   obs.Conditions,
	}
	c.store.Publish(plan)
	c.recordReconcile(ctx, client.Node.ID, watch.Database, watch.Table, "success", plan.TickDuration)
	span.SetAttributes(
		attribute.Int("tiering.partitions", len(verdicts)),
		attribute.Int64("tiering.duration_ms", plan.TickDuration.Milliseconds()),
	)
	c.dispatch(ctx, client, obs, verdicts)
	return true
}

func tableLogKey(nodeID string, database string, table string) string {
	return nodeID + "/" + database + "/" + table
}

func (c *controller) warnRepeatedFailure(key string, message string, log func([]any)) {
	if log == nil {
		return
	}
	shouldLog, suppressed := c.updateFailureLog(key, message, time.Now())
	if !shouldLog {
		return
	}
	attrs := make([]any, 0, 1)
	if suppressed > 0 {
		attrs = append(attrs, slog.Uint64("suppressed_repeats", suppressed))
	}
	log(attrs)
}

func (c *controller) updateFailureLog(key string, message string, now time.Time) (bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failureLogs == nil {
		c.failureLogs = make(map[string]failureLogState)
	}
	state, ok := c.failureLogs[key]
	if !ok || state.message != message {
		c.failureLogs[key] = failureLogState{message: message, lastLogged: now}
		return true, 0
	}
	if now.Sub(state.lastLogged) >= repeatedFailureLogEvery {
		suppressed := state.suppressed
		state.lastLogged = now
		state.suppressed = 0
		c.failureLogs[key] = state
		return true, suppressed
	}
	state.suppressed++
	c.failureLogs[key] = state
	return false, 0
}

func (c *controller) clearRepeatedFailure(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failureLogs != nil {
		delete(c.failureLogs, key)
	}
}

// republishTable re-observes one node×table and publishes a fresh plan to the
// store, best-effort. It is the post-action refresh for supervised apply/retry
// (observe → classify → overlay stalled → publish) and deliberately skips
// dispatch, the read probe, side-merge counting, and reconcile metrics — those
// belong to the periodic reconcile tick, not a manual action.
func (c *controller) republishTable(ctx context.Context, client chclient.Client, watch EffectiveWatch) {
	start := time.Now()
	obs, err := c.observeTable(ctx, client, watch)
	if err != nil {
		c.store.PublishError(client.Node.ID, watch.Database, watch.Table, "", err, time.Since(start))
		return
	}
	c.applyAdaptiveResplitQuiet(&obs)
	verdicts := c.overlayStalled(DecideTable(obs, decisionTime(obs)), time.Now().UTC())
	c.store.Publish(TablePlan{
		NodeID:       client.Node.ID,
		Database:     watch.Database,
		Table:        watch.Table,
		ReconciledAt: obs.ObservedAt,
		TickDuration: time.Since(start),
		Generation:   obs.Layout.Generation,
		Verdicts:     verdicts,
		Conditions:   obs.Conditions,
	})
}

func (c *controller) recordReconcile(ctx context.Context, nodeID string, database string, table string, result string, duration time.Duration) {
	if c.instrumenter == nil {
		return
	}
	c.instrumenter.RecordReconcile(ctx, nodeID, database, table, result, duration)
}

func decisionTime(obs TableObservation) time.Time {
	if obs.ObservedAt.IsZero() {
		return time.Now().UTC()
	}
	return obs.ObservedAt
}

func (c *controller) usesEnforceMode(watch EffectiveWatch) bool {
	if watch.Settings != nil && watch.Settings.Mode != "" {
		return watch.Settings.Mode == ModeEnforce
	}
	return c.cfg.Tiering.Mode == ModeEnforce
}

func replicaPhaseOffset(node chclient.Node, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(node.Shard + "/" + node.Replica + "/" + node.ID))
	offset := h.Sum64() % uint64(interval)
	//nolint:gosec // interval is a positive time.Duration, so offset is bounded by MaxInt64.
	return time.Duration(offset)
}
