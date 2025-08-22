package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

type testKVStore struct {
	mu                   sync.RWMutex
	data                 map[string][]byte
	prunedCount          int64
	pruneError           error
	simulatePruneFailure bool
}

func newTestKVStore() *testKVStore {
	return &testKVStore{
		data:        make(map[string][]byte),
		prunedCount: 0,
	}
}

func (s *testKVStore) setPrunedCount(count int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunedCount = count
}

func (s *testKVStore) setPruneError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneError = err
	s.simulatePruneFailure = err != nil
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

func (s *testKVStore) PruneExpiredEntries(ctx context.Context, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.simulatePruneFailure {
		return 0, s.pruneError
	}

	return s.prunedCount, nil
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
			Method:  gateway_common.MethodWorkflowExecute,
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
	require.Error(t, err)
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

func TestRequestCache_Cleanup_Success(t *testing.T) {
	t.Parallel()

	lggr := logger.Sugared(logger.Test(t))
	kvstore := newTestKVStore()
	cache := newRequestCache(lggr, kvstore, time.Hour)

	// Simulate that 5 entries were pruned
	kvstore.setPrunedCount(5)

	count, err := cache.cleanup(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(5), count)
}

func TestRequestCache_Cleanup_Error(t *testing.T) {
	t.Parallel()

	lggr := logger.Sugared(logger.Test(t))
	kvstore := newTestKVStore()
	cache := newRequestCache(lggr, kvstore, time.Hour)

	// Simulate an error during pruning
	expectedErr := errors.New("database connection failed")
	kvstore.setPruneError(expectedErr)

	count, err := cache.cleanup(t.Context())
	require.Error(t, err)
	require.Equal(t, expectedErr, err)
	require.Equal(t, int64(0), count)
}
