package shared

import (
	"context"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// Emitter abstracts the snapshot emission (beholder in prod, mock in tests).
type Emitter interface {
	Emit(ctx context.Context, data []byte) error
}

// ResourceManager orchestrates periodic snapshot emission for registered Meterables.
// It owns the cron loop and queries all registered resources on each tick.
type ResourceManager struct {
	services.StateMachine

	lggr     logger.Logger
	clock    clockwork.Clock
	interval time.Duration
	emitter  Emitter

	mu        sync.RWMutex
	resources []Meterable

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// ResourceManagerConfig contains configuration for the ResourceManager.
type ResourceManagerConfig struct {
	// SnapshotInterval is how often to emit snapshots.
	SnapshotInterval time.Duration

	// Emitter handles the actual emission (e.g., to beholder/CHiP).
	Emitter Emitter

	// Clock is optional, defaults to real clock. Inject fake clock for testing.
	Clock clockwork.Clock
}

// NewResourceManager creates a new ResourceManager.
func NewResourceManager(lggr logger.Logger, cfg ResourceManagerConfig) *ResourceManager {
	clock := cfg.Clock
	if clock == nil {
		clock = clockwork.NewRealClock()
	}

	return &ResourceManager{
		lggr:      lggr,
		clock:     clock,
		interval:  cfg.SnapshotInterval,
		emitter:   cfg.Emitter,
		resources: make([]Meterable, 0),
		stopCh:    make(chan struct{}),
	}
}

// Register adds a Meterable resource to be included in snapshots.
// Safe to call before or after Start.
func (rm *ResourceManager) Register(m Meterable) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.resources = append(rm.resources, m)
	rm.lggr.Debugw("registered meterable resource",
		"entity", m.ResourceInfo().Entity,
		"resource", m.ResourceInfo().Resource,
	)
}

// Start begins the periodic snapshot loop.
func (rm *ResourceManager) Start(ctx context.Context) error {
	return rm.StartOnce("ResourceManager", func() error {
		rm.wg.Add(1)
		go rm.loop(ctx)
		rm.lggr.Info("ResourceManager started")
		return nil
	})
}

// Close stops the ResourceManager and waits for the loop to exit.
func (rm *ResourceManager) Close() error {
	return rm.StopOnce("ResourceManager", func() error {
		close(rm.stopCh)
		rm.wg.Wait()
		rm.lggr.Info("ResourceManager closed")
		return nil
	})
}

// Name returns the service name.
func (rm *ResourceManager) Name() string {
	return "ResourceManager"
}

// HealthReport returns the health status.
func (rm *ResourceManager) HealthReport() map[string]error {
	return map[string]error{rm.Name(): rm.Healthy()}
}

func (rm *ResourceManager) loop(ctx context.Context) {
	defer rm.wg.Done()

	ticker := rm.clock.NewTicker(rm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rm.stopCh:
			return
		case <-ticker.Chan():
			rm.emitSnapshots(ctx)
		}
	}
}

func (rm *ResourceManager) emitSnapshots(ctx context.Context) {
	rm.mu.RLock()
	resources := make([]Meterable, len(rm.resources))
	copy(resources, rm.resources)
	rm.mu.RUnlock()

	timestamp := rm.clock.Now().UTC().Format(time.RFC3339)

	for _, m := range resources {
		info := m.ResourceInfo()
		utilization := m.GetUtilization(ctx)

		snapshot := &Snapshot{
			Timestamp:    timestamp,
			Entity:       info.Entity,
			Resource:     info.Resource,
			ResourceType: info.ResourceType,
			Utilization:  utilization,
		}

		if err := rm.emit(ctx, snapshot); err != nil {
			rm.lggr.Errorw("failed to emit snapshot",
				"entity", info.Entity,
				"resource", info.Resource,
				"error", err,
			)
			continue
		}

		rm.lggr.Debugw("emitted snapshot",
			"entity", info.Entity,
			"resource", info.Resource,
			"utilizationCount", len(utilization),
		)
	}
}

func (rm *ResourceManager) emit(ctx context.Context, snapshot *Snapshot) error {
	if rm.emitter == nil {
		return nil // no-op if no emitter configured
	}

	data, err := proto.Marshal(snapshot)
	if err != nil {
		return err
	}

	return rm.emitter.Emit(ctx, data)
}

// Snapshot returns a snapshot for all registered resources (useful for testing/debugging).
func (rm *ResourceManager) Snapshot(ctx context.Context) []*Snapshot {
	rm.mu.RLock()
	resources := make([]Meterable, len(rm.resources))
	copy(resources, rm.resources)
	rm.mu.RUnlock()

	timestamp := rm.clock.Now().UTC().Format(time.RFC3339)
	snapshots := make([]*Snapshot, 0, len(resources))

	for _, m := range resources {
		info := m.ResourceInfo()
		snapshots = append(snapshots, &Snapshot{
			Timestamp:    timestamp,
			Entity:       info.Entity,
			Resource:     info.Resource,
			ResourceType: info.ResourceType,
			Utilization:  m.GetUtilization(ctx),
		})
	}

	return snapshots
}
