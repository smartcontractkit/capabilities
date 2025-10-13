package oracle

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/metrics"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/libocr/quorumhelper"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

const InfoRequestID = "requestID"

const ReportMetaDataPrependLength = 109

type reportingPlugin struct {
	batchSize int
	store     *requests.Store[*ConsensusRequest]

	f int
	n int

	minimumObservations int

	// outcomeExpirySeqNrSpan is the duration, expressed as a seq number span, after which a request outcome will be pruned from the plugins outcome
	outcomeExpirySeqNrSpan uint64

	config  *ocrtypes.ReportingPluginConfig
	metrics *metrics.Metrics

	lggr logger.Logger
}

// NewReportingPlugin creates a new reporting plugin for the OCR3 capability
// historicalOutcomeExpirySeqNrSpan is the duration, expressed as a seq number span, after which a request outcome will be pruned
// from the plugins outcome
func NewReportingPlugin(lggr logger.Logger, metrics *metrics.Metrics, f int, n int, store *requests.Store[*ConsensusRequest],
	configProto *ocrtypes.ReportingPluginConfig, historicalOutcomeExpirySeqNrSpan uint64) (*reportingPlugin, error) {
	return &reportingPlugin{
		store:                  store,
		batchSize:              int(configProto.MaxBatchSize),
		f:                      f,
		n:                      n,
		minimumObservations:    2*f + 1,
		outcomeExpirySeqNrSpan: historicalOutcomeExpirySeqNrSpan,
		lggr:                   logger.Named(lggr, "CapabilityConsensusReportingPlugin"),
		config:                 configProto,
		metrics:                metrics,
	}, nil
}

func (r *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	allRequests, err := r.getAllRequests()
	if err != nil {
		return nil, fmt.Errorf("failed to get all requests: %w", err)
	}

	// Get only those requests that are pending consensus to prevent completed requests being included in the new query
	pendingRequests, err := r.getPendingRequests(outctx, allRequests)
	if err != nil {
		return nil, fmt.Errorf("failed to remove completed requests: %w", err)
	}

	// Take the first batchSize requests after filtering out completed requests
	if len(pendingRequests) > r.batchSize {
		pendingRequests = pendingRequests[:r.batchSize]
	}

	// To achieve a deterministic Outcome requires that each node has access to the same set of request observations, defaults and
	// consensus descriptors. Variations in the latter 2 would result in different nodes producing different outcomes.  As
	// the order of arrival of requests at each node is non-deterministic, relying on the request set at each node to provide
	// the consensus descriptors for a node would result in different nodes producing different outcomes for the same set
	// of observations.
	//
	// The solution to this problem is to embed the consensus descriptor and default into the query.  With this done, all nodes
	// will have access to the same consensus descriptor set and default when calculating the outcome.  One issue with this is that
	// it would allow the leader node to unduly influence the outcome by choosing which consensus descriptor and/or default to associate with
	// a request in the query.
	// To prevent this each node checks the consensus descriptor and default for a request in the query against the consensus descriptor
	// and default it has for the request and only contributes an observation for the request if they match.
	//
	// The same reasoning applies to the metadata for the request, which is also included in the query.

	seenIDs := make(map[IDKey]bool)
	cachedQuerySize := 0

	var reqs []*oracletypes.Request
	for _, rq := range pendingRequests {
		key := GetIDKey(rq)

		// Simple duplicate elimination using a map
		if seenIDs[key] {
			continue
		}

		serialisedConsensusDescriptor, err := proto.MarshalOptions{Deterministic: true}.Marshal(rq.Input.Descriptors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal consensus descriptor for request %s: %w", rq.ID(), err)
		}

		var serialisedDefault []byte
		if rq.Input.Default != nil {
			serialisedDefault, err = proto.MarshalOptions{Deterministic: true}.Marshal(rq.Input.Default)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal default for request %s: %w", rq.ID(), err)
			}
		}

		newReq := &oracletypes.Request{
			Metadata:                   ToRequestMetaData(rq.Metadata),
			RequestConsensusDescriptor: serialisedConsensusDescriptor,
			RequestDefault:             serialisedDefault,
		}

		// If the new id would exceed the max query size, stop adding more ids
		ok, newSize := BatchHasCapacity(cachedQuerySize, newReq, int(r.config.MaxQueryLengthBytes),
			func() { r.metrics.IncBatchRequestsTotal(ctx, "query") },
			func() { r.metrics.IncBatchCapacityExceeded(ctx, "query") })
		if !ok {
			break
		}

		seenIDs[key] = true
		reqs = append(reqs, newReq)
		cachedQuerySize = newSize
	}

	r.lggr.Debugw("consensus plugin query complete", "number of requests", len(reqs))
	return proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Query{
		Requests: reqs,
	})
}

