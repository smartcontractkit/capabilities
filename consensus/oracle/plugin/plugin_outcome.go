package plugin

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cloudevents/sdk-go/v2/event/datacodec/json"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin/batching"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func (r *reportingPlugin) Outcome(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, attributedObservations []types.AttributedObservation) (ocr3types.Outcome, error) {
	requestsQuery := &oracletypes.Query{}
	err := proto.Unmarshal(query, requestsQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal query: %w", err)
	}

	outcomeBatch, err := batching.NewOutcomeBatch(ctx, r.lggr, outctx, r.outcomeExpirySeqNrSpan, int(r.config.MaxOutcomeLengthBytes), r.defaultKeyBundleIDForConsensusFailure,
		r.metrics, r.maxRequestOutcomeSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create new outcome batch: %w", err)
	}

	requestIDToObservations := groupAttributedObservationsByRequestID(r.lggr, attributedObservations)

	for _, requestID := range requestsQuery.RequestIDs {
		observations := requestIDToObservations[requestID]

		// 2f+1 or more observations have been received, calculate the outcome for the request
		if len(observations) >= 2*r.f+1 {
			hasCapacity, err := r.addRequestOutcomeToBatch(ctx, requestID, observations, outcomeBatch)
			if err != nil {
				return nil, fmt.Errorf("failed to add request outcome to batch for request %s: %w", requestID, err)
			}

			if !hasCapacity {
				r.lggr.Debugw("batch does not have capacity to add request outcome - skipping in this round", "requestID", requestID)
				break
			}
			r.lggr.Debugw("added request outcome to batch", "requestID", requestID, "numObservations", len(observations))
		} else {
			r.lggr.Debugw("not enough observations to calculate outcome for request - skipping in this round", "requestID", requestID, "numObservations", len(observations))
		}
	}

	return outcomeBatch.SerialiseOutcomeBatch(ctx)
}

// addRequestOutcomeToBatch adds the outcome for a single request to the outcome batch. Returns false if batch does not have capacity to add the outcome.
func (r *reportingPlugin) addRequestOutcomeToBatch(ctx context.Context, requestID string, observations []*oracletypes.RequestObservation, outcome *batching.OutcomeBatch) (bool, error) {
	consensusMDD, err := r.calculateConsensusMetadataDescriptorAndDefault(observations)
	if err != nil {
		return outcome.AddFailedConsensusRequestOutcomeToBatch(ctx, requestID,
			fmt.Sprintf("failed to calculate consensus metadata, descriptor and default for request: %v", err),
			oracletypes.ConsensusFailureCode_FAILED_TO_CALCULATE_CONSENSUS_MDD)
	}

	var obsErrors []string
	var obsValues []*valuespb.Value
	var timestamps []*timestamppb.Timestamp

	for _, obs := range observations {
		// Does the observation have a timestamp?
		if obs.ReceivedAt == nil {
			r.lggr.Warnw("observation missing receivedAt timestamp", "requestID", requestID, "observerMetadata", obs.Metadata)
			continue
		}

		// Does the observation's metadata, descriptor and default match the consensus?
		if !verifyMetadataDescriptorAndDefaultMatchConsensus(obs, consensusMDD) {
			r.lggr.Warnw("observation metadata, descriptor or default does not match consensus", "requestID", requestID, "observation", obs, "consensusMDD", consensusMDD)
			continue
		}

		// Is the observation an error or a value?
		switch inputObservation := obs.Input.GetObservation().(type) {
		case *sdk.SimpleConsensusInputs_Value:
			obsValues = append(obsValues, inputObservation.Value)
			timestamps = append(timestamps, obs.ReceivedAt)
		case *sdk.SimpleConsensusInputs_Error:
			obsErrors = append(obsErrors, inputObservation.Error)
		}
	}

	timestamp := &timestamppb.Timestamp{}
	if len(timestamps) > 0 {
		timestamp = calculateMedianTimestamp(timestamps)
	}

	if len(obsErrors) >= r.f+1 {
		consensusFailedMsg := fmt.Sprintf(
			"consensus calculation failed: received %d errors which is >= f+1 (%d) for requestID %s; Consensus metadata, descriptor and default: %+v; Errors received: %s",
			len(obsErrors), r.f+1, requestID, consensusMDD, formatErrorsForLogging(ctx, obsErrors),
		)

		return outcome.FailConsensusWithDefaultCheck(ctx, r.lggr, requestID, consensusFailedMsg,
			"consensus calculation failed: received >= f+1 error observations",
			oracletypes.ConsensusFailureCode_RECEIVED_FPLUS1_ERRORS, consensusMDD, timestamp)
	}

	value, err := oracle.CalculateOutcomeForObservations(r.lggr, obsValues, consensusMDD.Input.Descriptors, consensusMDD.Input.Default, r.f)
	if err != nil {
		valuesInfo := formatValuesInfoForLogging(obsValues)
		consensusFailedMsg := fmt.Sprintf(
			"consensus calculation failed: %v; Consensus metadata, descriptor and default: %+v; %s; Errors received: %s",
			err, consensusMDD, valuesInfo, formatErrorsForLogging(ctx, obsErrors),
		)
		return outcome.FailConsensusWithDefaultCheck(ctx, r.lggr, requestID, consensusFailedMsg,
			"consensus calculation failed: aggregation failed",
			oracletypes.ConsensusFailureCode_CONSENSUS_CALCULATION_FAILED, consensusMDD, timestamp)
	}

	return outcome.AddSuccessfulConsensusRequestOutcomeToBatch(ctx, consensusMDD.Metadata, value, timestamp)
}

