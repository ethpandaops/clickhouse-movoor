package tiering

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

type Controller interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Store() *Store
	Apply(ctx context.Context, nodeID string, database string, table string, partitionID string, stateToken string) (HistoryEntry, error)
	Retry(ctx context.Context, nodeID string, database string, table string, partitionID string, stateToken string) (HistoryEntry, error)
	Pause(reason PauseReason) StatusSnapshot
	Resume() StatusSnapshot
	InFlight() []InFlightLeg
}

// InFlightLeg is one currently-executing convergence leg — movoor's
// intent-level "active now", which spans the leg's whole dispatch→converged
// window rather than the milliseconds the physical operation is visible in
// system.moves/system.merges.
type InFlightLeg struct {
	NodeID      string
	Database    string
	Table       string
	Partition   string
	PartitionID string
	Action      Decision
	Bytes       uint64
	StartedAt   time.Time
	// Source is "dispatch" for autonomous legs, "supervised" for applies.
	Source string
}

type ControllerConfig struct {
	Tiering      Config
	Watches      []EffectiveWatch
	QueryTimeout time.Duration
	InstanceID   string
	Instrumenter Instrumenter
}

type controller struct {
	log              *slog.Logger
	cfg              ControllerConfig
	clients          []chclient.Client
	observer         Observer
	executor         Actuator
	instrumenter     Instrumenter
	store            *Store
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	mu               sync.Mutex
	inFlight         map[string]InFlightLeg
	bytesInFlight    uint64
	bytesMovedToday  map[string]uint64
	budgetDay        map[string]string
	bootTimes        map[string]time.Time
	foreignClean     map[string]int
	foreignGuardSeen map[string]time.Time
	probeLast        map[string]time.Time
	stalled          map[string]stalledPartition
	resplitFlaps     map[string]int
	sideMergeLast    map[string]time.Time
	kicks            map[string]chan struct{}
	failureLogs      map[string]failureLogState
}

func New(log *slog.Logger, clients []chclient.Client, cfg ControllerConfig) Controller {
	if log == nil {
		log = slog.Default()
	}
	store := NewStore(2000)
	observer := NewSQLObserver(cfg.QueryTimeout)
	instanceID := cfg.InstanceID
	if instanceID == "" {
		instanceID = "default"
	}
	instrumenter := cfg.Instrumenter
	if instrumenter == nil {
		instrumenter = noopInstrumenter{}
	}
	c := &controller{
		log:              log.With(slog.String("component", "tiering")),
		cfg:              cfg,
		clients:          append([]chclient.Client(nil), clients...),
		observer:         observer,
		instrumenter:     instrumenter,
		store:            store,
		inFlight:         make(map[string]InFlightLeg),
		bytesMovedToday:  make(map[string]uint64),
		budgetDay:        make(map[string]string),
		bootTimes:        make(map[string]time.Time),
		foreignClean:     make(map[string]int),
		foreignGuardSeen: make(map[string]time.Time),
		probeLast:        make(map[string]time.Time),
		stalled:          make(map[string]stalledPartition),
		resplitFlaps:     make(map[string]int),
		sideMergeLast:    make(map[string]time.Time),
		kicks:            make(map[string]chan struct{}),
		failureLogs:      make(map[string]failureLogState),
	}
	executor := NewExecutor(log, store, observer, instanceID)
	executor.Instrumenter = instrumenter
	c.executor = executor
	store.SetStatus(StatusSnapshot{
		Mode:                    cfg.Tiering.Mode,
		PauseState:              PauseRunning,
		MaxConcurrentPartitions: cfg.Tiering.MaxConcurrentPartitions,
		MaxMovesPerCycle:        cfg.Tiering.Safety.MaxMovesPerCycle,
		MaxBytesInFlight:        cfg.Tiering.Safety.MaxBytesInFlight.Value,
		MaxBytesPerDay:          cfg.Tiering.Safety.MaxBytesPerDay.Value,
	})
	return c
}

func (c *controller) Store() *Store {
	return c.store
}

func (c *controller) Start(ctx context.Context) error {
	if c.cfg.Tiering.Mode == ModeOff {
		c.log.InfoContext(ctx, "tiering controller disabled")
		return nil
	}
	if len(c.clients) == 0 {
		return errors.New("tiering controller requires at least one ClickHouse client")
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	for _, client := range c.clients {
		c.seedNode(runCtx, client)
		for _, watch := range c.cfg.Watches {
			if watch.Settings == nil {
				continue
			}
			c.wg.Go(func() {
				c.reconcileLoop(runCtx, client, watch)
			})
		}
	}
	return nil
}

func (c *controller) Stop(ctx context.Context) error {
	if c.cancel != nil {
		c.cancel()
	}
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (c *controller) Pause(reason PauseReason) StatusSnapshot {
	return c.store.Pause(reason)
}

func (c *controller) Resume() StatusSnapshot {
	return c.store.Resume()
}

func (c *controller) client(nodeID string) (chclient.Client, bool) {
	for _, client := range c.clients {
		if client.Node.ID == nodeID {
			return client, true
		}
	}
	return chclient.Client{}, false
}

func (c *controller) watch(database string, table string) (EffectiveWatch, bool) {
	for _, watch := range c.cfg.Watches {
		if watch.Database == database && watch.Table == table {
			return watch, true
		}
	}
	return EffectiveWatch{}, false
}

func (c *controller) instanceID() string {
	if c.cfg.InstanceID != "" {
		return c.cfg.InstanceID
	}
	return "default"
}