// Removes any requests that have already been completed (successfully/failed/errored) from the batch
// leaving only those requests that are pending consensus.
func (r *reportingPlugin) getPendingRequests(outctx ocr3types.OutcomeContext, allRequests []*ConsensusRequest) ([]*ConsensusRequest, error) {
	var pendingRequests []*ConsensusRequest
	if outctx.PreviousOutcome == nil {
		return allRequests, nil
	}

	prevOutcome := &oracletypes.Outcome{}
	err := proto.Unmarshal(outctx.PreviousOutcome, prevOutcome)
	if err != nil {
		r.lggr.Errorw("could not unmarshal previous outcome", "error", err)
		return nil, err
	}

	// Remove any requests from the batch that are already in the previous outcome and not marked as pending
	// This ensures that requests that have been completed (whether successfully/failed/errored) are not included in the new query
	requestIDToHistoricalOutcome := make(map[string]*oracletypes.HistoricalRequestOutcome)
	for _, ro := range prevOutcome.HistoricalOutcomes {
		requestIDToHistoricalOutcome[ro.RequestId] = ro
	}

	for _, rq := range allRequests {
		previousRequestOutcome, exists := requestIDToHistoricalOutcome[rq.ID()]
		// If the request ID exists in the historical outcome and is not marked as pending, skip it
		if exists && previousRequestOutcome.Status != oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_PENDING {
			continue
		}

		pendingRequests = append(pendingRequests, rq)
	}

	return pendingRequests, nil
}

// TODO move this onto the store
func (r *reportingPlugin) getAllRequests() ([]*ConsensusRequest, error) {
	storeSize := r.store.Len()
	if storeSize == 0 {
		return nil, nil
	}

	// Get all pending requests
	batch, err := r.store.FirstN(storeSize)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch consensus requests from store: %w", err)
	}
	return batch, nil
}

func ToRequestMetaData(metadata ConsensusRequestMetadata) *oracletypes.RequestMetaData {
	return &oracletypes.RequestMetaData{
		RequestId:                metadata.RequestID(),
		WorkflowExecutionId:      metadata.WorkflowExecutionID,
		WorkflowStepReference:    metadata.ReferenceID,
		WorkflowId:               metadata.WorkflowID,
		WorkflowOwner:            metadata.WorkflowOwner,
		WorkflowName:             metadata.WorkflowName,
		WorkflowDonId:            metadata.WorkflowDonID,
		WorkflowDonConfigVersion: metadata.WorkflowDonConfigVersion,
		ReportId:                 metadata.ReportID,
		KeyBundleId:              metadata.KeyBundleID,
		RequestType:              metadata.RequestType,
	}
}