func formatErrorsForLogging(ctx context.Context, errors []string) string {
	// Deduplicate unique error strings while preserving order
	seen := make(map[string]bool)
	uniqueErrors := make([]string, 0, len(errors))
	for _, err := range errors {
		if !seen[err] {
			seen[err] = true
			uniqueErrors = append(uniqueErrors, err)
		}
	}

	b, err := json.Encode(ctx, uniqueErrors)
	if err != nil {
		return "could not marshal errors"
	}
	return string(b)
}

// formatValuesInfoForLogging extracts type information from values without exposing customer data.
func formatValuesInfoForLogging(obsValues []*valuespb.Value) string {
	if len(obsValues) == 0 {
		return "Values count: 0"
	}

	typeCounts := make(map[string]int)
	for _, val := range obsValues {
		if val == nil {
			typeCounts["nil"]++
			continue
		}

		// Check type without unwrapping the actual value
		switch val.GetValue().(type) {
		case *valuespb.Value_StringValue:
			typeCounts["string"]++
		case *valuespb.Value_Int64Value:
			typeCounts["int64"]++
		case *valuespb.Value_Uint64Value:
			typeCounts["uint64"]++
		case *valuespb.Value_Float64Value:
			typeCounts["float64"]++
		case *valuespb.Value_BoolValue:
			typeCounts["bool"]++
		case *valuespb.Value_BytesValue:
			typeCounts["bytes"]++
		case *valuespb.Value_DecimalValue:
			typeCounts["decimal"]++
		case *valuespb.Value_BigintValue:
			typeCounts["bigint"]++
		case *valuespb.Value_TimeValue:
			typeCounts["time"]++
		case *valuespb.Value_MapValue:
			typeCounts["map"]++
		case *valuespb.Value_ListValue:
			typeCounts["list"]++
		default:
			typeCounts["unknown"]++
		}
	}

	// Format as "Values count: 5 (3 string, 2 int64)" for better debugging without exposing data
	var parts []string
	for typ, count := range typeCounts {
		parts = append(parts, fmt.Sprintf("%d %s", count, typ))
	}

	if len(parts) > 0 {
		return fmt.Sprintf("Values count: %d (%s)", len(obsValues), strings.Join(parts, ", "))
	}
	return fmt.Sprintf("Values count: %d", len(obsValues))
}

// verifyMetadataDescriptorAndDefaultMatchConsensus checks if the observation's metadata, descriptor and default match the consensus.
func verifyMetadataDescriptorAndDefaultMatchConsensus(obs *oracletypes.RequestObservation, consensusMDD *oracletypes.RequestObservation) bool {
	obsMDD := &oracletypes.RequestObservation{
		Metadata: obs.Metadata,
		Input: &sdk.SimpleConsensusInputs{
			Descriptors: obs.Input.Descriptors,
			Default:     obs.Input.Default,
		},
	}

	return proto.Equal(obsMDD, consensusMDD)
}

func (r *reportingPlugin) calculateConsensusMetadataDescriptorAndDefault(observations []*oracletypes.RequestObservation) (*oracletypes.RequestObservation, error) {
	var allObservationsMDDBytes []*valuespb.Value
	for _, obs := range observations {
		mddBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.RequestObservation{
			Metadata: obs.Metadata,
			Input: &sdk.SimpleConsensusInputs{
				Descriptors: obs.Input.Descriptors,
				Default:     obs.Input.Default,
			},
		})
		if err != nil {
			r.lggr.Errorw("could not marshal RequestObservation", "error", err)
			continue
		}

		// Wrapped here to allow reuse of the existing CalculateOutcomeForObservations function for identical aggregation
		allObservationsMDDBytes = append(allObservationsMDDBytes, values.Proto(values.NewBytes(mddBytes)))
	}

	consensusMDDBytes, err := oracle.CalculateOutcomeForObservations(r.lggr, allObservationsMDDBytes,
		&sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
		nil, r.f)
	if err != nil {
		return nil, err
	}

	consensusMDD := &oracletypes.RequestObservation{}
	err = proto.Unmarshal(consensusMDDBytes.GetBytesValue(), consensusMDD)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal consensus metadata, descriptor and default for request: %w", err)
	}

	return consensusMDD, nil
}

func groupAttributedObservationsByRequestID(lggr logger.Logger, attributedObservations []types.AttributedObservation) map[string][]*oracletypes.RequestObservation {
	// sort attributedObservations by oracle ID
	// libOCR doesn't guarantee the same order across nodes (See eventTGraceTimeout() in outcome_generation_leader.go)
	slices.SortFunc(attributedObservations, func(a, b types.AttributedObservation) int {
		return int(a.Observer) - int(b.Observer)
	})

	requestIDToObservations := make(map[string][]*oracletypes.RequestObservation)
	for _, ao := range attributedObservations {
		obs := &oracletypes.Observation{}
		err := proto.Unmarshal(ao.Observation, obs)
		if err != nil {
			lggr.Errorw("could not unmarshal observation from observer", "error", err, "observer", ao.Observer)
			continue
		}

		// the order here is consistent thanks to the initial sorting of attributedObservations
		for requestID, reqObservation := range obs.Observations {
			requestIDToObservations[requestID] = append(requestIDToObservations[requestID], reqObservation)
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
