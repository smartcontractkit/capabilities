package oracle_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"google.golang.org/protobuf/types/known/structpb"
)

type protocolRoundTest struct {
	requests            []*oracle.ConsensusRequest
	expectedResult      *values.Int64
	expectedKeyBundleID string
}

// TODO tests for determinism, shuffling inputs, non-happy path etc.

func Test_MismatchedLeaderMetaData(t *testing.T) {

	lggr := logger.Test(t)
	ctx := t.Context()

	n := 7
	f := 2
	batchSize := 10

	defaultMetaData := oracle.ConsensusRequestMetadata{
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowID:               "default-workflow-id",
			WorkflowOwner:            "test-owner",
			WorkflowExecutionID:      "default-workflow-execution-id",
			WorkflowName:             "test-workflow",
			WorkflowDonID:            1,
			WorkflowDonConfigVersion: 1,
			ReferenceID:              "01",
			DecodedWorkflowName:      "test-workflow-decoded",
			SpendLimits:              nil,
		},
		KeyBundleID: "evm",
	}

	leaderMetaData := oracle.ConsensusRequestMetadata{
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowID:               "leader-workflow-id",
			WorkflowOwner:            "test-owner",
			WorkflowExecutionID:      "leader-workflow-execution-id",
			WorkflowName:             "test-workflow",
			WorkflowDonID:            1,
			WorkflowDonConfigVersion: 1,
			ReferenceID:              "01",
			DecodedWorkflowName:      "test-workflow-decoded",
			SpendLimits:              nil,
		},
		KeyBundleID: "evm",
	}

	newCr := func(observation int64, metadata oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
		requestID := defaultMetaData.WorkflowExecutionID + "-" + defaultMetaData.ReferenceID

		simpleConsensusInputs := &pb.SimpleConsensusInputs{
			Observation: &pb.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
			Descriptors: &pb.ConsensusDescriptor{Descriptor_: &pb.ConsensusDescriptor_Aggregation{Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN}},
		}

		return oracle.NewConsensusRequest(requestID, simpleConsensusInputs, time.Now().Add(1*time.Hour).UTC(), nil, metadata)
	}

	protocolRoundTests := map[string]protocolRoundTest{
		"req-2": {requests: []*oracle.ConsensusRequest{
			newCr(110, leaderMetaData), newCr(120, defaultMetaData), newCr(130, defaultMetaData),
			newCr(140, defaultMetaData), newCr(150, defaultMetaData), newCr(160, defaultMetaData),
			newCr(170, defaultMetaData)},
			expectedResult: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, protocolRoundTests)
}

