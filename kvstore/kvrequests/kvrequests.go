package kvrequests

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var WriteRequestsKey = "write_requests"

// RequestsStore is a store for incoming read and write requests.
// There is a guarantee that there is only one request per workflow execution ID per type.
type RequestsStore struct {
	store core.KeyValueStore
}

// [ERROR] call to ReportingPlugin.Query errored              protocol/common.go:40            configDigest=000116e2c7d0c4c8862b52f3c108434a5712392698355d3cec547eae2cd91306 e=3 error=rpc error: code = Unknown desc = could not retrieve requests: failed to get write requests: failed to get value for key: write_requests: rpc error: code = Unknown desc = failed to get bytes for key: write_requests: failed to get value by key: write_requests for jobID: 1 : sql: no rows in result set  l=3 logger=StandardCapabilities.1 oid=3 proto=outgen round=1 seqNr=1 stacktrace=github.com/smartcontractkit/libocr/offchainreporting2plus/internal/ocr3/protocol.callPlugin[...]

func New(store core.KeyValueStore) (*RequestsStore, error) {
	requests := []Request{}
	requestsBytes, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal requests: %w", err)
	}

	if err := store.Store(context.Background(), WriteRequestsKey, requestsBytes); err != nil {
		return nil, fmt.Errorf("failed to initialize write requests: %w", err)
	}

	return &RequestsStore{
		store: store,
	}, nil
}

// TODO: Cleanup the store when requests aren't processed for some time.
func (rs *RequestsStore) Add(ctx context.Context, newRequest *Request) error {
	storedRequestsBytes, err := rs.store.Get(ctx, WriteRequestsKey)
	if err != nil {
		return fmt.Errorf("failed to get write requests: %w", err)
	}

	var storedRequests []Request
	if storedRequestsBytes != nil {
		if err := json.Unmarshal(storedRequestsBytes, &storedRequests); err != nil {
			return fmt.Errorf("failed to unmarshal write requests: %w", err)
		}
	}

	// Check if the request already exists in the store
	// Looping instead of storing unique keys to make it easier to cleanup stale requests
	for _, sotredRequest := range storedRequests {
		if sotredRequest.ID() == newRequest.ID() {
			return nil
		}
		// TODO: Add logic to remove stale requests
	}

	// At this point we know that the request doesn't exist in the store
	updatedRequests := append(storedRequests, *newRequest)

	updatedRequestsBytes, err := json.Marshal(updatedRequests)
	if err != nil {
		return fmt.Errorf("failed to marshal write requests: %w", err)
	}

	return rs.store.Store(ctx, WriteRequestsKey, updatedRequestsBytes)
}

func (rs *RequestsStore) Update(ctx context.Context, updatedRequest Request) error {
	storedRequestsBytes, err := rs.store.Get(ctx, WriteRequestsKey)
	if err != nil {
		return fmt.Errorf("failed to get write requests: %w", err)
	}

	var storedRequests []Request
	if storedRequestsBytes != nil {
		if err := json.Unmarshal(storedRequestsBytes, &storedRequests); err != nil {
			return fmt.Errorf("failed to unmarshal write requests: %w", err)
		}
	}

	// Check if the request already exists in the store
	// Looping instead of storing unique keys to make it easier to cleanup stale requests
	for i, sotredRequest := range storedRequests {
		if sotredRequest.ID() == updatedRequest.ID() {
			storedRequests[i] = updatedRequest
			break
		}
	}

	updatedRequestsBytes, err := json.Marshal(storedRequests)
	if err != nil {
		return fmt.Errorf("failed to marshal write requests: %w", err)
	}

	return rs.store.Store(ctx, WriteRequestsKey, updatedRequestsBytes)
}

// TODO: We might need to order requests and process them in batches.
func (rs *RequestsStore) Get(ctx context.Context) ([]Request, error) {
	var requests []Request
	requestsBytes, err := rs.store.Get(ctx, WriteRequestsKey)
	if err != nil {
		return requests, fmt.Errorf("failed to get write requests: %w", err)
	}

	if requestsBytes != nil {
		if err := json.Unmarshal(requestsBytes, &requests); err != nil {
			return requests, fmt.Errorf("failed to unmarshal write requests: %w", err)
		}
	}

	return requests, nil
}

func (rs *RequestsStore) GetByIDs(ctx context.Context, requestIDs []RequestID) ([]Request, error) {
	requests, err := rs.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve requests: %w", err)
	}

	requestsByID := []Request{}

	// This is not very efficient, but the number of requests should be small
	for _, request := range requests {
		for _, requestID := range requestIDs {
			if request.ID() == requestID {
				requestsByID = append(requestsByID, request)
			}
		}
	}

	return requestsByID, nil
}

func (rs *RequestsStore) GetByID(ctx context.Context, requestID RequestID) (*Request, error) {
	requests, err := rs.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve requests: %w", err)
	}

	// This is not very efficient, but the number of requests should be small
	for _, request := range requests {
		if request.ID() == requestID {
			return &request, nil
		}
	}

	return nil, fmt.Errorf("could not find request with ID: %v", requestID)
}
