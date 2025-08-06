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

func (c *requestCache) cleanup(ctx context.Context) error {
	// TODO: PRODCRE-715 Periodically cleanup expired entries
	return nil
}
