package actions_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	actions "github.com/smartcontractkit/capabilities/readcontract/action"
)

type testService struct {
	A       int
	mux     sync.Mutex
	running bool
}

func (t *testService) IsRunning() bool {
	t.mux.Lock()
	defer t.mux.Unlock()
	return t.running
}

func (t *testService) Start(ctx context.Context) error {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.running = true
	return nil
}

func (t *testService) Close() error {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.running = false
	return nil
}

func (t *testService) Ready() error {
	panic("not implemented")
}

func (t *testService) HealthReport() map[string]error {
	panic("not implemented")
}

func (t *testService) Name() string {
	return "TestService"
}

func TestServiceCache(t *testing.T) {
	log := logger.Test(t)
	clock := clockwork.NewFakeClock()
	tick := 1 * time.Second
	timeout := 1 * time.Second

	stats := &mockStatsCollector{}
	cache := actions.NewServiceCache[string, services.Service](log, "TestServiceCache", clock, tick, timeout, 0, stats)
	err := cache.Start()
	require.NoError(t, err)
	defer func() {
		err = cache.Close()
		require.NoError(t, err)
	}()

	id := uuid.New().String()
	value := &testService{A: 42}
	err = cache.AddAndStart(context.Background(), id, value)
	require.NoError(t, err)

	// Verify the service is started
	assert.True(t, value.IsRunning())

	got, ok := cache.Get(id)
	assert.True(t, ok)
	assert.Equal(t, value, got)

	assert.Eventually(t, func() bool {
		clock.Advance(15 * time.Second)
		return stats.Evictions() == 1
	}, 100*time.Second, 100*time.Millisecond)

	_, ok = cache.Get(id)
	assert.False(t, ok)

	// Verify the service is stopped
	assert.False(t, value.IsRunning())
}

func TestServiceCache_DoesNotEvictIfBelowMinimumSize(t *testing.T) {
	log := logger.Test(t)
	clock := clockwork.NewFakeClock()
	tick := 1 * time.Second
	timeout := 60 * time.Second

	cache := actions.NewServiceCache[string, services.Service](log, "TestServiceCache", clock, tick, timeout, 1, nil)
	err := cache.Start()
	require.NoError(t, err)
	defer func() {
		err = cache.Close()
		require.NoError(t, err)
	}()

	id := uuid.New().String()
	value := &testService{A: 42}
	err = cache.AddAndStart(context.Background(), id, value)
	require.NoError(t, err)

	// Verify the service is started
	assert.True(t, value.IsRunning())

	got, ok := cache.Get(id)
	assert.True(t, ok)
	assert.Equal(t, value, got)

	clock.Advance(120 * time.Second)
	_, ok = cache.Get(id)
	assert.True(t, ok)

	// Verify the service is still running
	assert.True(t, value.IsRunning())
}

func TestServiceCache_DoesNotEvictBelowMinimumSize(t *testing.T) {
	log := logger.Test(t)
	clock := clockwork.NewFakeClock()
	tick := 1 * time.Second
	timeout := 60 * time.Second

	stats := &mockStatsCollector{}

	cache := actions.NewServiceCache[string, services.Service](log, "TestServiceCache", clock, tick, timeout, 1, stats)
	err := cache.Start()
	require.NoError(t, err)
	defer func() {
		err = cache.Close()
		require.NoError(t, err)
	}()

	id1 := uuid.New().String()
	value1 := &testService{A: 43}
	err = cache.AddAndStart(context.Background(), id1, value1)
	require.NoError(t, err)

	id2 := uuid.New().String()
	value2 := &testService{A: 44}
	err = cache.AddAndStart(context.Background(), id2, value2)
	require.NoError(t, err)

	// Verify both services are started
	assert.True(t, value1.IsRunning())
	assert.True(t, value2.IsRunning())

	// Advance time to check eviction behavior
	assert.Eventually(t, func() bool {
		clock.Advance(120 * time.Second)
		return stats.Evictions() == 1
	}, 100*time.Second, 100*time.Millisecond)

	_, ok1 := cache.Get(id1)
	_, ok2 := cache.Get(id2)
	assert.True(t, ok1 != ok2)

	// Verify one service is stopped
	assert.True(t, value1.IsRunning() != value2.IsRunning())
}

