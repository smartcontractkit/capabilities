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

		simpleConsensusInputs := &pb.SimpleConsensusInputs{
			Observation: &pb.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
			Descriptors: &pb.ConsensusDescriptor{Descriptor_: &pb.ConsensusDescriptor_Aggregation{Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN}},
		}

		return oracle.NewConsensusRequest(simpleConsensusInputs, time.Now().Add(1*time.Hour).UTC(), nil, metadata)
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

	md1 := newRequestMetaData()
	md2 := newRequestMetaData()
	md3 := newRequestMetaData()
	md4 := newRequestMetaData()
	md5 := newRequestMetaData()
	md6 := newRequestMetaData()
	md7 := newRequestMetaData()

	md1.KeyBundleID = "evm"

	newCr := func(observation int64, metaData oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
		simpleConsensusInputs := &pb.SimpleConsensusInputs{
			Observation: &pb.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
			Descriptors: &pb.ConsensusDescriptor{Descriptor_: &pb.ConsensusDescriptor_Aggregation{Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN}},
		}

		return oracle.NewConsensusRequest(simpleConsensusInputs, time.Now().Add(1*time.Hour).UTC(), nil, metaData)
	}

	reqToObservations := map[string]protocolRoundTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, md1), newCr(20, md1), newCr(30, md1),
			newCr(40, md1), newCr(50, md1), newCr(60, md1),
			newCr(70, md1)},
			expectedResult: values.NewInt64(40), expectedKeyBundleID: "evm"},

		md2.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md2), newCr(120, md2), newCr(130, md2),
			newCr(140, md2), newCr(150, md2), newCr(160, md2),
			newCr(170, md2)},
			expectedResult: values.NewInt64(140)},

		// Simulate some rounds where some nodes have not yet received the observation for req-3 and req-4
		md3.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md3), newCr(120, md3), newCr(130, md3),
			newCr(140, md3), newCr(150, md3), nil, nil},
			expectedResult: values.NewInt64(130)},
		md4.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md4), nil, newCr(130, md4),
			newCr(140, md4), newCr(150, md4), nil,
			newCr(170, md4)},
			expectedResult: values.NewInt64(140)},
		md5.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md5), nil, newCr(130, md5),
			nil, newCr(150, md5), nil,
			newCr(170, md5)},
			expectedResult: nil},

		// Simulate a round where there are insufficient observations for req-6
		md6.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md6), nil, newCr(130, md6),
			newCr(140, md6), newCr(150, md6), nil, nil},
			expectedResult: nil},

		// Simulate a round where the leader has not yet received the observation for req-7
		md7.RequestID(): {requests: []*oracle.ConsensusRequest{
			nil, newCr(120, md7), newCr(130, md7),
			newCr(140, md7), newCr(150, md7), newCr(160, md7),
			newCr(170, md7)},
			expectedResult: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func getRequestID(metaData oracle.ConsensusRequestMetadata) string {
	return metaData.WorkflowExecutionID + "-" + metaData.ReferenceID
}

func newRequestMetaData() oracle.ConsensusRequestMetadata {
	return oracle.ConsensusRequestMetadata{
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowID:               "default-workflow-id",
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
	for _, obs := range pluginObservations {
		req := obs
		err := reqStore.Add(req)
		require.NoError(t, err, "failed to add request to store")
	}

	reportingPlugin, err := oracle.NewReportingPlugin(lggr, f, n, reqStore, batchSize)
	require.NoError(t, err)
	return reportingPlugin
}