func (r *reportingPlugin) Observation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query) (types.Observation, error) {
	requestsQuery := &oracletypes.Query{}
	err := proto.Unmarshal(query, requestsQuery)
	if err != nil {
		return nil, err
	}

	var requestIDs []string
	for _, req := range requestsQuery.Requests {
		requestIDs = append(requestIDs, req.Metadata.RequestId)
	}

	reqIDToQueryRequest := map[string]*oracletypes.Request{}
	for _, req := range requestsQuery.Requests {
		reqIDToQueryRequest[req.Metadata.RequestId] = req
	}

	reqs := r.store.GetByIDs(requestIDs)

	// Observations for a request are only included if the consensus descriptor, metadata and default match those one in the query
	// to ensure that the leader node cannot unduly influence the outcome by choosing which consensus descriptor, default or metadata
	// to associate with a request.
	var requestObservations []*oracletypes.RequestObservation
	// Initialize cached size with the base message size
	obs := &oracletypes.Observation{Observations: make([]*oracletypes.RequestObservation, 0, len(reqs))}
	cachedObsSize := CalculateMessageSize(obs)

	for _, req := range reqs {
		queryRequest, ok := reqIDToQueryRequest[req.ID()]
		if !ok {
			return nil, fmt.Errorf("request %s not found in query", req.ID())
		}

		match, err := requestDescriptorMetadataAndDefaultMatch(r.lggr, req, queryRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to compare request and query for request %s: %w", req.ID(), err)
		}

		// If the consensus descriptor, metadata or default do not match that of the query skip this request
		if !match {
			// TODO - for DoS protection will mark the request as mismatched in subsequent PR
			continue
		}

		// Now we know the consensus descriptor, metadata and default match, we can include the observation (if it exists)
		var newOb *oracletypes.RequestObservation
		switch obs := req.Input.GetObservation().(type) {
		case *sdk.SimpleConsensusInputs_Value:
			marshalledValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(obs.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal observation value for request %s: %w", req.ID(), err)
			}

			newOb = &oracletypes.RequestObservation{
				Metadata:    queryRequest.Metadata,
				Observation: marshalledValue,
				ReceivedAt:  timestamppb.New(req.ReceivedAt),
			}
		case *sdk.SimpleConsensusInputs_Error:
			r.lggr.Debugw("observation is an error, skipping", "error", obs.Error, "requestID", req.ID())
			continue
		default:
			// Neither value nor error is set in the observation input, use the default if it exists
			if req.Input.Default != nil {
				serialisedDefault, err := proto.MarshalOptions{Deterministic: true}.Marshal(req.Input.Default)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal default value for request %s: %w", req.ID(), err)
				}

				newOb = &oracletypes.RequestObservation{
					Metadata:    queryRequest.Metadata,
					Observation: serialisedDefault,
					ReceivedAt:  timestamppb.New(req.ReceivedAt),
				}
			} else {
				r.lggr.Debugw("neither value, error or default is set in the observation input for request", "requestID", req.ID())
			}
		}

		if newOb != nil {
			ok, newSize := BatchHasCapacity(cachedObsSize, newOb, int(r.config.MaxObservationLengthBytes),
				func() { r.metrics.IncBatchRequestsTotal(ctx, "observation") },
				func() { r.metrics.IncBatchCapacityExceeded(ctx, "observation") })
			if !ok {
				break
			}

			requestObservations = append(requestObservations, newOb)
			cachedObsSize = newSize
		}
	}

	observation := &oracletypes.Observation{Observations: requestObservations}

	r.lggr.Debugw("consensus plugin observation complete", "numObservations", len(requestObservations), "numOfRequestsInQuery", len(requestsQuery.Requests))
	return proto.MarshalOptions{Deterministic: true}.Marshal(observation)
}

