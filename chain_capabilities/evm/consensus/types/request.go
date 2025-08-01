package types

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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

const (
	AggregationMethodFPlusOneHighest = "f+1-highest"
)

var _ ObservableRequest = (*AggregatableRequest)(nil)

type AggregatableRequest struct {
	*observableRequest[*AggregatableObservation]
}

func NewAggregatableRequest(id string, observe func(context.Context) (*AggregatableObservation, error)) *AggregatableRequest {
	return &AggregatableRequest{
		observableRequest: &observableRequest[*AggregatableObservation]{
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
	err               ObservationError
	observationExists bool
	observationMutex  sync.Mutex
	observe           func(context.Context) (T, error)
}

func (r *observableRequest[T]) ID() string {
	return r.id
}

func (r *observableRequest[T]) CaptureObservation(ctx context.Context) error {
	// always prefer latest observation even if it's not successful, as it might be possible that error is not transient
	// and all nodes will converge to it.
	observation, err := r.observe(ctx)
	r.observationMutex.Lock()
	defer r.observationMutex.Unlock()
	if err != nil {
		obErr, conversionErr := NewObservationError(err)
		if conversionErr != nil {
			return errors.Join(err, fmt.Errorf("failed to convert error to ObservationError: %w", conversionErr))
		}

		r.err = obErr
		var zero T
		r.observation = zero
	} else {
		r.err = nil
		r.observation = observation
	}

	r.observationExists = true
	return err
}

func (r *observableRequest[T]) GetObservation() (T, ObservationError, bool) {
	r.observationMutex.Lock()
	defer r.observationMutex.Unlock()
	return r.observation, r.err, r.observationExists
}

// SetObservation - sets observation. Should be used only for tests
func (r *observableRequest[T]) SetObservation(observation T) {
	r.observationMutex.Lock()
	defer r.observationMutex.Unlock()
	r.observation = observation
	r.observationExists = true
}

// TODO PLEX-1626: test observation error
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

type Reply struct {
	Value any
	Err   error
}

type ObservationError []byte

func NewObservationError(err error) (ObservationError, error) {
	transmittableErr := status.Convert(err)
	return proto.Marshal(transmittableErr.Proto())
}

func (o ObservationError) Err() error {
	if len(o) == 0 {
		return nil
	}

	transmittableErr := new(spb.Status)
	err := proto.Unmarshal(o, transmittableErr)
	if err != nil {
		return fmt.Errorf("failed to unmarshal ObservationError: %w. Original error: %s", err, hex.EncodeToString(o))
	}

	return status.FromProto(transmittableErr).Err()
}
