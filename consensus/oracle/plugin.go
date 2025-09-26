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

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/libocr/quorumhelper"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
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

	config *ocrtypes.ReportingPluginConfig

	lggr logger.Logger
}

func NewReportingPlugin(lggr logger.Logger, f int, n int, store *requests.Store[*ConsensusRequest], configProto *ocrtypes.ReportingPluginConfig) (*reportingPlugin, error) {
	return &reportingPlugin{
		store:               store,
		batchSize:           int(configProto.MaxBatchSize),
		f:                   f,
		n:                   n,
		minimumObservations: 2*f + 1,
		lggr:                logger.Named(lggr, "CapabilityConsensusReportingPlugin"),
		config:              configProto,
	}, nil
}

func (r *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	batch, err := r.store.FirstN(r.batchSize)
	if err != nil {
		r.lggr.Errorw("could not retrieve batch", "error", err)
		return nil, err
	}

	// To achieve a deterministic Outcome requires that each node has access to the same set of request observations AND
	// consensus descriptors. Variations in the latter would result in different nodes producing different outcomes.  As
	// the order of arrival of requests at each node is non-deterministic, relying on the request set at each node to provide
	// the consensus descriptors for a node would result in different nodes producing different outcomes for the same set
	// of observations.
	//
	// The solution to this problem is to embed the consensus descriptor in the query.  With this done, all nodes
	// will have access to the same consensus descriptor set when calculating the outcome.  One issue with this is that
	// it would allow the leader node to unduly influence the outcome by choosing which consensus descriptor to associate with
	// a request in the query.
	// To prevent this each node checks the consensus descriptor for a request in the query against the consensus descriptor
	// it has for the request and only contributes an observation for the request if the consensus descriptor matches.
	//
	// The same reasoning applies to the metadata for the request, which is also included in the query.

	seenIDs := make(map[IDKey]bool)
	cachedQuerySize := 0

	var reqs []*oracletypes.Request
	for _, rq := range batch {
		key := GetIDKey(rq)

		// Simple duplicate elimination using a map
		if seenIDs[key] {
			continue
		}

		serialisedConsensusDescriptor, err := proto.MarshalOptions{Deterministic: true}.Marshal(rq.Input.Descriptors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal consensus descriptor for request %s: %w", rq.ID(), err)
		}

		newReq := &oracletypes.Request{
			Metadata:                   ToRequestMetaData(rq.Metadata),
			RequestConsensusDescriptor: serialisedConsensusDescriptor,
		}

		// If the new id would exceed the max query size, stop adding more ids
		ok, newSize := BatchHasCapacity(cachedQuerySize, newReq, int(r.config.MaxQueryLengthBytes))
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

	// Observations for a request are only included if the consensus descriptor and metadata match that one in the query
	// to ensure that the leader node cannot unduly influence the outcome by choosing which consensus descriptor to associate with a request
	// or what metadata to associate with a request.
	var requestObservations []*oracletypes.RequestObservation
	// Initialize cached size with the base message size
	obs := &oracletypes.Observation{Observations: make([]*oracletypes.RequestObservation, 0, len(reqs))}
	cachedObsSize := CalculateMessageSize(obs)

	for _, req := range reqs {
		queryRequest, ok := reqIDToQueryRequest[req.ID()]
		if !ok {
			return nil, fmt.Errorf("request %s not found in query", req.ID())
		}

		serialisedConsensusDescriptor, err := proto.MarshalOptions{Deterministic: true}.Marshal(req.Input.Descriptors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal consensus descriptor for request %s: %w", req.ID(), err)
		}

		if !bytes.Equal(queryRequest.RequestConsensusDescriptor, serialisedConsensusDescriptor) {
			r.lggr.Debugw("Consensus descriptor mismatch", "requestID", req.ID())
			continue // Skip this request as the consensus descriptor does not match
		}

		serialisedRequestMetaData, err := proto.MarshalOptions{Deterministic: true}.Marshal(ToRequestMetaData(req.Metadata))
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request metadata for request %s: %w", req.ID(), err)
		}

		serialisedQueryRequestMetaData, err := proto.MarshalOptions{Deterministic: true}.Marshal(queryRequest.Metadata)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal query request metadata for request %s: %w", req.ID(), err)
		}

		if !bytes.Equal(serialisedRequestMetaData, serialisedQueryRequestMetaData) {
			r.lggr.Debugw("Metadata mismatch", "requestID", req.ID())
			continue // Skip this request as the metadata does not match
		}

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
			ok, newSize := BatchHasCapacity(cachedObsSize, newOb, int(r.config.MaxObservationLengthBytes))
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

	// Group attributed observations by request ID.
	requestIDToObservations := make(map[string][]timestampedObservation)
	for _, ao := range attributedObservations {
		obs := &oracletypes.Observation{}
		err := proto.Unmarshal(ao.Observation, obs)
		if err != nil {
			r.lggr.Errorw("could not unmarshal observation from observer", "error", err, "observer", ao.Observer)
			continue
		}

		for _, requestObservation := range obs.Observations {
			requestID := requestObservation.Metadata.RequestId
			observationValue := &valuespb.Value{}
			err := proto.Unmarshal(requestObservation.Observation, observationValue)
			if err != nil {
				r.lggr.Errorw("could not unmarshal observation for request from observer", "error", err, "requestID", requestID, "observer", ao.Observer)
				continue
			}

			// Check the observation correctly marshals to a value to ensure it is a valid observation
			_, err = values.FromProto(observationValue)
			if err != nil {
				r.lggr.Errorw("could not convert observation value proto to value", "error", err, "requestID", requestID, "observer", ao.Observer)
				continue
			}

			requestIDToObservations[requestID] = append(requestIDToObservations[requestID], timestampedObservation{
				Observation: observationValue,
				Timestamp:   requestObservation.ReceivedAt,
			})
		}
	}

	var outcomes []*oracletypes.RequestOutcome
	cachedOutcomeSize := CalculateMessageSize(&oracletypes.Outcome{Outcomes: outcomes})
	for _, request := range requestsQuery.Requests {
		requestID := request.Metadata.RequestId
		observations := requestIDToObservations[requestID]
		if len(observations) < r.minimumObservations {
			r.lggr.Debugw("insufficient observations for request", "requestID", requestID, "numObservations", len(observations))
			continue
		}

		consensusDescriptor := &sdk.ConsensusDescriptor{}
		err := proto.Unmarshal(request.RequestConsensusDescriptor, consensusDescriptor)
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

		// Retrieve the original ConsensusRequest from the store to get the default value
		var defaultValue *valuespb.Value
		if reqs := r.store.GetByIDs([]string{requestID}); len(reqs) == 1 {
			originalRequest := reqs[0]
			if originalRequest != nil && originalRequest.Input != nil {
				defaultValue = originalRequest.Input.GetDefault()
			}
		}

		value, err := CalculateOutcomeForObservations(r.lggr, values, consensusDescriptor, defaultValue, r.minimumObservations, r.f)
		if err != nil {
			// TODO - should the err from CalculateOutcomeForObservations need to be distinguishable between a consensus failure and an error?
			r.lggr.Errorw("failed to calculate outcome for observations", "requestID", requestID, "error", err)
			continue
		}

		serialisedValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal outcome value for request %s: %w", requestID, err)
		}

		newRequestOutcome := &oracletypes.RequestOutcome{
			Metadata:  request.Metadata,
			Outcome:   serialisedValue,
			Timestamp: calculateMedianTimestamp(timestamps),
		}

		ok, newSize := BatchHasCapacity(cachedOutcomeSize, newRequestOutcome, int(r.config.MaxOutcomeLengthBytes))
		if !ok {
			break
		}

		outcomes = append(outcomes, newRequestOutcome)
		cachedOutcomeSize = newSize
	}

	serialisedOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Outcome{
		Outcomes: outcomes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outcome: %w", err)
	}

	return serialisedOutcome, nil
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