func requestDescriptorMetadataAndDefaultMatch(lggr logger.Logger, req *ConsensusRequest,
	queryRequest *oracletypes.Request) (bool, error) {
	serialisedConsensusDescriptor, err := proto.MarshalOptions{Deterministic: true}.Marshal(req.Input.Descriptors)
	if err != nil {
		return false, fmt.Errorf("failed to marshal consensus descriptor for request %s: %w", req.ID(), err)
	}

	if !bytes.Equal(queryRequest.RequestConsensusDescriptor, serialisedConsensusDescriptor) {
		lggr.Debugw("Consensus descriptor mismatch", "requestID", req.ID())
		return false, nil
	}

	serialisedRequestMetaData, err := proto.MarshalOptions{Deterministic: true}.Marshal(ToRequestMetaData(req.Metadata))
	if err != nil {
		return false, fmt.Errorf("failed to marshal request metadata for request %s: %w", req.ID(), err)
	}

	serialisedQueryRequestMetaData, err := proto.MarshalOptions{Deterministic: true}.Marshal(queryRequest.Metadata)
	if err != nil {
		return false, fmt.Errorf("failed to marshal query request metadata for request %s: %w", req.ID(), err)
	}

	if !bytes.Equal(serialisedRequestMetaData, serialisedQueryRequestMetaData) {
		lggr.Debugw("Metadata mismatch", "requestID", req.ID())
		return false, nil
	}

	if queryRequest.RequestDefault != nil {
		if req.Input.Default == nil {
			lggr.Debugw("Default value mismatch - query has default but request does not", "requestID", req.ID())
			return false, nil
		}

		serialisedDefault, err := proto.MarshalOptions{Deterministic: true}.Marshal(req.Input.Default)
		if err != nil {
			return false, fmt.Errorf("failed to marshal default for request %s: %w", req.ID(), err)
		}

		if !bytes.Equal(queryRequest.RequestDefault, serialisedDefault) {
			lggr.Debugw("Default value mismatch", "requestID", req.ID())
			return false, nil
		}
	} else {
		if req.Input.Default != nil {
			lggr.Debugw("Default value mismatch - request has default but query does not", "requestID", req.ID())
			return false, nil
		}
	}

	return true, nil
}

func (r *reportingPlugin) ValidateObservation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	return nil
}

func (r *reportingPlugin) ObservationQuorum(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (bool, error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumTwoFPlusOne, r.n, r.f, aos), nil
}

type timestampedObservation struct {
	Observation *valuespb.Value
	Timestamp   *timestamppb.Timestamp
}

