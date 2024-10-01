package kvrequests

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var WriteRequestsKey = "write_requests"

type RequestsStore struct {
	store core.KeyValueStore
}

type WriteRequest struct {
	KVPairs map[string][]byte
}

func New(store core.KeyValueStore) *RequestsStore {
	return &RequestsStore{
		store: store,
	}
}

func (rs *RequestsStore) AddWriteRequest(ctx context.Context, weID string, kvpairs map[string][]byte) error {
	writeRequestsBytes, err := rs.store.Get(ctx, WriteRequestsKey)
	if err != nil {
		return fmt.Errorf("failed to get write requests: %w", err)
	}

	var writeRequests map[string]WriteRequest
	if writeRequestsBytes != nil {
		if err := json.Unmarshal(writeRequestsBytes, &writeRequests); err != nil {
			return fmt.Errorf("failed to unmarshal write requests: %w", err)
		}
	} else {
		// Initialize the map if it doesn't exist
		writeRequests = make(map[string]WriteRequest)
	}

	writeRequests[weID] = WriteRequest{
		KVPairs: kvpairs,
	}

	writeReqeustBytes, err := json.Marshal(writeRequests)
	if err != nil {
		return fmt.Errorf("failed to marshal write requests: %w", err)
	}

	return rs.store.Store(ctx, WriteRequestsKey, writeReqeustBytes)
}
