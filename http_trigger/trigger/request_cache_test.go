package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

type testKVStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newTestKVStore() *testKVStore {
	return &testKVStore{
		data: make(map[string][]byte),
	}
}

func (s *testKVStore) Store(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *testKVStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, exists := s.data[key]
	if !exists {
		return nil, sql.ErrNoRows
	}
	return value, nil
}
func TestRequestCache_Add_Success(t *testing.T) {
	t.Parallel()

	lggr := logger.Sugared(logger.Test(t))
	kvstore := newTestKVStore()
	cache := newRequestCache(lggr, kvstore, time.Hour)

	resultJSON := json.RawMessage(`{"result":"test"}`)
	entry := requestCacheEntry{
		ReqHash:     "test-hash",
		WorkflowID:  "0x123",
		ExecutionID: "0x456",
		RequestID:   "req-123",
		Response: &jsonrpc.Response[json.RawMessage]{
			Version: "2.0",
			ID:      "req-123",
			Result:  &resultJSON,
		},
	}

	err := cache.add(t.Context(), entry)
	require.NoError(t, err)

	retrieved, err := cache.get(t.Context(), entry.RequestID)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, entry.ExecutionID, retrieved.ExecutionID)
	require.Equal(t, entry.RequestID, retrieved.RequestID)
	require.Equal(t, entry.ReqHash, retrieved.ReqHash)
	require.Equal(t, entry.WorkflowID, retrieved.WorkflowID)
	require.Equal(t, entry.Response.ID, retrieved.Response.ID)
	require.Equal(t, entry.Response.Version, retrieved.Response.Version)
	require.Equal(t, *entry.Response.Result, *retrieved.Response.Result)
}

func TestRequestCache_Get_NotFound(t *testing.T) {
	t.Parallel()

	lggr := logger.Sugared(logger.Test(t))
	kvstore := newTestKVStore()
	cache := newRequestCache(lggr, kvstore, time.Hour)

	requestID := "0x789"

	result, err := cache.get(t.Context(), requestID)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestRequestCache_Get_NilValue(t *testing.T) {
	t.Parallel()

	lggr := logger.Sugared(logger.Test(t))
	kvstore := newTestKVStore()
	cache := newRequestCache(lggr, kvstore, time.Hour)

	requestID := "0x789"

	// Store a nil value directly in the kvstore
	err := kvstore.Store(t.Context(), requestID, nil)
	require.NoError(t, err)

	result, err := cache.get(t.Context(), requestID)
	require.NoError(t, err)
	require.Nil(t, result)
}
