package oracle

import (
	"bytes"
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
	"github.com/smartcontractkit/libocr/quorumhelper"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type reportingPlugin struct {
	batchSize int
	s         *requests.Store[*ConsensusRequest]

	f int
	n int

	minimumObservations int

	lggr logger.Logger
}

func NewReportingPlugin(lggr logger.Logger, f int, n int, s *requests.Store[*ConsensusRequest], batchSize int) (*reportingPlugin, error) {
	return &reportingPlugin{
		s:                   s,
		batchSize:           batchSize,
		f:                   f,
		n:                   n,
		minimumObservations: 2*f + 1,
		lggr:                logger.Named(lggr, "CapabilityConsensusReportingPlugin"),
	}, nil
}

func (r *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	batch, err := r.s.FirstN(r.batchSize)
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

	var reqs []*oracletypes.Request
	for _, rq := range batch {
		serialisedConsensusDescriptor, err := proto.MarshalOptions{Deterministic: true}.Marshal(rq.input.Descriptors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal consensus descriptor for request %s: %w", rq.ID(), err)
		}

		reqs = append(reqs, &oracletypes.Request{
			Metadata: &oracletypes.RequestMetaData{
				RequestId:                rq.ID(),
				WorkflowExecutionId:      rq.Metadata.WorkflowExecutionID,
				WorkflowStepReference:    rq.Metadata.ReferenceID,
				WorkflowId:               rq.Metadata.WorkflowID,
				WorkflowOwner:            rq.Metadata.WorkflowOwner,
				WorkflowName:             rq.Metadata.WorkflowName,
				WorkflowDonId:            rq.Metadata.WorkflowDonID,
				WorkflowDonConfigVersion: rq.Metadata.WorkflowDonConfigVersion,
				KeyBundleId:              rq.KeyBundleID,
			},
			RequestConsensusDescriptor: serialisedConsensusDescriptor,
		})
	}

	r.lggr.Debugw("Query complete", "number of requests", len(reqs))
	return proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Query{
		Requests: reqs,
	})
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

	reqs := r.s.GetByIDs(requestIDs)

	// Observations for a request are only included if the consensus descriptor matches the one in the query
	// to ensure that the leader node cannot unduly influence the outcome by choosing which consensus descriptor to associate with a request.
	var requestObservations []*oracletypes.RequestObservation
	for _, req := range reqs {
		queryRequest, ok := reqIDToQueryRequest[req.ID()]
		if !ok {
			return nil, fmt.Errorf("request %s not found in query", req.ID())
		}

		serialisedConsensusDescriptor, err := proto.MarshalOptions{Deterministic: true}.Marshal(req.input.Descriptors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal consensus descriptor for request %s: %w", req.ID(), err)
		}

		if !bytes.Equal(queryRequest.RequestConsensusDescriptor, serialisedConsensusDescriptor) {
			r.lggr.Debugw("Consensus descriptor mismatch", "requestID", req.ID())
			continue // Skip this request as the consensus descriptor does not match
		}

		switch obs := req.input.GetObservation().(type) {
		case *pb.SimpleConsensusInputs_Value:
			marshalledValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(obs.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal observation value for request %s: %w", req.ID(), err)
			}
			requestObservations = append(requestObservations, &oracletypes.RequestObservation{
				Metadata:    queryRequest.Metadata,
				Observation: marshalledValue,
			})
		case *pb.SimpleConsensusInputs_Error:
			r.lggr.Debugw("observation is an error, skipping", "error", obs.Error, "requestID", req.ID())
		default:
			// Neither value nor error is set in the observation input, use the default if it exists
			if req.input.Default != nil {
				serialisedDefault, err := proto.MarshalOptions{Deterministic: true}.Marshal(req.input.Default)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal default value for request %s: %w", req.ID(), err)
				}
				requestObservations = append(requestObservations, &oracletypes.RequestObservation{
					Metadata:    queryRequest.Metadata,
					Observation: serialisedDefault,
				})
			} else {
				r.lggr.Debugw("neither value, error or default is set in the observation input for request", "requestID", req.ID())
			}
		}
	}

	observation := &oracletypes.Observation{Observations: requestObservations}

	r.lggr.Debugw("Observation complete", "numObservations", len(requestObservations), "numOfRequestsInQuery", len(requestsQuery.Requests))
	return proto.MarshalOptions{Deterministic: true}.Marshal(observation)
}

