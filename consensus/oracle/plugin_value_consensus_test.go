package oracle_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/capabilities/consensus/metrics"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	pbtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	libocrTypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

type consensusPluginTest struct {
	requests     []*oracle.ConsensusRequest
	verifyReport func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct)
}

const n = 7
const f = 2
const batchSize = 10
const defaultMaxLengthBytes = 1000000 // 1 MB

func Test_MismatchedLeaderConsensusDescriptor(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	metaData := newRequestMetaData()

	newCrIdenticalConsensus := func(observation int64, metaData oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
		simpleConsensusInputs := &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
			Descriptors: &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
		}

		return oracle.NewConsensusRequest(simpleConsensusInputs, time.Now().Add(1*time.Hour).UTC(), time.Now(), nil, metaData)
	}

	protocolRoundTests := map[string]consensusPluginTest{
		metaData.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCrIdenticalConsensus(110, metaData), newCr(120, metaData), newCr(130, metaData),
			newCr(140, metaData), newCr(150, metaData), newCr(160, metaData),
			newCr(170, metaData)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, protocolRoundTests)
}

func Test_MismatchedNonLeaderConsensusDescriptor(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	metaData := newRequestMetaData()

	newCrIdenticalConsensus := func(observation int64, metaData oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
		simpleConsensusInputs := &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
			Descriptors: &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
		}

		return oracle.NewConsensusRequest(simpleConsensusInputs, time.Now().Add(1*time.Hour).UTC(), time.Now(), nil, metaData)
	}

	protocolRoundTests := map[string]consensusPluginTest{
		metaData.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, metaData), newCr(120, metaData), newCr(130, metaData),
			newCr(140, metaData), newCrIdenticalConsensus(150, metaData), newCr(160, metaData),
			newCr(170, metaData)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(130), "")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, protocolRoundTests)
}

func Test_MismatchedLeaderMetaData(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	metaData := newRequestMetaData()
	leaderMetaData := metaData

	leaderMetaData.WorkflowDonID = 2

	protocolRoundTests := map[string]consensusPluginTest{
		metaData.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, leaderMetaData), newCr(120, metaData), newCr(130, metaData),
			newCr(140, metaData), newCr(150, metaData), newCr(160, metaData),
			newCr(170, metaData)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, protocolRoundTests)
}

func Test_MismatchedNonLeaderMetaData(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	metaData := newRequestMetaData()
	misMatchedMetaData := metaData

	misMatchedMetaData.WorkflowDonID = 2

	protocolRoundTests := map[string]consensusPluginTest{
		metaData.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, metaData), newCr(120, metaData), newCr(130, metaData),
			newCr(140, metaData), newCr(150, misMatchedMetaData), newCr(160, metaData),
			newCr(170, metaData)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(130), "")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, protocolRoundTests)
}

