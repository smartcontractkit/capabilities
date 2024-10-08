package kvrequests

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var RequestsKey = "requests"

// RequestsStore is a store for incoming read and write requests.
// There is a guarantee that there is only one request per request ID.
type RequestsStore struct {
	lggr  logger.SugaredLogger
	store core.KeyValueStore
}

func New(store core.KeyValueStore, lggr logger.SugaredLogger) (*RequestsStore, error) {
	requests := []Request{}
	requestsBytes, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal requests: %w", err)
	}

	if err := store.Store(context.Background(), RequestsKey, requestsBytes); err != nil {
		return nil, fmt.Errorf("failed to initialize write requests: %w", err)
	}

	return &RequestsStore{
		store: store,
		lggr:  lggr,
	}, nil
}

// TODO: Cleanup the store when requests aren't processed for some time.
func (rs *RequestsStore) Add(ctx context.Context, newRequest *Request) error {
	storedRequestsBytes, err := rs.store.Get(ctx, RequestsKey)
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

	if err := rs.store.Store(ctx, RequestsKey, updatedRequestsBytes); err != nil {
		return fmt.Errorf("failed to store write requests: %w", err)
	}

	rs.lggr.Debugw("Request added", "request", newRequest)
	return nil
}

func (rs *RequestsStore) Update(ctx context.Context, updatedRequest Request) error {
	storedRequestsBytes, err := rs.store.Get(ctx, RequestsKey)
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

	return rs.store.Store(ctx, RequestsKey, updatedRequestsBytes)
}

type Filters struct {
	RequestIDs []RequestID
	Status     RequestStatus
}

// TODO: We might need to order requests and process them in batches.
func (rs *RequestsStore) Get(ctx context.Context, filters *Filters) ([]Request, error) {
	var requests []Request
	requestsBytes, err := rs.store.Get(ctx, RequestsKey)
	if err != nil {
		return requests, fmt.Errorf("failed to get write requests: %w", err)
	}

	if requestsBytes != nil {
		if err := json.Unmarshal(requestsBytes, &requests); err != nil {
			return requests, fmt.Errorf("failed to unmarshal write requests: %w", err)
		}
	}

	if filters == nil {
		return requests, nil
	}

	filteredRequests := []Request{}
	for _, request := range requests {
		if filters.Status != 0 && filters.Status != request.Status {
			continue
		}

		if len(filters.RequestIDs) > 0 {
			found := false
			for _, requestID := range filters.RequestIDs {
				if request.ID() == requestID {
					found = true
					break
				}
			}

			if !found {
				continue
			}
		}

		filteredRequests = append(filteredRequests, request)
	}

	return filteredRequests, nil
}

func (rs *RequestsStore) GetByID(ctx context.Context, requestID RequestID) (*Request, error) {
	requests, err := rs.Get(ctx, nil)
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

func (rs *RequestsStore) Remove(ctx context.Context, requestID RequestID) error {
	requests, err := rs.Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not retrieve requests: %w", err)
	}

	// This is not very efficient, but the number of requests should be small
	var updatedRequests []Request
	found := false
	for _, request := range requests {
		if request.ID() == requestID {
			found = true
		} else {
			updatedRequests = append(updatedRequests, request)
		}
	}

	if !found {
		return fmt.Errorf("could not find request with ID: %v", requestID)
	}

	updatedRequestsBytes, err := json.Marshal(updatedRequests)
	if err != nil {
		return fmt.Errorf("failed to marshal updated requests: %w", err)
	}

	return rs.store.Store(ctx, RequestsKey, updatedRequestsBytes)
}
