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
	"google.golang.org/protobuf/types/known/emptypb"

	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

type RequestType int

type Request interface {
	ID() string
	Copy() Request
	GetOCRObservation() (*RequestObservation, error)
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

func (r *EventuallyConsistentRequest) GetOCRObservation() (*RequestObservation, error) {
	requestOb, observationErr, ok := r.GetObservation()
	if !ok {
		return nil, nil
	}
	if observationErr != nil {
		return &RequestObservation{
			Observation: &RequestObservation_Error{Error: observationErr},
		}, nil
	}
	return &RequestObservation{
		Observation: &RequestObservation_EventuallyConsistent{EventuallyConsistent: requestOb},
	}, nil
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

func (a *AggregatableRequest) GetOCRObservation() (*RequestObservation, error) {
	requestOb, observationErr, ok := a.GetObservation()
	if !ok {
		return nil, nil
	}
	if observationErr != nil {
		return &RequestObservation{
			Observation: &RequestObservation_Error{Error: observationErr},
		}, nil
	}
	return &RequestObservation{
		Observation: &RequestObservation_Aggregatable{Aggregatable: requestOb},
	}, nil
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

func (r *LockableToBlockRequest) GetOCRObservation() (*RequestObservation, error) {
	return &RequestObservation{
		Observation: &RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
	}, nil
}

func (r *LockableToBlockRequest) LockToABlock(chainHeight *ChainHeight) Request {
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

// HashableRequest is an observable request, whose payload can be hashed to be used in hash-based consensus.
// It is the responsibility of the caller to ensure that the payload is deterministic and does not contain any non-deterministic data (e.g. timestamps, random values, etc.)
// that can cause different nodes to have different hashes for the same request.
type HashableRequest[T proto.Message] struct {
	workflowExecutionID string
	reference           string
	metadata            commoncap.ResponseMetadata
	*observableRequest[T]
	observations map[[HashLength]byte]T
	lock         sync.RWMutex
}

func NewHashableRequest[T proto.Message](workflowExecutionID, reference string, metadata commoncap.ResponseMetadata, observe func(context.Context) (T, error)) *HashableRequest[T] {
	return &HashableRequest[T]{
		workflowExecutionID: workflowExecutionID,
		reference:           reference,
		metadata:            metadata,
		observations:        make(map[[HashLength]byte]T),
		observableRequest: &observableRequest[T]{
			id:      commonMon.RequestID(workflowExecutionID, reference),
			observe: observe,
		},
	}
}

func (r *HashableRequest[T]) Copy() Request {
	// intentionally reuse the same instance, since it's thread safe, and we need to get most recent captured observation
	return r
}

var ErrNoObservation = errors.New("no observation captured yet")

func (r *HashableRequest[T]) getObservationHash() ([HashLength]byte, ObservationError, error) {
	observation, obErr, ok := r.GetObservation()
	if !ok {
		return [HashLength]byte{}, obErr, ErrNoObservation
	}

	if obErr != nil {
		return [HashLength]byte{}, obErr, nil
	}

	rawPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(observation)
	if err != nil {
		return [HashLength]byte{}, nil, fmt.Errorf("failed to marshal observation: %w", err)
	}

	reportData, err := commoncap.ResponseToReportData(r.workflowExecutionID, r.reference, rawPayload, r.metadata)
	if err != nil {
		return [HashLength]byte{}, nil, fmt.Errorf("failed to convert response to report data: %w", err)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	r.observations[reportData] = observation
	return reportData, nil, nil
}

func (r *HashableRequest[T]) GetOCRObservation() (*RequestObservation, error) {
	hash, obErr, err := r.getObservationHash()
	if err != nil {
		return nil, err
	}
	if obErr != nil {
		return &RequestObservation{
			Observation: &RequestObservation_Error{Error: obErr},
		}, nil
	}
	return &RequestObservation{
		Observation: &RequestObservation_Hashable{Hashable: hash[:]},
	}, nil
}

func (r *HashableRequest[T]) GetObservationByReportData(reportData [HashLength]byte) (T, bool) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	result, ok := r.observations[reportData]
	return result, ok
}

func (r *HashableRequest[T]) GetMetadata() commoncap.ResponseMetadata {
	return r.metadata
}

type LockableToBlockHashableRequest[T proto.Message] struct {
	id                  string
	workflowExecutionID string
	reference           string
	metadata            commoncap.ResponseMetadata
	observe             func(context.Context, *ChainHeight) (T, error)
	lock                sync.RWMutex
	hashableRequest     *HashableRequest[T]
}

func NewLockableToBlockHashableRequest[T proto.Message](workflowExecutionID, reference string, metadata commoncap.ResponseMetadata, observe func(context.Context, *ChainHeight) (T, error)) *LockableToBlockHashableRequest[T] {
	return &LockableToBlockHashableRequest[T]{
		id:                  commonMon.RequestID(workflowExecutionID, reference),
		workflowExecutionID: workflowExecutionID,
		reference:           reference,
		metadata:            metadata,
		observe:             observe,
	}
}

func (r *LockableToBlockHashableRequest[T]) Copy() Request {
	return &LockableToBlockHashableRequest[T]{
		id:                  r.id,
		workflowExecutionID: r.workflowExecutionID,
		reference:           r.reference,
		metadata:            r.metadata,
		observe:             r.observe,
	}
}

func (r *LockableToBlockHashableRequest[T]) ID() string {
	return r.id
}

func (r *LockableToBlockHashableRequest[T]) LockToABlock(chainHeight *ChainHeight) Request {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.hashableRequest == nil {
		r.hashableRequest = NewHashableRequest(r.workflowExecutionID, r.reference, r.metadata, func(ctx context.Context) (T, error) {
			return r.observe(ctx, chainHeight)
		})
	}
	return r.hashableRequest
}

const HashLength = 32

func (r *LockableToBlockHashableRequest[T]) GetObservationByReportData(reportData [HashLength]byte) (T, bool) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	if r.hashableRequest == nil {
		var zero T
		return zero, false
	}

	return r.hashableRequest.GetObservationByReportData(reportData)
}

func (r *LockableToBlockHashableRequest[T]) GetOCRObservation() (*RequestObservation, error) {
	return &RequestObservation{
		Observation: &RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
	}, nil
}

func (r *LockableToBlockHashableRequest[T]) GetMetadata() commoncap.ResponseMetadata {
	return r.metadata
}