func TestServiceCache_ExpiryTimeResetAfterFetch(t *testing.T) {
	log := logger.Test(t)
	clock := clockwork.NewFakeClock()
	tick := 1 * time.Second
	timeout := 100 * time.Second

	cache := actions.NewServiceCache[string, services.Service](log, "TestServiceCache", clock, tick, timeout, 0, nil)
	err := cache.Start()
	require.NoError(t, err)
	defer func() {
		err = cache.Close()
		require.NoError(t, err)
	}()

	id := uuid.New().String()
	value := &testService{A: 42}
	err = cache.AddAndStart(context.Background(), id, value)
	require.NoError(t, err)

	// Verify the service is started
	assert.True(t, value.IsRunning())

	clock.Advance(timeout / 2)

	// Fetch the item to reset its expiry time
	_, ok := cache.Get(id)
	assert.True(t, ok)

	clock.Advance(timeout)

	_, ok = cache.Get(id)
	assert.True(t, ok)

	// Verify the service is still running
	assert.True(t, value.IsRunning())
}

func TestServiceCache_CloseClosesAllServices(t *testing.T) {
	log := logger.Test(t)
	clock := clockwork.NewFakeClock()
	tick := 1 * time.Second
	timeout := 60 * time.Second

	cache := actions.NewServiceCache[string, services.Service](log, "TestServiceCache", clock, tick, timeout, 0, nil)
	err := cache.Start()
	require.NoError(t, err)

	id1 := uuid.New().String()
	value1 := &testService{A: 43}
	err = cache.AddAndStart(context.Background(), id1, value1)
	require.NoError(t, err)

	id2 := uuid.New().String()
	value2 := &testService{A: 44}
	err = cache.AddAndStart(context.Background(), id2, value2)
	require.NoError(t, err)

	// Verify both services are started
	assert.True(t, value1.IsRunning())
	assert.True(t, value2.IsRunning())

	// Close the cache
	err = cache.Close()
	require.NoError(t, err)

	// Verify both services are stopped
	assert.False(t, value1.IsRunning())
	assert.False(t, value2.IsRunning())
}

func TestServiceCache_StatsCollector(t *testing.T) {
	log := logger.Test(t)
	clock := clockwork.NewFakeClock()
	tick := 1 * time.Second
	timeout := 10 * time.Second

	stats := &mockStatsCollector{}
	cache := actions.NewServiceCache[string, services.Service](log, "TestServiceCache", clock, tick, timeout, 0, stats)
	err := cache.Start()
	require.NoError(t, err)
	defer func() {
		err = cache.Close()
		require.NoError(t, err)
	}()

	id := uuid.New().String()
	value := &testService{A: 42}
	err = cache.AddAndStart(context.Background(), id, value)
	require.NoError(t, err)

	// Verify the service is started
	assert.True(t, value.IsRunning())

	// Check addition count
	assert.Equal(t, 1, stats.Additions())

	// Fetch the item to increment hit counter
	_, ok := cache.Get(id)
	assert.True(t, ok)
	assert.Equal(t, 1, stats.Hits())

	// Fetch a non-existent item to increment miss counter
	_, ok = cache.Get("non-existent")
	assert.False(t, ok)
	assert.Equal(t, 1, stats.Misses())

	assert.Eventually(t, func() bool {
		clock.Advance(15 * time.Second)
		return stats.Evictions() == 1
	}, 100*time.Second, 100*time.Millisecond)

	_, ok = cache.Get(id)
	assert.False(t, ok)

	// Verify the service is stopped
	assert.False(t, value.IsRunning())
}

type mockStatsCollector struct {
	mu        sync.Mutex
	hits      int
	misses    int
	evictions int
	additions int
}

func (m *mockStatsCollector) OnCacheHit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hits++
}

func (m *mockStatsCollector) OnCacheMiss() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.misses++
}

func (m *mockStatsCollector) OnCacheEviction(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictions += count
}

func (m *mockStatsCollector) OnCacheAddition() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.additions++
}

func (m *mockStatsCollector) Hits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hits
}

func (m *mockStatsCollector) Misses() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.misses
}

func (m *mockStatsCollector) Evictions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evictions
}

func (m *mockStatsCollector) Additions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.additions
}