func Test_ReceivedAllObservationsFromAllNodes(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md2 := newRequestMetaData()

	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, md1), newCr(20, md1), newCr(30, md1),
			newCr(40, md1), newCr(50, md1), newCr(60, md1),
			newCr(70, md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
			}},

		md2.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md2), newCr(120, md2), newCr(130, md2),
			newCr(140, md2), newCr(150, md2), newCr(160, md2),
			newCr(170, md2)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(140), "")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_ReceivedObservationsWithErrors(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md2 := newRequestMetaData()

	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, md1), newCrWithError(errors.New("its broken"), md1), newCr(30, md1),
			newCr(40, md1), newCr(50, md1), newCr(60, md1),
			newCr(70, md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
			}},

		md2.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md2), newCr(120, md2), newCr(130, md2),
			newCr(140, md2), newCr(150, md2), newCr(160, md2),
			newCrWithError(errors.New("its broken"), md2)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(130), "")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_ReceivedObservationsWithMatchingDefaults(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCrWithObsAndDef(10, 17, md1), newCrWithObsAndDef(20, 17, md1), newCrWithObsAndDef(30, 17, md1),
			newCrWithObsAndDef(40, 17, md1), newCrWithObsAndDef(50, 17, md1), newCrWithObsAndDef(60, 17, md1),
			newCrWithObsAndDef(70, 17, md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

// In this test some nodes have observations that match the default, and some have observations that do not match the default
// The consensus should be reached as there are sufficient observations with defaults that match the leader's default
func Test_ReceivedObservationsWithSomeMisMatchedDefaults_SufficientForConsensus(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCrWithObsAndDef(10, 17, md1), newCrWithObsAndDef(20, 17, md1), newCrWithObsAndDef(30, 17, md1),
			newCrWithObsAndDef(40, 16, md1), newCrWithObsAndDef(50, 17, md1), newCrWithObsAndDef(60, 17, md1),
			newCrWithObsAndDef(70, 17, md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(30), "evm")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

// In this test some nodes have observations that match the default, and some have observations that do not match the default
// The consensus should not be reached as there are insufficient observations with defaults that match the leader's default
func Test_ReceivedObservationsWithSomeMisMatchedDefaults_InsufficientForConsensus(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCrWithObsAndDef(10, 17, md1), newCrWithObsAndDef(20, 12, md1), newCrWithObsAndDef(30, 17, md1),
			newCrWithObsAndDef(40, 16, md1), newCrWithObsAndDef(50, 17, md1), newCrWithObsAndDef(60, 11, md1),
			newCrWithObsAndDef(70, 17, md1)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

// In this test other nodes have observations that do not match the leader's default
func Test_LeaderNodeMisMatchedDefault_InsufficientForConsensus(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCrWithObsAndDef(10, 14, md1), newCrWithObsAndDef(20, 17, md1), newCrWithObsAndDef(30, 17, md1),
			newCrWithObsAndDef(40, 17, md1), newCrWithObsAndDef(50, 17, md1), newCrWithObsAndDef(60, 17, md1),
			newCrWithObsAndDef(70, 17, md1)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_MissingButSufficientObservations(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()
	md3 := newRequestMetaData()
	md4 := newRequestMetaData()
	md5 := newRequestMetaData()

	reqToObservations := map[string]consensusPluginTest{

		// Simulate some rounds where some nodes have not yet received the observation for req-3 and req-4
		md3.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md3), newCr(120, md3), newCr(130, md3),
			newCr(140, md3), newCr(150, md3), nil, nil},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(130), "")
			}},
		md4.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md4), nil, newCr(130, md4),
			newCr(140, md4), newCr(150, md4), nil,
			newCr(170, md4)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(140), "")
			}},
		md5.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md5), nil, newCr(130, md5),
			nil, newCr(150, md5), nil,
			newCr(170, md5)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_InsufficientObservations(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md6 := newRequestMetaData()

	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, md1), newCr(20, md1), newCr(30, md1),
			newCr(40, md1), newCr(50, md1), newCr(60, md1),
			newCr(70, md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
			}},

		// Simulate a round where there are insufficient observations for req-6
		md6.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md6), nil, newCr(130, md6),
			newCr(140, md6), newCr(150, md6), nil, nil},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_LeaderHasNoMatchingRequest(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md7 := newRequestMetaData()

	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, md1), newCr(20, md1), newCr(30, md1),
			newCr(40, md1), newCr(50, md1), newCr(60, md1),
			newCr(70, md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
			}},

		// Simulate a round where the leader has not yet received the observation for req-7
		md7.RequestID(): {requests: []*oracle.ConsensusRequest{
			nil, newCr(120, md7), newCr(130, md7),
			newCr(140, md7), newCr(150, md7), newCr(160, md7),
			newCr(170, md7)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_WithOutcomeContext(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()

	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, md1), newCr(20, md1), newCr(30, md1),
			newCr(40, md1), newCr(50, md1), newCr(60, md1),
			newCr(70, md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func newRequestMetaData() oracle.ConsensusRequestMetadata {
	return oracle.ConsensusRequestMetadata{
		RequestMetadata: capabilities.RequestMetadata{

			WorkflowID:    "0039525c34de895c8fa68006bd63f6ce4a45ef1bc66377e791c6a8ae803dc0e4",
			WorkflowOwner: "1139525c34de895c8fa68006bd634387a9f1192a",

			WorkflowExecutionID:      generateRandomHexString(32),
			WorkflowName:             "a1b2c3d4e5f6a1b2c3d4",
			WorkflowDonID:            1,
			WorkflowDonConfigVersion: 1,
			ReferenceID:              "01",
			DecodedWorkflowName:      "test-workflow-decoded",
			SpendLimits:              nil,
		},
		KeyBundleID: "",
		ReportID:    generateRandomHexString(2),
	}
}

func generateRandomHexString(byteLength int) string {
	randomBytes := make([]byte, byteLength)
	_, err := rand.Read(randomBytes)
	if err != nil {
		panic(fmt.Sprintf("failed to generate random bytes: %v", err))
	}
	return hex.EncodeToString(randomBytes)
}

func newCr(observation int64, metaData oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
	simpleConsensusInputs := &sdk.SimpleConsensusInputs{
		Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
		Descriptors: &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN}},
	}

	return oracle.NewConsensusRequest(simpleConsensusInputs, time.Now(), time.Now().Add(1*time.Hour).UTC(), nil, metaData)
}

func newCrWithError(err error, metaData oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
	simpleConsensusInputs := &sdk.SimpleConsensusInputs{
		Observation: &sdk.SimpleConsensusInputs_Error{
			Error: err.Error(),
		},
		Descriptors: &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN}},
	}

	return oracle.NewConsensusRequest(simpleConsensusInputs, time.Now(), time.Now().Add(1*time.Hour).UTC(), nil, metaData)
}

