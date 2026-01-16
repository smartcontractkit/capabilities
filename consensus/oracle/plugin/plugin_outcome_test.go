package plugin_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	libocrTypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

func TestOutcomePhaseHandlesPermanentlyExcludedObservations(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	f := 2
	n := 7
	reportingPlugin, reqStore := createReportingPlugin(t, lggr, f, n, 5, 1000)

	// Create a request with oversized observation
	md := newRequestMetaData()
	largeData := strings.Repeat("x", 2000000) // 2MB, way over 1MB limit
	largeValue, err := values.Wrap(largeData)
	require.NoError(t, err)

	req := oracle.NewConsensusRequest(
		&sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(largeValue)},
			Descriptors: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL,
				},
			},
		},
		time.Now(),
		time.Now().Add(1*time.Hour),
		make(chan oracle.ConsensusResponse, 1),
		oracle.ConsensusRequestMetadata{
			RequestMetadata: capabilities.RequestMetadata{
				WorkflowExecutionID: md.WorkflowExecutionID,
				ReferenceID:         md.ReferenceID,
			},
			KeyBundleID: "evm",
		},
	)

	err = reqStore.Add(req)
	require.NoError(t, err)

	// Create observations from multiple nodes, all excluding the request
	query, err := reportingPlugin.Query(ctx, ocr3types.OutcomeContext{})
	require.NoError(t, err)

	var attributedObservations []libocrTypes.AttributedObservation
	for i := range n {
		observation, err := reportingPlugin.Observation(ctx, ocr3types.OutcomeContext{}, query)
		require.NoError(t, err)

		// Verify the observation contains the excluded request ID
		obs := &oracletypes.Observation{}
		err = proto.Unmarshal(observation, obs)
		require.NoError(t, err)
		require.Contains(t, obs.PermanentlyExcludedRequestIds, req.RequestID,
			"observation should contain permanently excluded request ID")

		attributedObservations = append(attributedObservations, libocrTypes.AttributedObservation{
			Observation: observation,
			Observer:    commontypes.OracleID(uint8(i)), //nolint:gosec // G115: n is 7, so i is always < 256
		})
	}

	// Run outcome phase
	outcome, err := reportingPlugin.Outcome(ctx, ocr3types.OutcomeContext{}, query, attributedObservations)
	require.NoError(t, err)

	// Verify outcome contains failure for excluded request
	outcomeProto := &oracletypes.Outcome{}
	err = proto.Unmarshal(outcome, outcomeProto)
	require.NoError(t, err)

	require.Len(t, outcomeProto.Outcomes, 1, "should have one outcome")
	failureOutcome := outcomeProto.Outcomes[0].GetFailure()
	require.NotNil(t, failureOutcome, "outcome should be a failure")
	require.Equal(t, req.RequestID, failureOutcome.RequestID)
	require.Equal(t, oracletypes.ConsensusFailureCode_OBSERVATION_TOO_LARGE, failureOutcome.Code)
	require.Contains(t, failureOutcome.FailureMessage, "observation too large")
}

func TestOutcomePhaseMixedExcludedAndValidObservations(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	f := 2
	n := 7
	reportingPlugin, reqStore := createReportingPlugin(t, lggr, f, n, 5, 1000)

	// Create one valid request and one oversized request
	md1 := newRequestMetaData()
	md2 := newRequestMetaData()

	// Valid request - add single request to store (multiple nodes will observe the same request)
	validReq := newIdenticalCr(t, 100, md1)
	err := reqStore.Add(validReq)
	require.NoError(t, err)

	// Oversized request
	largeData := strings.Repeat("x", 2000000)
	largeValue, err := values.Wrap(largeData)
	require.NoError(t, err)

	oversizedReq := oracle.NewConsensusRequest(
		&sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(largeValue)},
			Descriptors: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL,
				},
			},
		},
		time.Now(),
		time.Now().Add(1*time.Hour),
		make(chan oracle.ConsensusResponse, 1),
		oracle.ConsensusRequestMetadata{
			RequestMetadata: capabilities.RequestMetadata{
				WorkflowExecutionID: md2.WorkflowExecutionID,
				ReferenceID:         md2.ReferenceID,
			},
			KeyBundleID: "evm",
		},
	)

	err = reqStore.Add(oversizedReq)
	require.NoError(t, err)

	// Run protocol round
	query, err := reportingPlugin.Query(ctx, ocr3types.OutcomeContext{})
	require.NoError(t, err)

	var attributedObservations []libocrTypes.AttributedObservation
	for i := range n {
		observation, err := reportingPlugin.Observation(ctx, ocr3types.OutcomeContext{}, query)
		require.NoError(t, err)

		attributedObservations = append(attributedObservations, libocrTypes.AttributedObservation{
			Observation: observation,
			Observer:    commontypes.OracleID(uint8(i)), //nolint:gosec // G115: n is 7, so i is always < 256
		})
	}

	outcome, err := reportingPlugin.Outcome(ctx, ocr3types.OutcomeContext{}, query, attributedObservations)
	require.NoError(t, err)

	outcomeProto := &oracletypes.Outcome{}
	err = proto.Unmarshal(outcome, outcomeProto)
	require.NoError(t, err)

	// Should have 2 outcomes: one success for valid request, one failure for oversized
	require.Len(t, outcomeProto.Outcomes, 2, "should have two outcomes")

	// Find the failure outcome
	var foundFailure, foundSuccess bool
	for _, oc := range outcomeProto.Outcomes {
		if failure := oc.GetFailure(); failure != nil {
			require.Equal(t, oversizedReq.RequestID, failure.RequestID)
			require.Equal(t, oracletypes.ConsensusFailureCode_OBSERVATION_TOO_LARGE, failure.Code)
			require.Contains(t, failure.FailureMessage, "observation too large")
			foundFailure = true
		} else if success := oc.GetSuccess(); success != nil {
			require.Equal(t, validReq.RequestID, success.Metadata.RequestId)
			foundSuccess = true
		}
	}

	require.True(t, foundFailure, "should have failure outcome for oversized request")
	require.True(t, foundSuccess, "should have success outcome for valid request")
}