func Test_ProtocolRounds(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	n := 7
	f := 2
	batchSize := 10

	defaultMetaData := oracle.ConsensusRequestMetadata{
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowID:               uuid.NewString(),
			WorkflowOwner:            "test-owner",
			WorkflowExecutionID:      uuid.NewString(),
			WorkflowName:             "test-workflow",
			WorkflowDonID:            1,
			WorkflowDonConfigVersion: 1,
			ReferenceID:              "01",
			DecodedWorkflowName:      "test-workflow-decoded",
			SpendLimits:              nil,
		},
		KeyBundleID: "",
	}

	newCrKBID := func(observation int64, keyBundleID string) *oracle.ConsensusRequest {
		requestID := defaultMetaData.WorkflowExecutionID + "-" + defaultMetaData.ReferenceID

		metaDataCopy := defaultMetaData
		metaDataCopy.KeyBundleID = keyBundleID

		simpleConsensusInputs := &pb.SimpleConsensusInputs{
			Observation: &pb.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
			Descriptors: &pb.ConsensusDescriptor{Descriptor_: &pb.ConsensusDescriptor_Aggregation{Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN}},
		}

		return oracle.NewConsensusRequest(requestID, simpleConsensusInputs, time.Now().Add(1*time.Hour).UTC(), nil, metaDataCopy)
	}

	newCr := func(observation int64) *oracle.ConsensusRequest {
		return newCrKBID(observation, "")
	}

	reqToObservations := map[string]protocolRoundTest{
		"req-1": {requests: []*oracle.ConsensusRequest{
			newCrKBID(10, "evm"), newCrKBID(20, "evm"), newCrKBID(30, "evm"),
			newCrKBID(40, "evm"), newCrKBID(50, "evm"), newCrKBID(60, "evm"),
			newCrKBID(70, "evm")},
			expectedResult: values.NewInt64(40), expectedKeyBundleID: "evm"},

		"req-2": {requests: []*oracle.ConsensusRequest{
			newCr(110), newCr(120), newCr(130),
			newCr(140), newCr(150), newCr(160),
			newCr(170)},
			expectedResult: values.NewInt64(140)},

		// Simulate some rounds where some nodes have not yet received the observation for req-3 and req-4
		"req-3": {requests: []*oracle.ConsensusRequest{
			newCr(110), newCr(120), newCr(130),
			newCr(140), newCr(150), nil, nil},
			expectedResult: values.NewInt64(130)},
		"req-4": {requests: []*oracle.ConsensusRequest{
			newCr(110), nil, newCr(130),
			newCr(140), newCr(150), nil,
			newCr(170)},
			expectedResult: values.NewInt64(140)},
		"req-5": {requests: []*oracle.ConsensusRequest{
			newCr(110), nil, newCr(130),
			nil, newCr(150), nil,
			newCr(170)},
			expectedResult: nil},

		// Simulate a round where there are insufficient observations for req-6
		"req-6": {requests: []*oracle.ConsensusRequest{
			newCr(110), nil, newCr(130),
			newCr(140), newCr(150), nil, nil},
			expectedResult: nil},

		// Simulate a round where the leader has not yet received the observation for req-7
		"req-7": {requests: []*oracle.ConsensusRequest{
			nil, newCr(120), newCr(130),
			newCr(140), newCr(150), newCr(160),
			newCr(170)},
			expectedResult: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func runProtocolRoundTests(ctx context.Context, t *testing.T, lggr logger.Logger, n, f, batchSize int, reqToObservations map[string]protocolRoundTest) {
	var reportingPlugins []ocr3types.ReportingPlugin[[]byte]
	for i := 0; i < n; i++ {
		pluginObs := map[string]*oracle.ConsensusRequest{}
		pluginRequestMetaData := make(map[string]metadata)

		for reqID, obsData := range reqToObservations {
			observation := obsData.requests[i]
			if observation != nil {
				pluginObs[reqID] = observation
			}

			pluginRequestMetaData[reqID] = metadata{
				keyBundleID: obsData.expectedKeyBundleID,
			}
		}
		reportingPlugin := createReportingPlugin(t, pluginObs, lggr, f, n, batchSize, pluginRequestMetaData)
		reportingPlugins = append(reportingPlugins, reportingPlugin)
	}

	// Simulate a protocol round

	// Select the first reporting plugin as the leader, note that setting the observation to nil for the leader
	// will result in a nil outcome for that request
	leaderPlugin := reportingPlugins[0]

	outCtx := ocr3types.OutcomeContext{}

	query, err := leaderPlugin.Query(ctx, outCtx)
	require.NoError(t, err)

	var attributedObservations []types.AttributedObservation
	for oracleIdx, plugin := range reportingPlugins {
		observation, err := plugin.Observation(ctx, outCtx, query)

		fmt.Printf("Oracle %d observation: %v\n", oracleIdx, observation)

		require.NoError(t, err, "failed to get observation from reporting plugin")
		attributedObservations = append(attributedObservations, types.AttributedObservation{
			Observation: observation,
			Observer:    commontypes.OracleID(oracleIdx), //nolint:gosec // G115
		})
	}

	for _, plugin := range reportingPlugins {
		for _, obs := range attributedObservations {
			err := plugin.ValidateObservation(ctx, outCtx, query, obs)
			require.NoError(t, err, "failed to validate observation from reporting plugin")
		}
	}

	for _, plugin := range reportingPlugins {
		quorumReached, err := plugin.ObservationQuorum(ctx, outCtx, query, attributedObservations)
		require.NoError(t, err, "failed to validate observation from reporting plugin")
		require.True(t, quorumReached, "quorum should be reached for observation")
	}

	var nodeOutcomes []ocr3types.Outcome
	for _, plugin := range reportingPlugins {
		outcome, err := plugin.Outcome(ctx, outCtx, query, attributedObservations)
		require.NoError(t, err, "failed to get outcome from reporting plugin")
		nodeOutcomes = append(nodeOutcomes, outcome)
	}

	// Verify that all outcomes are the same
	for i := 1; i < len(nodeOutcomes); i++ {
		require.True(t, bytes.Equal(nodeOutcomes[0], nodeOutcomes[i]), "outcomes should be equal across reporting plugins")
	}

	var allReports [][]ocr3types.ReportPlus[[]byte]
	for _, plugin := range reportingPlugins {
		reports, err := plugin.Reports(ctx, 0, nodeOutcomes[0])
		require.NoError(t, err, "failed to report outcome from reporting plugin")

		outcome := &oracletypes.Outcome{}
		err = proto.Unmarshal(nodeOutcomes[0], outcome)
		require.NoError(t, err, "failed to unmarshal value from outcome")

		require.Len(t, reports, len(outcome.Outcomes), "reporting plugin returned wrong number of reports")
		allReports = append(allReports, reports)
	}

	// Verify all reports are the same for each request
	for i := 1; i < len(allReports); i++ {
		require.Equal(t, len(allReports[0]), len(allReports[i]), "number of reports should be equal across reporting plugins")

		for idx, reports := range allReports[0] {
			require.Equal(t, reports.ReportWithInfo.Report, allReports[i][idx].ReportWithInfo.Report, "reports should be equal across reporting plugins")
			require.Equal(t, reports.ReportWithInfo.Info, allReports[i][idx].ReportWithInfo.Info, "report info should be equal across reporting plugins")
		}
	}

	// Create a map to hold the request ID to expected result
	requestIDToOutcome := make(map[string]*protocolRoundTest)
	for reqID, obs := range reqToObservations {
		if obs.expectedResult != nil {
			requestIDToOutcome[reqID] = &obs
		} else {
			requestIDToOutcome[reqID] = nil // No expected result for this request
		}
	}

	// Get reports and verify the value selected
	reports := allReports[0]
	for _, report := range reports {
		outcome := &oracletypes.RequestOutcome{}
		err := proto.Unmarshal(report.ReportWithInfo.Report, outcome)
		require.NoError(t, err, "failed to unmarshal value from outcome")

		reqID := outcome.Metadata.RequestId

		expectedOutcome, ok := requestIDToOutcome[reqID]
		require.True(t, ok, "got unexpected result for request %s", reqID)

		actual := outcome.Outcome
		actualProto := &valuespb.Value{}
		err = proto.Unmarshal(actual, actualProto)
		require.NoError(t, err, "failed to unmarshal value from outcome")
		expectedProto := values.Proto(expectedOutcome.expectedResult)
		fmt.Printf("Expected outcome for request %s: %s, Actual outcome: %s\n", reqID, expectedProto, actualProto)
		require.True(t, proto.Equal(actualProto, expectedProto), "expected outcome value to match expected value for request %s", reqID)

		require.Equal(t, expectedOutcome.expectedKeyBundleID, outcome.Metadata.KeyBundleId)

		// Verify that the report info contains key bundle id
		require.NotNil(t, report.ReportWithInfo.Info, "report info should not be nil")

		var infos structpb.Struct
		err = proto.Unmarshal(report.ReportWithInfo.Info, &infos)
		require.NoError(t, err, "failed to unmarshal value from report")

		keyBundleName := infos.Fields["keyBundleName"].GetStringValue()
		assert.Equal(t, expectedOutcome.expectedKeyBundleID, keyBundleName, "keyBundle name should be equal")
	}
}

type metadata struct {
	keyBundleID string
}

func createReportingPlugin(t *testing.T, pluginObservations map[string]*oracle.ConsensusRequest, lggr logger.Logger, f int, n int,
	batchSize int, requestMetaData map[string]metadata) ocr3types.ReportingPlugin[[]byte] {
	reqStore := requests.NewStore[*oracle.ConsensusRequest]()
	for reqID, obs := range pluginObservations {
		req := oracle.NewConsensusRequest(reqID, obs.Input, time.Now(), nil, oracle.ConsensusRequestMetadata{KeyBundleID: requestMetaData[reqID].keyBundleID})
		err := reqStore.Add(req)
		require.NoError(t, err, "failed to add request to store")
	}

	reportingPlugin, err := oracle.NewReportingPlugin(lggr, f, n, reqStore, batchSize)
	require.NoError(t, err)
	return reportingPlugin
}
