package requests

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

type Observation struct {
	height uint64
	value  []byte
}

type RequestWithConsensusHeight struct {
	RequestID string
	Height    uint64
}

type ConsensusHandler struct {
	activeConsensusRequests metric.Int64UpDownCounter
	activeObservationsCount metric.Int64UpDownCounter
	requests                map[string]*request
	mux                     sync.Mutex
}

func NewConsensusHandler() (*ConsensusHandler, error) {
	activeConsensusRequest, err := beholder.GetMeter().Int64UpDownCounter("ConsensusHandlerActiveRequests")
	if err != nil {
		return nil, fmt.Errorf("failed to register active consensus request counter: %w", err)
	}

	activeObservationsCount, err := beholder.GetMeter().Int64UpDownCounter("ConsensusHandlerActiveObservationsCount")
	if err != nil {
		return nil, fmt.Errorf("failed to register active observations count counter: %w", err)
	}

	return &ConsensusHandler{
		activeConsensusRequests: activeConsensusRequest,
		activeObservationsCount: activeObservationsCount,
		requests:                make(map[string]*request),
	}, nil
}

func (r *ConsensusHandler) StartConsensusRequest(ctx context.Context, requestID string, observationsBeforeHeightReset int) (<-chan []byte, error) {
	r.mux.Lock()
	defer r.mux.Unlock()

	responseCh := make(chan []byte, 1)
	r.requests[requestID] = &request{id: requestID, responseCh: responseCh, observationsBeforeHeightReset: observationsBeforeHeightReset}

	r.activeConsensusRequests.Add(ctx, 1)

	return responseCh, nil
}

func (r *ConsensusHandler) StopConsensusRequest(requestID string) {
	r.mux.Lock()
	defer r.mux.Unlock()

	if req, exists := r.requests[requestID]; exists {
		close(req.responseCh)
		delete(r.requests, requestID)
	}
}

func (r *ConsensusHandler) AddObservationForRequest(ctx context.Context, requestID string, height uint64, value []byte) error {
	r.mux.Lock()
	defer r.mux.Unlock()

	if req, exists := r.requests[requestID]; exists {
		if err := req.addObservation(height, value); err != nil {
			return fmt.Errorf("failed to add observation to request: %w", err)
		}

		r.activeObservationsCount.Add(ctx, 1)

		return nil
	}

	return nil
}

func (r *ConsensusHandler) SetConsensusHeightForRequest(requestID string, height uint64) {
	r.mux.Lock()
	defer r.mux.Unlock()

	if req, exists := r.requests[requestID]; exists {
		req.consensusHeight = &height
	}
}

func (r *ConsensusHandler) GetAllRequestIDs() []string {
	r.mux.Lock()
	defer r.mux.Unlock()

	var ids []string
	for id := range r.requests {
		ids = append(ids, id)
	}

	return ids
}

func (r *ConsensusHandler) GetRequestsWithConsensusHeight() []RequestWithConsensusHeight {
	r.mux.Lock()
	defer r.mux.Unlock()

	var requests []RequestWithConsensusHeight
	for _, req := range r.requests {
		if req.consensusHeight != nil {
			requests = append(requests, RequestWithConsensusHeight{
				RequestID: req.id,
				Height:    *req.consensusHeight,
			})
		}
	}

	return requests
}

// GetLatestObservedHeightForRequest returns the latest observed height for a given request or nil if no observations exist for the given request.
func (r *ConsensusHandler) GetLatestObservedHeightForRequest(requestID string) *uint64 {
	r.mux.Lock()
	defer r.mux.Unlock()

	if req, exists := r.requests[requestID]; exists {
		if req.GetLatestObservation() != nil {
			return &req.GetLatestObservation().height
		}

		return nil
	}

	return nil
}

// GetValueAtHeight returns the value at the given height.  Nil if a value is not available at the given height for the request id.
func (r *ConsensusHandler) GetValueAtHeight(requestID string, height uint64) []byte {
	r.mux.Lock()
	defer r.mux.Unlock()

	if req, exists := r.requests[requestID]; exists {
		for _, obs := range req.observations {
			if obs.height == height {
				return obs.value
			}
		}
		return nil
	}

	return nil
}

func (r *ConsensusHandler) SetConsensusValue(ctx context.Context, requestID string, value []byte) {
	r.mux.Lock()
	defer r.mux.Unlock()
	if req, exists := r.requests[requestID]; exists {
		delete(r.requests, requestID)
		req.responseCh <- value
		close(req.responseCh)

		r.activeConsensusRequests.Add(ctx, -1)
		r.activeObservationsCount.Add(ctx, -int64(req.getObservationCount()))
	}
}