func (r *reportingPlugin) Outcome(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, attributedObservations []types.AttributedObservation) (ocr3types.Outcome, error) {
	requestsQuery := &oracletypes.Query{}
	err := proto.Unmarshal(query, requestsQuery)
	if err != nil {
		return nil, err
	}

	historicalOutcomes, requestIDToHistoricalOutcome, err := getNonExpiredHistoricalRequestOutcomes(r.lggr, outctx, r.outcomeExpirySeqNrSpan)
	if err != nil {
		return nil, fmt.Errorf("failed to get previous outcomes: %w", err)
	}

	requestIDToObservations := groupAttributedObservationsByRequestID(r.lggr, attributedObservations)

	var outcomes []*oracletypes.RequestOutcome
	cachedOutcomeSize := CalculateMessageSize(&oracletypes.Outcome{Outcomes: outcomes, HistoricalOutcomes: historicalOutcomes})
	for _, request := range requestsQuery.Requests {
		requestID := request.Metadata.RequestId
		observations := requestIDToObservations[requestID]

		consensusDescriptor := &sdk.ConsensusDescriptor{}
		err = proto.Unmarshal(request.RequestConsensusDescriptor, consensusDescriptor)
		if err != nil {
			return nil, fmt.Errorf("could not unmarshal consensus descriptor for request %s: %w", requestID, err)
		}

		values := make([]*valuespb.Value, 0, len(observations))
		timestamps := make([]*timestamppb.Timestamp, 0, len(observations))
		for _, obs := range observations {
			if obs.Observation == nil || obs.Timestamp == nil {
				r.lggr.Errorw("observation or timestamp is nil for request, skipping", "requestID", requestID)
				continue
			}

			timestamps = append(timestamps, obs.Timestamp)
			values = append(values, obs.Observation)
		}

		// Get the default value from the query request if it exists
		var defaultValue *valuespb.Value
		if request.RequestDefault != nil {
			defaultValue = &valuespb.Value{}
			err := proto.Unmarshal(request.RequestDefault, defaultValue)
			if err != nil {
				return nil, fmt.Errorf("could not unmarshal default value for request %s: %w", requestID, err)
			}
		}

		var requestOutcome *oracletypes.RequestOutcome
		var historicalRequestOutcome *oracletypes.HistoricalRequestOutcome
		value, err := CalculateOutcomeForObservations(r.lggr, values, consensusDescriptor, defaultValue, r.minimumObservations, r.f)
		if err != nil {
			// TODO - pending this JIRA https://smartcontract-it.atlassian.net/browse/CAPPL-1076 mark the request as
			// pending so it is included in the next round. Subsequent PR for the latter JIRA will address better consensus failure and
			// error handling separately to avoid unnecessary consensus retries for the request and address DoS (+allow request to fail fast if consensus is not possible).
			r.lggr.Errorw("failed to calculate outcome for observations", "requestID", requestID, "error", err)
			historicalRequestOutcome = &oracletypes.HistoricalRequestOutcome{
				RequestId:        request.Metadata.RequestId,
				Status:           oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_PENDING,
				FirstSeenAtSeqNr: outctx.SeqNr,
			}
		} else {
			serialisedValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal outcome value for request %s: %w", requestID, err)
			}
			requestOutcome = &oracletypes.RequestOutcome{
				Metadata:  request.Metadata,
				Outcome:   serialisedValue,
				Timestamp: calculateMedianTimestamp(timestamps),
				Status:    oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS,
			}

			historicalRequestOutcome = &oracletypes.HistoricalRequestOutcome{
				RequestId:        request.Metadata.RequestId,
				Status:           oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS,
				FirstSeenAtSeqNr: outctx.SeqNr,
			}
		}

		if existingHistoricalRequestOutcome, ok := requestIDToHistoricalOutcome[requestID]; ok {
			// If the request already exists in the historical outcomes update the status only
			existingHistoricalRequestOutcome.Status = historicalRequestOutcome.Status
			historicalRequestOutcome = nil
		}

		hasCapacity, newOutcomeSize := r.checkOutcomeBatchHasCapacity(ctx, cachedOutcomeSize, requestOutcome, historicalRequestOutcome)
		if !hasCapacity {
			break
		}

		cachedOutcomeSize = newOutcomeSize

		if requestOutcome != nil {
			outcomes = append(outcomes, requestOutcome)
		}

		if historicalRequestOutcome != nil {
			historicalOutcomes = append(historicalOutcomes, historicalRequestOutcome)
		}
	}

	serialisedOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Outcome{
		Outcomes:           outcomes,
		HistoricalOutcomes: historicalOutcomes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outcome: %w", err)
	}

	return serialisedOutcome, nil
}

func (r *reportingPlugin) checkOutcomeBatchHasCapacity(ctx context.Context, existingOutcomeSize int, requestOutcome *oracletypes.RequestOutcome,
	historicalRequestOutcome *oracletypes.HistoricalRequestOutcome) (bool, int) {
	if requestOutcome != nil {
		ok, newSize := BatchHasCapacity(existingOutcomeSize, requestOutcome, int(r.config.MaxOutcomeLengthBytes),
			func() { r.metrics.IncBatchRequestsTotal(ctx, "outcome") },
			func() { r.metrics.IncBatchCapacityExceeded(ctx, "outcome") })

		if !ok {
			r.lggr.Debugw("max outcome batch size reached, skipping other requests", "requestID", requestOutcome.Metadata.RequestId)
			return false, 0
		}

		existingOutcomeSize = newSize
	}

	if historicalRequestOutcome != nil {
		ok, newSize := BatchHasCapacity(existingOutcomeSize, historicalRequestOutcome, int(r.config.MaxOutcomeLengthBytes),
			func() { r.metrics.IncBatchRequestsTotal(ctx, "outcome") },
			func() { r.metrics.IncBatchCapacityExceeded(ctx, "outcome") })

		if !ok {
			r.lggr.Debugw("max outcome batch size reached when adding historical request outcome, skipping other requests", "requestID", historicalRequestOutcome.RequestId)
			return false, 0
		}

		existingOutcomeSize = newSize
	}

	return true, existingOutcomeSize
}