func newCrWithObsAndDef(observation int64, def int64, metaData oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
	simpleConsensusInputs := &sdk.SimpleConsensusInputs{
		Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(observation))},
		Default:     values.Proto(values.NewInt64(def)),
		Descriptors: &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN}},
	}

	return oracle.NewConsensusRequest(simpleConsensusInputs, time.Now(), time.Now().Add(1*time.Hour).UTC(), nil, metaData)
}

type pluginAndRequestStore struct {
	plugin ocr3types.ReportingPlugin[[]byte]
	store  *requests.Store[*oracle.ConsensusRequest]
}

func runProtocolRoundTests(ctx context.Context, t *testing.T, lggr logger.Logger, n, f, batchSize int,
	reqToObservations map[string]consensusPluginTest) {
	pluginAndRequestStores := createPluginsAndStores(n, t, lggr, f, batchSize, 5)

	addRequestsToAllStores(pluginAndRequestStores, reqToObservations, t)
	runProtocolRoundTestsWithPlugins(ctx, t, reqToObservations, pluginAndRequestStores, ocr3types.OutcomeContext{})
}

func createPluginsAndStores(n int, t *testing.T, lggr logger.Logger, f int, batchSize int, outcomeExpirySpan uint64) []pluginAndRequestStore {
	var pluginAndRequestStores []pluginAndRequestStore

	for i := 0; i < n; i++ {
		reportingPlugin, reqStore := createReportingPlugin(t, lggr, f, n, batchSize, outcomeExpirySpan)
		pluginAndRequestStores = append(pluginAndRequestStores, pluginAndRequestStore{
			plugin: reportingPlugin,
			store:  reqStore,
		})
	}
	return pluginAndRequestStores
}

