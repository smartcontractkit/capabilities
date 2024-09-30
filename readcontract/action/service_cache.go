package actions

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type StatsCollector interface {
	OnCacheHit()
	OnCacheMiss()
	OnCacheEviction(int)
	OnCacheAddition()
}

// ServiceCache is a cache that evicts and closes services after a configurable period of inactivity once a minimum size is reached.
// It is safe for concurrent use.  The ServiceCache is responsible for starting and stopping the services it caches and itself should
// be closed to ensure all services are stopped.
type ServiceCache[I comparable, S services.Service] struct {
	services.StateMachine
	lggr logger.Logger
	name string

	m  map[I]*cachedService[S]
	mu sync.RWMutex

	wg       sync.WaitGroup
	stopChan services.StopChan

	cacheCleanupInterval time.Duration
	cacheExpiryTime      time.Duration
	cleanupAfterSize     int

	statsCollector StatsCollector

	clock clockwork.Clock
}

type cachedService[S services.Service] struct {
	service       S
	lastFetchedAt time.Time
}

type noopStatsCollector struct{}

func (n *noopStatsCollector) OnCacheHit()         {}
func (n *noopStatsCollector) OnCacheMiss()        {}
func (n *noopStatsCollector) OnCacheEviction(int) {}
func (n *noopStatsCollector) OnCacheAddition()    {}

// NewServiceCache creates a new ServiceCache with the given parameters.  The cache will only initiate expiration of services
// once the cleanupAfterSize is reached. An optional statsCollector can be provided to collect cache stats.
func NewServiceCache[I comparable, S services.Service](lggr logger.Logger, name string, clock clockwork.Clock, cacheCleanupInterval, cacheExpiryTime time.Duration, cleanupAfterSize int,
	statsCollector StatsCollector) *ServiceCache[I, S] {
	if statsCollector == nil {
		statsCollector = &noopStatsCollector{}
	}

	return &ServiceCache[I, S]{
		lggr:                 lggr,
		name:                 name,
		m:                    map[I]*cachedService[S]{},
		cacheCleanupInterval: cacheCleanupInterval,
		cacheExpiryTime:      cacheExpiryTime,
		cleanupAfterSize:     cleanupAfterSize,
		clock:                clock,
		stopChan:             make(chan struct{}),
		statsCollector:       statsCollector,
	}
}

func (ec *ServiceCache[I, S]) Name() string {
	return ec.name
}

func (ec *ServiceCache[I, S]) Start() error {
	return ec.StartOnce(ec.name, func() error {
		ec.wg.Add(1)
		go func() {
			defer ec.wg.Done()
			ec.reapLoop()
		}()
		return nil
	})
}

func (ec *ServiceCache[I, S]) Close() error {
	return ec.StopOnce(ec.Name(), func() error {
		close(ec.stopChan)
		ec.wg.Wait()

		ec.mu.Lock()
		defer ec.mu.Unlock()

		for id, m := range ec.m {
			if err := m.service.Close(); err != nil {
				ec.lggr.Errorw("failed to close service", "id", id, "error", err)
			}
		}

		return nil
	})
}

func (ec *ServiceCache[I, S]) reapLoop() {
	ticker := ec.clock.NewTicker(ec.cacheCleanupInterval)
	for {
		select {
		case <-ticker.Chan():
			ec.evictOlderThan(ec.cacheExpiryTime)
		case <-ec.stopChan:
			return
		}
	}
}

func (ec *ServiceCache[I, S]) AddAndStart(ctx context.Context, id I, s S) error {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	if err := s.Start(ctx); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	ec.m[id] = &cachedService[S]{
		service:       s,
		lastFetchedAt: time.Now(),
	}
	ec.statsCollector.OnCacheAddition()

	return nil
}

func (ec *ServiceCache[I, S]) Get(id I) (S, bool) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	fetchedValue, ok := ec.m[id]
	if !ok {
		ec.statsCollector.OnCacheMiss()
		var zero S
		return zero, false
	}

	ec.statsCollector.OnCacheHit()
	fetchedValue.lastFetchedAt = ec.clock.Now()
	return fetchedValue.service, true
}

func (ec *ServiceCache[I, S]) evictOlderThan(duration time.Duration) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	evicted := 0

	if len(ec.m) > ec.cleanupAfterSize {
		for id, m := range ec.m {
			if ec.clock.Now().Sub(m.lastFetchedAt) > duration {
				if err := m.service.Close(); err != nil {
					ec.lggr.Errorw("failed to close service", "id", id, "error", err)
				}

				delete(ec.m, id)
				evicted++
			}

			if len(ec.m) <= ec.cleanupAfterSize {
				break
			}
		}
	}

	ec.statsCollector.OnCacheEviction(evicted)
}
