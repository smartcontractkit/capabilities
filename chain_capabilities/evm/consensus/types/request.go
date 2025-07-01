package types

import (
	"context"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type Request interface {
	ID() string
	Copy() Request
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

func (r *EventuallyConsistentRequest) Copy() Request {
	// intentionally reuse the same instance, since it's thread safe and we need to get most recent captured observation
	return r
}

var _ ObservableRequest = (*AggregatableRequest)(nil)

type AggregatableRequest struct {
	*observableRequest[*pb.Decimal]
}

func NewAggregatableRequest(id string, observe func(context.Context) (*pb.Decimal, error)) *AggregatableRequest {
	return &AggregatableRequest{
		observableRequest: &observableRequest[*pb.Decimal]{
			id:      id,
			observe: observe,
		},
	}
}

func (a *AggregatableRequest) Copy() Request {
	// intentionally reuse the same instance, since it's thread safe and we need to get most recent captured observation
	return a
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
	observe func(context.Context, *ChainHeight) ([]byte, error)
}

func NewLockableToBlockRequest(id string, observe func(context.Context, *ChainHeight) ([]byte, error)) *LockableToBlockRequest {
	return &LockableToBlockRequest{
		id:      id,
		observe: observe,
	}
}

func (r *LockableToBlockRequest) Copy() Request {
	return &LockableToBlockRequest{
		id:      r.id,
		observe: r.observe,
	}
}

func (r *LockableToBlockRequest) ID() string {
	return r.id
}

func (r *LockableToBlockRequest) ToEventuallyConsistent(chainHeight *ChainHeight) *EventuallyConsistentRequest {
	return NewEventuallyConsistentRequest(r.id, func(ctx context.Context) ([]byte, error) {
		return r.observe(ctx, chainHeight)
	})
}