func getNonExpiredHistoricalRequestOutcomes(lggr logger.Logger, outctx ocr3types.OutcomeContext, outcomeExpirySeqNrSpan uint64) ([]*oracletypes.HistoricalRequestOutcome, map[string]*oracletypes.HistoricalRequestOutcome, error) {
	var nonExpiredHistoricalOutcomes []*oracletypes.HistoricalRequestOutcome
	requestIDToHistoricalOutcome := map[string]*oracletypes.HistoricalRequestOutcome{}
	if outctx.PreviousOutcome != nil {
		prevOutcome := &oracletypes.Outcome{}
		err := proto.Unmarshal(outctx.PreviousOutcome, prevOutcome)
		if err != nil {
			lggr.Errorw("could not unmarshal previous outcome", "error", err)
			return nil, nil, err
		}

		for _, ho := range prevOutcome.HistoricalOutcomes {
			if outctx.SeqNr-ho.FirstSeenAtSeqNr <= outcomeExpirySeqNrSpan {
				nonExpiredHistoricalOutcomes = append(nonExpiredHistoricalOutcomes, ho)
				requestIDToHistoricalOutcome[ho.RequestId] = ho
			}
		}
	}

	return nonExpiredHistoricalOutcomes, requestIDToHistoricalOutcome, nil
}

func groupAttributedObservationsByRequestID(lggr logger.Logger, attributedObservations []types.AttributedObservation) map[string][]timestampedObservation {
	requestIDToObservations := make(map[string][]timestampedObservation)
	for _, ao := range attributedObservations {
		obs := &oracletypes.Observation{}
		err := proto.Unmarshal(ao.Observation, obs)
		if err != nil {
			lggr.Errorw("could not unmarshal observation from observer", "error", err, "observer", ao.Observer)
			continue
		}

		for _, requestObservation := range obs.Observations {
			requestID := requestObservation.Metadata.RequestId
			observationValue := &valuespb.Value{}
			err = proto.Unmarshal(requestObservation.Observation, observationValue)
			if err != nil {
				lggr.Errorw("could not unmarshal observation for request from observer", "error", err, "requestID", requestID, "observer", ao.Observer)
				continue
			}

			// Check the observation correctly marshals to a value to ensure it is a valid observation
			_, err = values.FromProto(observationValue)
			if err != nil {
				lggr.Errorw("could not convert observation value proto to value", "error", err, "requestID", requestID, "observer", ao.Observer)
				continue
			}

			requestIDToObservations[requestID] = append(requestIDToObservations[requestID], timestampedObservation{
				Observation: observationValue,
				Timestamp:   requestObservation.ReceivedAt,
			})
		}
	}
	return requestIDToObservations
}

func calculateMedianTimestamp(timestamps []*timestamppb.Timestamp) *timestamppb.Timestamp {
	slices.SortFunc(timestamps, func(a, b *timestamppb.Timestamp) int {
		if a.AsTime().Before(b.AsTime()) {
			return -1
		}
		if a.AsTime().After(b.AsTime()) {
			return 1
		}
		return 0
	})
	timestampCount := len(timestamps)
	mid := timestampCount / 2

	finalTimestamp := timestamps[mid]
	if timestampCount%2 != 1 {
		a := timestamps[mid-1].AsTime().Unix()
		b := timestamps[mid].AsTime().Unix()
		// a + (b-a) / 2 to avoid overflows
		finalTimestamp = timestamppb.New(time.Unix(a+(b-a)/2, 0))
	}
	return finalTimestamp
}

