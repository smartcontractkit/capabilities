package types

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// MaxNumberOfVolatileObservations defines a hard cap on the number of unique observations retained for a VolatileRequest to prevent unbounded memory growth.
// It's additionally enforced by the OCR protocol layer which will reject any VolatileRequestObservation with more than this number of observations.
// Actual max number of distinct observations is defined by request timeout and block rate of target chain.
// This number should be high enough to allow for all observations to be retained during the request lifecycle (even in case of changes of timeout or polling changes),
// but low enough to prevent unbounded memory growth in case of misconfiguration or CPU starvation by a malicious OCR node.
// It's not configurable as odds of needing to change it are very low, and it adds complexity to configuration and testing.
// NOTE: exceeding this limit during CaptureObservation will cause an error and newest observation won't be included, as moving
// observation window does not make sense by design. We expect to reach quorum once lagging nodes observe block that faster nodes observed during first polls.
// Moving observation window contradicts that expectation.
const MaxNumberOfVolatileObservations = 1000

// volatileObservationEntry stores a successful observation and the highest block height seen for its report-data hash.
type volatileObservationEntry[T proto.Message] struct {
	observation T
	height      uint64
}

// VolatileRequest is an observable request for observations that change frequently and cannot be locked to a block.
// It retains all unique observations keyed by the same report-data hash as ECHashableRequest, keeping the maximum height
// per hash, the latest capture error, and exposes hashes (not aggregated payloads) for OCR.
// Assumes that all errors are transient or eventually consistent, and thus clears the latest error on a successful observation.
type VolatileRequest[T proto.Message] struct {
	workflowExecutionID string
	reference           string
	metadata            commoncap.ResponseMetadata
	id                  string
	observe             func(context.Context) (T, uint64, error)
	mu                  sync.RWMutex
	observations        map[Hash]volatileObservationEntry[T]
	latestErr           ObservationError
	lggr                logger.SugaredLogger
}

func NewVolatileRequest[T proto.Message](
	workflowExecutionID, reference string,
	metadata commoncap.ResponseMetadata,
	observe func(context.Context) (T, uint64, error),
	lggr logger.SugaredLogger,
) *VolatileRequest[T] {
	return &VolatileRequest[T]{
		workflowExecutionID: workflowExecutionID,
		reference:           reference,
		metadata:            metadata,
		id:                  commonMon.RequestID(workflowExecutionID, reference),
		observe:             observe,
		observations:        make(map[Hash]volatileObservationEntry[T]),
		lggr:                lggr,
	}
}

var _ ObservableRequest = (*VolatileRequest[*emptypb.Empty])(nil)
var _ HashableRequest[*emptypb.Empty] = (*VolatileRequest[*emptypb.Empty])(nil)

// ID returns the request identifier derived from workflow execution ID and reference.
func (r *VolatileRequest[T]) ID() string {
	return r.id
}

// Copy intentionally reuses the same instance; the request is safe for concurrent use and callers need the latest state.
func (r *VolatileRequest[T]) Copy() Request {
	return r
}

func (r *VolatileRequest[T]) GetMetadata() commoncap.ResponseMetadata {
	return r.metadata
}

func reportDataForObservation[T proto.Message](
	workflowExecutionID, reference string,
	metadata commoncap.ResponseMetadata,
	observation T,
) (Hash, error) {
	rawPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(observation)
	if err != nil {
		return Hash{}, fmt.Errorf("failed to marshal observation: %w", err)
	}
	reportData, err := commoncap.ResponseToReportData(workflowExecutionID, reference, rawPayload, metadata)
	if err != nil {
		return Hash{}, fmt.Errorf("failed to convert response to report data: %w", err)
	}
	return reportData, nil
}

// CaptureObservation runs the observe callback and merges the result into stored observations or updates latest error.
func (r *VolatileRequest[T]) CaptureObservation(ctx context.Context) error {
	observation, height, err := r.observe(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()

	if err != nil {
		obErr, conversionErr := NewObservationError(err)
		if conversionErr != nil {
			return errors.Join(err, fmt.Errorf("failed to convert error to ObservationError: %w", conversionErr))
		}
		r.latestErr = obErr
		return err
	}

	reportData, err := reportDataForObservation(r.workflowExecutionID, r.reference, r.metadata, observation)
	if err != nil {
		return err
	}

	r.latestErr = nil // clear latest error on success, as we assume that all errors are transient or eventually consistent.

	if existing, ok := r.observations[reportData]; ok {
		existing.height = max(existing.height, height)
		r.observations[reportData] = existing
	} else {
		if len(r.observations) >= MaxNumberOfVolatileObservations {
			err = fmt.Errorf("cannot capture observation for report data %x: max number of unique observations reached", reportData)
			r.lggr.Criticalw("Request captured too many observations. This should never occur in production. Most likely something is wrong with RPC/Polling Period/Request Timeout. "+
				"If you see this log, reach out to Chainlink team", "err", err)
			return err
		}
		r.observations[reportData] = volatileObservationEntry[T]{
			observation: observation,
			height:      height,
		}
	}

	return nil
}

// GetOCRObservation returns a volatile observation bundle when there is at least one stored success or a latest error.
func (r *VolatileRequest[T]) GetOCRObservation() (*RequestObservation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hasErr := len(r.latestErr) > 0
	hasObs := len(r.observations) > 0
	if !hasErr && !hasObs {
		return nil, nil
	}

	vo := &VolatileObservations{
		Error: r.latestErr,
	}

	if !hasObs {
		return &RequestObservation{
			Observation: &RequestObservation_Volatile{Volatile: vo},
		}, nil
	}

	keys := make([]Hash, 0, len(r.observations))
	for k := range r.observations {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return bytes.Compare(keys[i][:], keys[j][:]) < 0
	})
	vo.Observations = make([]*VolatileObservation, 0, len(keys))
	for _, key := range keys {
		ent := r.observations[key]
		vo.Observations = append(vo.Observations, &VolatileObservation{
			Height: ent.height,
			Hash:   key[:],
		})
	}

	return &RequestObservation{
		Observation: &RequestObservation_Volatile{Volatile: vo},
	}, nil
}

// GetObservationByReportData returns the proto observation for a previously captured report-data hash, if present.
func (r *VolatileRequest[T]) GetObservationByReportData(reportData Hash) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ent, ok := r.observations[reportData]
	if !ok {
		var zero T
		return zero, false
	}
	return ent.observation, true
}