func (r *reportingPlugin) ValidateObservation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	return nil
}

func (r *reportingPlugin) ObservationQuorum(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (bool, error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumTwoFPlusOne, r.n, r.f, aos), nil
}

func (r *reportingPlugin) Outcome(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, attributedObservations []types.AttributedObservation) (ocr3types.Outcome, error) {
	requestsQuery := &oracletypes.Query{}
	err := proto.Unmarshal(query, requestsQuery)
	if err != nil {
		return nil, err
	}

	// Group attributed observations by request ID.
	requestIDToObservations := make(map[string][]*valuespb.Value)
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

			requestIDToObservations[requestID] = append(requestIDToObservations[requestID], observationValue)
		}
	}

	var outcomes []*oracletypes.RequestOutcome
	for _, request := range requestsQuery.Requests {
		requestID := request.Metadata.RequestId
		observations := requestIDToObservations[requestID]
		if len(observations) < r.minimumObservations {
			r.lggr.Debugw("insufficient observations for request", "requestID", requestID, "numObservations", len(observations))
			continue
		}

		consensusDescriptor := &pb.ConsensusDescriptor{}
		err := proto.Unmarshal(request.RequestConsensusDescriptor, consensusDescriptor)
		if err != nil {
			return nil, fmt.Errorf("could not unmarshal consensus descriptor for request %s: %w", requestID, err)
		}

		value, err := CalculateOutcomeForObservations(observations, consensusDescriptor)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate outcome for observations %s: %w", requestID, err)
		}

		serialisedValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal outcome value for request %s: %w", requestID, err)
		}

		outcomes = append(outcomes, &oracletypes.RequestOutcome{
			Metadata: request.Metadata,
			Outcome:  serialisedValue,
		})
	}

	serialisedOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Outcome{
		Outcomes: outcomes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outcome: %w", err)
	}

	return serialisedOutcome, nil
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

		serialisedOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(requestOutcome)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request outcome for request id %s: %w", requestOutcome.Metadata.RequestId, err)
		}

		// TODO - request meta data - need to ensure this can't be manipulated by the leader node
		info := &ocrtypes.ReportInfo{
			Id: &ocrtypes.Id{
				WorkflowExecutionId:      reqMetadata.WorkflowExecutionId,
				WorkflowId:               reqMetadata.WorkflowId,
				WorkflowOwner:            reqMetadata.WorkflowOwner,
				WorkflowName:             reqMetadata.WorkflowName,
				ReportId:                 "", // TODO confirm for value reports we would not use this, but for onchain reports we would
				WorkflowDonId:            reqMetadata.WorkflowDonId,
				WorkflowDonConfigVersion: reqMetadata.WorkflowDonConfigVersion,
				KeyId:                    reqMetadata.KeyBundleId,
			},
			ShouldReport: true,
		}

		// TODO - its as this point that an encoder should be applied for report generation

		infob, err := marshalReportInfo(info, reqMetadata.KeyBundleId)
		if err != nil {
			r.lggr.Errorw("could not marshal id into ReportWithInfo for request", "requestID",
				reqMetadata.RequestId, "error", err)
			continue
		}

		reports = append(reports, ocr3types.ReportPlus[[]byte]{
			ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
				Report: serialisedOutcome,
				Info:   infob,
			},
			TransmissionScheduleOverride: nil,
		})
	}

	r.lggr.Debug("Reports complete, number of reports", len(reports))
	return reports, nil
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

// TODO for value consensus, multichain support is not required, so could arguably simplify and remove the use
// of keybundleid.  If however we want to support report generation in the same capability this will be required.
// If required make this reusable in common and use here as the multi key encoders in common rely on the encoding format being the same
func marshalReportInfo(info *ocrtypes.ReportInfo, keyID string) ([]byte, error) {
	p, err := proto.MarshalOptions{Deterministic: true}.Marshal(info)
	if err != nil {
		return nil, err
	}

	infos, err := structpb.NewStruct(map[string]any{
		"keyBundleName": keyID,
		"reportInfo":    p,
	})
	if err != nil {
		return nil, err
	}

	ip, err := proto.MarshalOptions{Deterministic: true}.Marshal(infos)
	if err != nil {
		return nil, err
	}

	return ip, nil
}