func (r *reportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	requestsOutcome := &oracletypes.Outcome{}
	err := proto.Unmarshal(outcome, requestsOutcome)
	if err != nil {
		return nil, err
	}

	var reports []ocr3types.ReportPlus[[]byte]

	for _, requestOutcome := range requestsOutcome.Outcomes {
		// TODO as part of https://smartcontract-it.atlassian.net/browse/CAPPL-1076
		// handle other status outcomes
		if requestOutcome.Status != oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS {
			r.lggr.Debugw("skipping report generation for request as outcome status is not success", "requestID", requestOutcome.Metadata.RequestId, "status", requestOutcome.Status.String())
			continue
		}

		reqMetadata := requestOutcome.Metadata
		var report []byte
		switch reqMetadata.RequestType {
		case oracletypes.RequestType_VALUE_CONSENSUS:
			report = requestOutcome.Outcome
		case oracletypes.RequestType_REPORT_GENERATION:
			// If the request type is report extract the report from the values.Value before signing it
			serialisedValue := requestOutcome.Outcome
			value := &valuespb.Value{}
			if err := proto.Unmarshal(serialisedValue, value); err != nil {
				return nil, fmt.Errorf("failed to unmarshal value for request %s: %w", reqMetadata.RequestId, err)
			}

			report = value.GetBytesValue()
			if report == nil {
				return nil, fmt.Errorf("failed to get report bytes for request %s", reqMetadata.RequestId)
			}
		}

		meta := ocrtypes.Metadata{
			Version:          1,
			ExecutionID:      reqMetadata.WorkflowExecutionId,
			Timestamp:        uint32(requestOutcome.Timestamp.AsTime().Unix()), // nolint
			DONID:            reqMetadata.WorkflowDonId,
			DONConfigVersion: reqMetadata.WorkflowDonConfigVersion,
			WorkflowID:       reqMetadata.WorkflowId,
			WorkflowName:     reqMetadata.WorkflowName,
			WorkflowOwner:    reqMetadata.WorkflowOwner,
			ReportID:         reqMetadata.ReportId,
		}

		metadataPrepend, err := meta.Encode()
		if err != nil {
			return nil, fmt.Errorf("failed to encode metadata for request %s: %w", reqMetadata.RequestId, err)
		}

		reportWithMetaData := append(metadataPrepend, report...)

		info, err := createReportInfo(reqMetadata)
		if err != nil {
			return nil, fmt.Errorf("failed to create report info for request %s: %w", reqMetadata.RequestId, err)
		}

		reports = append(reports, ocr3types.ReportPlus[[]byte]{
			ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
				Report: reportWithMetaData,
				Info:   info,
			},
			TransmissionScheduleOverride: nil,
		})
	}

	r.lggr.Debug("consensus plugin reports complete, number of reports", len(reports))
	return reports, nil
}

// The report info is created as a map else the OCR3OnchainKeyringMultiChainAdapter will not work.
// OCR3OnchainKeyringMultiChainAdapter (in core) requires that the key bundle id is added to the map with the key
// "keyBundleName"
func createReportInfo(reqMetadata *oracletypes.RequestMetaData) ([]byte, error) {
	infos, err := structpb.NewStruct(map[string]any{
		"keyBundleName": reqMetadata.KeyBundleId,
		InfoRequestID:   reqMetadata.RequestId,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create structpb for report info: %w", err)
	}

	infoBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(infos)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal report info: %w", err)
	}

	return infoBytes, nil
}

func (r *reportingPlugin) ShouldAcceptAttestedReport(ctx context.Context, seqNr uint64, rwi ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	// True because we always want to transmit a report
	return true, nil
}

func (r *reportingPlugin) ShouldTransmitAcceptedReport(ctx context.Context, seqNr uint64, rwi ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	// True because we always want to transmit a report
	return true, nil
}

func (r *reportingPlugin) Close() error {
	return nil
}
