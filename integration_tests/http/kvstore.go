package http

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

type testKeyValueStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newTestKeyValueStore() *testKeyValueStore {
	return &testKeyValueStore{
		data: make(map[string][]byte),
	}
}

func (s *testKeyValueStore) Store(ctx context.Context, key string, val []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = val
	return nil
}

func (s *testKeyValueStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, exists := s.data[key]
	if !exists {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return value, nil
}

func (s *testKeyValueStore) PruneExpiredEntries(ctx context.Context, maxAge time.Duration) (int64, error) {
	return 0, nil
}

var _ core.KeyValueStore = (*testKeyValueStore)(nil)
