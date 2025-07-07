package oracle_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

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

type testObservations struct {
	observations   []*values.Int64
	expectedResult *values.Int64
	keyBundleID    string
}

// TODO tests for determinism, shuffling inputs, non-happy path etc.

func Test_ProtocolRounds(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	n := 7
	f := 2
	batchSize := 10

	reqToObservations := map[string]testObservations{
		"req-1": {observations: []*values.Int64{
			values.NewInt64(10), values.NewInt64(20), values.NewInt64(30),
			values.NewInt64(40), values.NewInt64(50), values.NewInt64(60),
			values.NewInt64(70)},
			expectedResult: values.NewInt64(40), keyBundleID: "evm"},
		"req-2": {observations: []*values.Int64{
			values.NewInt64(110), values.NewInt64(120), values.NewInt64(130),
			values.NewInt64(140), values.NewInt64(150), values.NewInt64(160),
			values.NewInt64(170)},
			expectedResult: values.NewInt64(140)},

		// Simulate some rounds where some nodes have not yet received the observation for req-3 and req-4
		"req-3": {observations: []*values.Int64{
			values.NewInt64(110), values.NewInt64(120), values.NewInt64(130),
			values.NewInt64(140), values.NewInt64(150), nil, nil},
			expectedResult: values.NewInt64(130)},
		"req-4": {observations: []*values.Int64{
			values.NewInt64(110), nil, values.NewInt64(130),
			values.NewInt64(140), values.NewInt64(150), nil,
			values.NewInt64(170)},
			expectedResult: values.NewInt64(140)},
		"req-5": {observations: []*values.Int64{
			values.NewInt64(110), nil, values.NewInt64(130),
			nil, values.NewInt64(150), nil,
			values.NewInt64(170)},
			expectedResult: nil},

		// Simulate a round where there are insufficient observations for req-6
		"req-6": {observations: []*values.Int64{
			values.NewInt64(110), nil, values.NewInt64(130),
			values.NewInt64(140), values.NewInt64(150), nil, nil},
			expectedResult: nil},

		// Simulate a round where the leader has not yet received the observation for req-7
		"req-7": {observations: []*values.Int64{
			nil, values.NewInt64(120), values.NewInt64(130),
			values.NewInt64(140), values.NewInt64(150), values.NewInt64(160),
			values.NewInt64(170)},
			expectedResult: nil},
	}

	var reportingPlugins []ocr3types.ReportingPlugin[[]byte]
	for i := 0; i < n; i++ {
		pluginObs := map[string]*values.Int64{}
		pluginRequestMetaData := make(map[string]metadata)

		for reqID, obsData := range reqToObservations {
			observation := obsData.observations[i]
			if observation != nil {
				pluginObs[reqID] = observation
			}

			pluginRequestMetaData[reqID] = metadata{
				keyBundleID: obsData.keyBundleID,
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
	requestIDToOutcome := make(map[string]*testObservations)
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

		require.Equal(t, expectedOutcome.keyBundleID, outcome.Metadata.KeyBundleId)

		// Verify that the report info contains key bundle id
		require.NotNil(t, report.ReportWithInfo.Info, "report info should not be nil")

		var infos structpb.Struct
		err = proto.Unmarshal(report.ReportWithInfo.Info, &infos)
		require.NoError(t, err, "failed to unmarshal value from report")

		keyBundleName := infos.Fields["keyBundleName"].GetStringValue()
		assert.Equal(t, expectedOutcome.keyBundleID, keyBundleName, "keyBundle name should be equal")
	}
}

type metadata struct {
	keyBundleID string
}

func createReportingPlugin(t *testing.T, pluginObservations map[string]*values.Int64, lggr logger.Logger, f int, n int,
	batchSize int, requestMetaData map[string]metadata) ocr3types.ReportingPlugin[[]byte] {
	reqStore := requests.NewStore[*oracle.ConsensusRequest]()
	for reqID, obs := range pluginObservations {
		input := &pb.SimpleConsensusInputs{
			Observation: &pb.SimpleConsensusInputs_Value{
				Value: values.Proto(obs),
			},
			Descriptors: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			Default: nil,
		}
		req := oracle.NewConsensusRequest(reqID, input, time.Now(), nil, capabilities.RequestMetadata{}, requestMetaData[reqID].keyBundleID)
		err := reqStore.Add(req)
		require.NoError(t, err, "failed to add request to store")
	}

	reportingPlugin, err := oracle.NewReportingPlugin(lggr, f, n, reqStore, batchSize)
	require.NoError(t, err)
	return reportingPlugin
}
