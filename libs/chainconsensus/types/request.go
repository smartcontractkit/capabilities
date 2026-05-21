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

// EventuallyConsistentRequest is a request type whose observation is inconsistent across multiple RPCs for a short
// period of time due to reorgs or delays in state propagation. The probability that any two honest nodes will observe
// the same value increases with time. Example: The receipt becomes available and has the same result.
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

// AggregatableRequest is a request type whose observation can be aggregated across multiple nodes using an
// aggregation method (e.g. f+1 highest, median, etc.) to achieve consensus on the observed value and the aggregated result
// is acceptable for a user. Example: Estimate Gas.
// This method must not be used for requests like "get balance", as it can produce results that were never observed
// on chain, due to malicious nodes. Example: [10, 20, 30, 30], with f+1 highest, the result will be 20, which was never observed on chain.
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

// LockableToBlockRequest - is a request type, where the observation can be captured at a specific block height.
// The observation may be volatile, but it will be the same across all honest nodes if captured at the same block height.
// Example: Get Block.
// Consensus occurs in two steps:
// 1. Nodes agree on a block height to lock to (e.g. 100).
// 2. Nodes lock request to the block height and treat it as eventually consistent.
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
// HashableRequest is newer version of EventuallyConsistentRequest.
// It should be used when the observation can be large and we want to avoid transmitting it multiple times for the same request,
// by transmitting only the hash of the observation.
type HashableRequest[T proto.Message] struct {
	workflowExecutionID string
	reference           string
	metadata            commoncap.ResponseMetadata
	*observableRequest[T]
	observations map[Hash]T
	obsLock      sync.RWMutex
}

func NewHashableRequest[T proto.Message](workflowExecutionID, reference string, metadata commoncap.ResponseMetadata, observe func(context.Context) (T, error)) *HashableRequest[T] {
	return &HashableRequest[T]{
		workflowExecutionID: workflowExecutionID,
		reference:           reference,
		metadata:            metadata,
		observations:        make(map[Hash]T),
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

var errNoObservation = errors.New("no observation captured yet")

func (r *HashableRequest[T]) captureObservationHash() (Hash, ObservationError, error) {
	observation, obErr, ok := r.GetObservation()
	if !ok {
		return Hash{}, nil, errNoObservation
	}

	if obErr != nil {
		return Hash{}, obErr, nil
	}

	reportData, err := reportDataForObservation(r.workflowExecutionID, r.reference, r.metadata, observation)
	if err != nil {
		return Hash{}, nil, err
	}
	r.obsLock.Lock()
	defer r.obsLock.Unlock()
	// store observation by report data hash to be able to retrieve it later when report is generated.
	// As there is a race between OCR plugin and capturing observations, we have to store all of them, as we don't know which one will be used in the report.
	// There is no eviction of old observations as we expect only a few of them to be captured during the lifetime of the request,
	// and they will be removed when the report is generated or timeout occurs.
	r.observations[reportData] = observation
	return reportData, nil, nil
}

func (r *HashableRequest[T]) GetOCRObservation() (*RequestObservation, error) {
	hash, obErr, err := r.captureObservationHash()
	if err != nil {
		if errors.Is(err, errNoObservation) {
			return nil, nil
		}
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

func (r *HashableRequest[T]) GetObservationByReportData(reportData Hash) (T, bool) {
	r.obsLock.RLock()
	defer r.obsLock.RUnlock()
	result, ok := r.observations[reportData]
	return result, ok
}

func (r *HashableRequest[T]) GetMetadata() commoncap.ResponseMetadata {
	return r.metadata
}

// LockableToBlockHashableRequest - is a request type, which combines properties of LockableToBlockRequest and HashableRequest.
// It allows to capture observation at a specific block height and hash the observation for hash-based consensus.
type LockableToBlockHashableRequest[T proto.Message] struct {
	id                  string
	workflowExecutionID string
	reference           string
	metadata            commoncap.ResponseMetadata
	observe             func(context.Context, *ChainHeight) (T, error)
	hashableReqLock     sync.RWMutex
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

// Copy returns the same instance of the request, as it's thread safe and we want to reuse hashable request.
func (r *LockableToBlockHashableRequest[T]) Copy() Request { return r }

func (r *LockableToBlockHashableRequest[T]) ID() string {
	return r.id
}

func (r *LockableToBlockHashableRequest[T]) LockToABlock(chainHeight *ChainHeight) Request {
	r.hashableReqLock.Lock()
	defer r.hashableReqLock.Unlock()
	if r.hashableRequest == nil {
		r.hashableRequest = NewHashableRequest(r.workflowExecutionID, r.reference, r.metadata, func(ctx context.Context) (T, error) {
			return r.observe(ctx, chainHeight)
		})
	}
	return r.hashableRequest
}

const HashLength = 32

type Hash [HashLength]byte

func (r *LockableToBlockHashableRequest[T]) GetObservationByReportData(reportData Hash) (T, bool) {
	r.hashableReqLock.RLock()
	defer r.hashableReqLock.RUnlock()
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
