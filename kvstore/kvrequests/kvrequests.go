package kvrequests

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var RequestsKey = "requests"

// RequestsStore is a store for incoming read and write requests.
// There is a guarantee that there is only one request per request ID.
type RequestsStore struct {
	lggr     logger.SugaredLogger
	requests map[RequestID]*Request
	mu       sync.RWMutex
}

func New(lggr logger.SugaredLogger) (*RequestsStore, error) {
	return &RequestsStore{
		requests: make(map[RequestID]*Request),
		lggr:     lggr,
	}, nil
}

func (rs *RequestsStore) Add(ctx context.Context, newRequest *Request) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	_, exists := rs.requests[newRequest.ID()]
	if exists {
		return fmt.Errorf("request with ID %v already exists", newRequest.ID())
	}

	rs.requests[newRequest.ID()] = newRequest
	rs.lggr.Debugw("Request added", "request", newRequest)
	return nil
}

func (rs *RequestsStore) Update(ctx context.Context, updatedRequest *Request) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	_, exists := rs.requests[updatedRequest.ID()]
	if !exists {
		return fmt.Errorf("request with ID %v does not exist", updatedRequest.ID())
	}

	rs.requests[updatedRequest.ID()] = updatedRequest
	rs.lggr.Debugw("Request updated", "request", updatedRequest)
	return nil
}

type Filters struct {
	RequestIDs []RequestID
	Status     RequestStatus
}

func (rs *RequestsStore) Get(ctx context.Context, filters *Filters) ([]Request, error) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	requests := []Request{}

	if filters == nil {
		for _, r := range rs.requests {
			requests = append(requests, *r)
		}
		return requests, nil
	}

	for _, request := range rs.requests {
		if filters.Status != RequestStatusUnspecified && filters.Status != request.Status {
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

		requests = append(requests, *request)
	}

	return requests, nil
}

func (rs *RequestsStore) GetByID(ctx context.Context, requestID RequestID) *Request {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.requests[requestID]
}

func (rs *RequestsStore) Remove(ctx context.Context, requestID RequestID) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.requests, requestID)
}
