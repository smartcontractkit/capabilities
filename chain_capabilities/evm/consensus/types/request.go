package types

import (
	"context"
	"sync"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
)

type Request interface {
	ID() string
}

type ObservableRequest interface {
	Request
	CaptureObservation(ctx context.Context) error
}

var _ ObservableRequest = (*EventuallyConsistentRequest)(nil)

type EventuallyConsistentRequest struct {
	*observableRequest[[]byte]
}

func NewEventuallyConsistentRequest(id string, observe func(context.Context) ([]byte, error)) *EventuallyConsistentRequest {
	return &EventuallyConsistentRequest{
		observableRequest: &observableRequest[[]byte]{
			id:      id,
			observe: observe,
		},
	}
}

var _ ObservableRequest = (*AggregatabelRequest)(nil)

const (
	AggregationMethodFPlusOneHighest = "f+1-highest"
)

type AggregatabelRequest struct {
	*observableRequest[*evmservice.AggregatableObservation]
}

func NewAggregatabelRequest(id string, observe func(context.Context) (*evmservice.AggregatableObservation, error)) *AggregatabelRequest {
	return &AggregatabelRequest{
		observableRequest: &observableRequest[*evmservice.AggregatableObservation]{
			id:      id,
			observe: observe,
		},
	}
}

type observableRequest[T any] struct {
	id                string
	observation       T
	observationExists bool
	observationMutex  sync.Mutex
	observe           func(context.Context) (T, error)
}

func (r *observableRequest[T]) ID() string {
	return r.id
}

func (r *observableRequest[T]) CaptureObservation(ctx context.Context) error {
	observation, err := r.observe(ctx)
	if err != nil {
		return err
	}

	r.observationMutex.Lock()
	defer r.observationMutex.Unlock()
	r.observation = observation
	r.observationExists = true
	return nil
}

func (r *observableRequest[T]) GetObservation() (T, bool) {
	r.observationMutex.Lock()
	defer r.observationMutex.Unlock()
	return r.observation, r.observationExists
}

// SetObservation - sets observation. Should be used only for tests
func (r *observableRequest[T]) SetObservation(observation T) {
	r.observationMutex.Lock()
	defer r.observationMutex.Unlock()
	r.observation = observation
	r.observationExists = true
}

type LockableToBlockRequest struct {
	id      string
	observe func(context.Context, *evmservice.ChainHeight) ([]byte, error)
}

func NewLockableToBlockRequest(id string, observe func(context.Context, *evmservice.ChainHeight) ([]byte, error)) *LockableToBlockRequest {
	return &LockableToBlockRequest{
		id:      id,
		observe: observe,
	}
}

func (r *LockableToBlockRequest) ID() string {
	return r.id
}

func (r *LockableToBlockRequest) ToEventuallyConsistent(chainHeight *evmservice.ChainHeight) *EventuallyConsistentRequest {
	return NewEventuallyConsistentRequest(r.id, func(ctx context.Context) ([]byte, error) {
		return r.observe(ctx, chainHeight)
	})
}
