package plugin

import (
	"bytes"
	"context"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

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

			isValueNil, err := isNilOrEmptySlice(obs.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to check if observation value is nil for request %s: %w", req.ID(), err)
			}

			if !isValueNil {
				marshalledValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(obs.Value)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal observation value for request %s: %w", req.ID(), err)
				}

				newOb = &oracletypes.RequestObservation{
					Metadata:    queryRequest.Metadata,
					Observation: marshalledValue,
					ReceivedAt:  timestamppb.New(req.ReceivedAt),
				}
			} else {
				isDefaultNil, err := isNilOrEmptySlice(req.Input.Default)
				if err != nil {
					return nil, fmt.Errorf("failed to check if default value is nil for request %s: %w", req.ID(), err)
				}

				if !isDefaultNil {
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
		case *sdk.SimpleConsensusInputs_Error:
			r.lggr.Debugw("observation is an error, skipping", "error", obs.Error, "requestID", req.ID())
			continue
		default:
			r.lggr.Debugw("observation is of unknown type, skipping", "requestID", req.ID())
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

func isNilOrEmptySlice(valueAsProto *valuespb.Value) (bool, error) {
	if valueAsProto == nil {
		return true, nil
	}

	marshalledValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(valueAsProto)
	if err != nil {
		return false, fmt.Errorf("failed to marshal observation value %w", err)
	}

	if len(marshalledValue) == 0 {
		return true, nil
	}

	value, err := values.FromProto(valueAsProto)
	if err != nil {
		return false, fmt.Errorf("failed to convert observation value from proto %w", err)
	}

	unwrappedVal, err := value.Unwrap()
	if err != nil {
		return false, fmt.Errorf("failed to unwrap observation value %w", err)
	}

	if unwrappedVal == nil {
		return true, nil
	}

	return isEmptySlice(unwrappedVal), nil
}

func isEmptySlice(val any) bool {
	v := reflect.ValueOf(val)
	return v.Kind() == reflect.Slice && v.Len() == 0
}

func requestDescriptorMetadataAndDefaultMatch(lggr logger.Logger, req *oracle.ConsensusRequest,
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

	isReqDefaultNil, err := isNilOrEmptySlice(req.Input.Default)
	if err != nil {
		return false, fmt.Errorf("failed to check if default is nil for request %s: %w", req.ID(), err)
	}

	if len(queryRequest.RequestDefault) > 0 {
		if isReqDefaultNil {
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
		if !isReqDefaultNil {
			lggr.Debugw("Default value mismatch - request has default but query does not", "requestID", req.ID())
			return false, nil
		}
	}

	return true, nil
}
