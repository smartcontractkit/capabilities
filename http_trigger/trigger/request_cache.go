package trigger

import (
	"context"
	"encoding/json"
	"time"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// requestCacheEntry stores information about a processed request for idempotency
type requestCacheEntry struct {
	ReqHash     string                             `json:"reqHash"`  // digest of the JSON-RPC request payload
	Response    *jsonrpc.Response[json.RawMessage] `json:"response"` // The response that was sent
	WorkflowID  string                             `json:"workflowID"`
	ExecutionID string                             `json:"executionID"`
	RequestID   string                             `json:"requestID"`
}

type requestCache struct {
	lggr    logger.SugaredLogger
	kvstore core.KeyValueStore
	ttl     time.Duration
}

func newRequestCache(lggr logger.SugaredLogger, kvstore core.KeyValueStore, ttl time.Duration) *requestCache {
	return &requestCache{
		lggr:    lggr,
		kvstore: kvstore,
		ttl:     ttl,
	}
}

func (c *requestCache) add(ctx context.Context, entry requestCacheEntry) error {
	val, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.kvstore.Store(ctx, entry.RequestID, val)
}

func (c *requestCache) get(ctx context.Context, requestID string) (*requestCacheEntry, error) {
	val, err := c.kvstore.Get(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	var entry requestCacheEntry
	err = json.Unmarshal(val, &entry)
	return &entry, err
}

func (c *requestCache) cleanup(ctx context.Context) (int64, error) {
	pruned, err := c.kvstore.PruneExpiredEntries(ctx, c.ttl)
	if err != nil {
		c.lggr.Errorw("failed to cleanup request cache", "error", err)
		return 0, err
	}
	c.lggr.Infow("pruned expired entries from request cache", "pruned", pruned)
	return pruned, nil
}
