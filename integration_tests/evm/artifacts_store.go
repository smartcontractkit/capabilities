package evmlogtrigger

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"
)

// artifactsStore handles capability requests like getting capability binary and config.
type artifactsStore struct {
	lock    sync.Mutex
	kvStore map[string][]byte
}

func newArtifactsStore() *artifactsStore {
	return &artifactsStore{
		kvStore: make(map[string][]byte),
	}
}

func (h *artifactsStore) SetValue(url string, value []byte) {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.kvStore[url] = value
}

func (h *artifactsStore) Fetch(_ context.Context, _ string, req capabilities.Request) ([]byte, error) {
	h.lock.Lock()
	defer h.lock.Unlock()
	result, ok := h.kvStore[req.URL]
	if !ok {
		return nil, fmt.Errorf("unknown URL: %s", req.URL)
	}

	return result, nil
}