// runProtocolRoundTestsWithPlugins simulates a single protocol round with the provided reporting plugins and request stores
// It verifies that all plugins reach the same outcome and that the reports generated are as expected according to the test
// and returns the outcome
func runProtocolRoundTestsWithPlugins(ctx context.Context, t *testing.T,
	reqToObservations map[string]consensusPluginTest, pluginAndRequestStores []pluginAndRequestStore, previousOutcome ocr3types.OutcomeContext) ocr3types.Outcome {
	// Simulate a protocol round
	// Select the first reporting plugin as the leader, note that setting the observation to nil for the leader
	// will result in a nil outcome for that request
	leaderPlugin := pluginAndRequestStores[0].plugin

	query, err := leaderPlugin.Query(ctx, previousOutcome)
	require.NoError(t, err)

	var attributedObservations []libocrTypes.AttributedObservation
	for oracleIdx, plugin := range pluginAndRequestStores {
		observation, err := plugin.plugin.Observation(ctx, previousOutcome, query)

		fmt.Printf("Oracle %d observation: %v\n", oracleIdx, observation)

		require.NoError(t, err, "failed to get observation from reporting plugin")
		attributedObservations = append(attributedObservations, libocrTypes.AttributedObservation{
			Observation: observation,
			Observer:    commontypes.OracleID(oracleIdx), //nolint:gosec // G115
		})
	}

	for _, pluginAndStore := range pluginAndRequestStores {
		for _, obs := range attributedObservations {
			err := pluginAndStore.plugin.ValidateObservation(ctx, previousOutcome, query, obs)
			require.NoError(t, err, "failed to validate observation from reporting plugin")
		}
	}

	for _, pluginAndStore := range pluginAndRequestStores {
		quorumReached, err := pluginAndStore.plugin.ObservationQuorum(ctx, previousOutcome, query, attributedObservations)
		require.NoError(t, err, "failed to validate observation from reporting plugin")
		require.True(t, quorumReached, "quorum should be reached for observation")
	}

	var nodeOutcomes []ocr3types.Outcome
	for _, pluginAndStore := range pluginAndRequestStores {
		outcome, err := pluginAndStore.plugin.Outcome(ctx, previousOutcome, query, attributedObservations)
		if err != nil {
			continue
		}

		require.NoError(t, err, "failed to get outcome from reporting plugin")
		nodeOutcomes = append(nodeOutcomes, outcome)
	}

	// Verify that all outcomes are the same
	for i := 1; i < len(nodeOutcomes); i++ {
		require.True(t, bytes.Equal(nodeOutcomes[0], nodeOutcomes[i]), "outcomes should be equal across reporting plugins")
	}

	var allReports [][]ocr3types.ReportPlus[[]byte]
	for _, pluginAndStore := range pluginAndRequestStores {
		reports, err := pluginAndStore.plugin.Reports(ctx, 0, nodeOutcomes[0])
		require.NoError(t, err, "failed to report outcome from reporting plugin")

		outcome := &oracletypes.Outcome{}
		err = proto.Unmarshal(nodeOutcomes[0], outcome)
		require.NoError(t, err, "failed to unmarshal value from outcome")

		var successfulOutcomes []ocr3types.Outcome
		for _, ro := range outcome.Outcomes {
			if ro.Status == oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS {
				successfulOutcomes = append(successfulOutcomes, ro.Outcome)
			}
		}

		require.Len(t, reports, len(successfulOutcomes), "reporting plugin returned wrong number of reports")
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
	requestIDToOutcome := make(map[string]*consensusPluginTest)
	for reqID, obs := range reqToObservations {
		if obs.verifyReport != nil {
			requestIDToOutcome[reqID] = &obs
		} else {
			requestIDToOutcome[reqID] = nil // No expected result for this request
		}
	}

	// Get reports and verify the value selected
	reports := allReports[0]
	for _, report := range reports {
		serialisedValue := report.ReportWithInfo.Report[oracle.ReportMetaDataPrependLength:]
		actualProto := &valuespb.Value{}
		err := proto.Unmarshal(serialisedValue, actualProto)
		require.NoError(t, err, "failed to unmarshal value from report")

		var infos structpb.Struct
		err = proto.Unmarshal(report.ReportWithInfo.Info, &infos)
		require.NoError(t, err, "failed to unmarshal value from report")

		reqID := infos.Fields[oracle.InfoRequestID].GetStringValue()

		expectedOutcome, ok := requestIDToOutcome[reqID]
		require.True(t, ok, "got unexpected result for request %s", reqID)

		expectedOutcome.verifyReport(t, report, &infos)
	}

	return nodeOutcomes[0]
}

func addRequestsToAllStores(pluginAndRequestStores []pluginAndRequestStore, reqToObservations map[string]consensusPluginTest, t *testing.T) {
	for i := 0; i < len(pluginAndRequestStores); i++ {
		var pluginObs []*oracle.ConsensusRequest

		for _, obsData := range reqToObservations {
			observation := obsData.requests[i]
			if observation != nil {
				pluginObs = append(pluginObs, observation)
			}
		}
		for _, obs := range pluginObs {
			req := obs
			err := pluginAndRequestStores[i].store.Add(req)
			require.NoError(t, err, "failed to add request to store")
		}
	}
}

func removeRequestFromAllStores(pluginAndRequestStores []pluginAndRequestStore, requestID string) {
	for i := 0; i < len(pluginAndRequestStores); i++ {
		pluginAndRequestStores[i].store.Evict(requestID)
	}
}

func verifyValueConsensusReport(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct, expectedResult *values.Int64,
	expectedKeyBundleName string) {
	require.NotNil(t, report.ReportWithInfo, "report should not be nil")
	require.NotNil(t, report.ReportWithInfo.Report, "report value should not be nil")

	// Verify that the report info contains request ID
	require.NotNil(t, infos, "report info should not be nil")
	require.Contains(t, infos.Fields, oracle.InfoRequestID, "report info should contain request ID")

	if expectedKeyBundleName != "" {
		require.Contains(t, infos.Fields, "keyBundleName", "report info should contain key bundle name")
		keyBundleName := infos.Fields["keyBundleName"].GetStringValue()
		require.Equal(t, expectedKeyBundleName, keyBundleName, "keyBundle name should match expected value")
	}

	// Verify the value in the report matches the expected result
	serialisedValue := report.ReportWithInfo.Report[oracle.ReportMetaDataPrependLength:]
	actualProto := &valuespb.Value{}
	err := proto.Unmarshal(serialisedValue, actualProto)
	require.NoError(t, err, "failed to unmarshal value from report")

	expectedProto := values.Proto(expectedResult)
	fmt.Printf("Expected outcome: %s, Actual outcome: %s\n", expectedProto, actualProto)
	require.True(t, proto.Equal(actualProto, expectedProto), "expected outcome value to match expected value")
}

func createReportingPlugin(t *testing.T, lggr logger.Logger, f int, n int,
	batchSize int, outcomeExpirySpan uint64) (ocr3types.ReportingPlugin[[]byte], *requests.Store[*oracle.ConsensusRequest]) {
	reqStore := requests.NewStore[*oracle.ConsensusRequest]()

	metrics, err := metrics.NewMetrics()
	require.NoError(t, err)

	reportingPlugin, err := oracle.NewReportingPlugin(lggr, metrics, f, n, reqStore, &pbtypes.ReportingPluginConfig{
		MaxQueryLengthBytes:       defaultMaxLengthBytes,
		MaxObservationLengthBytes: defaultMaxLengthBytes,
		MaxOutcomeLengthBytes:     defaultMaxLengthBytes,
		MaxBatchSize: func() uint32 {
			if batchSize < 0 || batchSize > int(^uint32(0)) {
				return 0
			}
			return uint32(batchSize)
		}(),
	}, outcomeExpirySpan)
	require.NoError(t, err)
	return reportingPlugin, reqStore
}
