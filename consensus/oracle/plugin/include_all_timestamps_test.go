package plugin_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	libocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

type timestampObs struct {
	receivedAt  int64
	isError     bool
	flag        bool
	aggregation sdk.AggregationType
}

func makeTimestampObs(t *testing.T, reqID string, md oracle.ConsensusRequestMetadata, observerID uint8, o timestampObs) libocrtypes.AttributedObservation {
	t.Helper()

	input := &sdk.SimpleConsensusInputs{
		Descriptors: &sdk.ConsensusDescriptor{
			Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: o.aggregation},
		},
	}
	if o.isError {
		input.Observation = &sdk.SimpleConsensusInputs_Error{Error: "boom"}
	} else {
		input.Observation = &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(10))}
	}

	ro := &oracletypes.RequestObservation{
		Metadata:                 plugin.ToRequestMetaData(md),
		ReceivedAt:               timestamppb.New(time.Unix(o.receivedAt, 0)),
		Input:                    input,
		IncludeAllTimestampsFlag: o.flag,
	}

	b, err := proto.Marshal(&oracletypes.Observation{
		Observations: map[string]*oracletypes.RequestObservation{reqID: ro},
	})
	require.NoError(t, err)

	return libocrtypes.AttributedObservation{
		Observation: b,
		Observer:    commontypes.OracleID(observerID),
	}
}

// Test_Outcome_includeAllTimestampsFlag checks that once f+1 observations set the flag, the
// outcome timestamp is the median over every valid observation, including error observations.
func Test_Outcome_includeAllTimestampsFlag(t *testing.T) {
	t.Parallel()

	const testF, testN = 1, 4
	median := sdk.AggregationType_AGGREGATION_TYPE_MEDIAN
	identical := sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL

	runOutcome := func(t *testing.T, obs []timestampObs) *oracletypes.Outcome {
		t.Helper()

		reportingPlugin, _ := createReportingPlugin(t, logger.Test(t), testF, testN, 5, defaultMaxLengthBytes)

		md := testMetaData()
		reqID := md.RequestID()

		attributed := make([]libocrtypes.AttributedObservation, 0, len(obs))
		for i, o := range obs {
			attributed = append(attributed, makeTimestampObs(t, reqID, md, uint8(i), o))
		}

		qBytes, err := proto.Marshal(&oracletypes.Query{RequestIDs: []string{reqID}})
		require.NoError(t, err)

		outcomeBytes, err := reportingPlugin.Outcome(context.Background(), ocr3types.OutcomeContext{SeqNr: 1}, qBytes, attributed)
		require.NoError(t, err)

		outcome := &oracletypes.Outcome{}
		require.NoError(t, proto.Unmarshal(outcomeBytes, outcome))
		require.Len(t, outcome.Outcomes, 1)
		return outcome
	}

	requireTimestamp := func(t *testing.T, outcome *oracletypes.Outcome, expectedUnix int64) {
		t.Helper()

		success := outcome.Outcomes[0].GetSuccess()
		require.NotNil(t, success, "expected consensus to succeed")
		require.Equal(t, expectedUnix, success.GetTimestamp().AsTime().Unix())
	}

	testCases := []struct {
		name              string
		obs               []timestampObs
		expectedTimestamp int64
	}{
		{
			name: "f+1 flags include error observation timestamps",
			obs: []timestampObs{
				{receivedAt: 100, flag: true, aggregation: median},
				{receivedAt: 200, flag: true, aggregation: median},
				{receivedAt: 300, aggregation: median},
				{receivedAt: 1000, isError: true, aggregation: median},
			},
			// median of 100, 200, 300, 1000
			expectedTimestamp: 250,
		},
		{
			name: "all flags include error observation timestamps",
			obs: []timestampObs{
				{receivedAt: 100, flag: true, aggregation: median},
				{receivedAt: 200, flag: true, aggregation: median},
				{receivedAt: 300, flag: true, aggregation: median},
				{receivedAt: 1000, isError: true, flag: true, aggregation: median},
			},
			expectedTimestamp: 250,
		},
		{
			name: "only f flags exclude error observation timestamps",
			obs: []timestampObs{
				{receivedAt: 100, flag: true, aggregation: median},
				{receivedAt: 200, aggregation: median},
				{receivedAt: 300, aggregation: median},
				{receivedAt: 1000, isError: true, aggregation: median},
			},
			expectedTimestamp: 200,
		},
		{
			name: "no flags exclude error observation timestamps",
			obs: []timestampObs{
				{receivedAt: 100, aggregation: median},
				{receivedAt: 200, aggregation: median},
				{receivedAt: 300, aggregation: median},
				{receivedAt: 1000, isError: true, aggregation: median},
			},
			expectedTimestamp: 200,
		},
		{
			name: "observations not matching the consensus descriptor are still excluded",
			obs: []timestampObs{
				{receivedAt: 100, flag: true, aggregation: median},
				{receivedAt: 200, flag: true, aggregation: median},
				{receivedAt: 300, flag: true, aggregation: median},
				{receivedAt: 1000, isError: true, flag: true, aggregation: identical},
			},
			expectedTimestamp: 200,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireTimestamp(t, runOutcome(t, tc.obs), tc.expectedTimestamp)
		})
	}
}

// Test_Observation_propagatesIncludeAllTimestamps checks the locally evaluated flag survives the
// request store (which hands out copies) and is emitted on the observation.
func Test_Observation_propagatesIncludeAllTimestamps(t *testing.T) {
	t.Parallel()

	for _, includeAllTimestamps := range []bool{false, true} {
		reportingPlugin, reqStore := createReportingPlugin(t, logger.Test(t), 1, 4, 5, defaultMaxLengthBytes)

		md := testMetaData()
		req := oracle.NewConsensusRequest(&sdk.SimpleConsensusInputs{}, time.Now(), time.Now().Add(time.Hour), nil, md, nil, false, includeAllTimestamps)
		require.NoError(t, reqStore.Add(req))

		qBytes, err := proto.Marshal(&oracletypes.Query{RequestIDs: []string{md.RequestID()}})
		require.NoError(t, err)

		obsBytes, err := reportingPlugin.Observation(t.Context(), ocr3types.OutcomeContext{SeqNr: 1}, qBytes)
		require.NoError(t, err)

		obs := &oracletypes.Observation{}
		require.NoError(t, proto.Unmarshal(obsBytes, obs))
		require.Contains(t, obs.Observations, md.RequestID())
		require.Equal(t, includeAllTimestamps, obs.Observations[md.RequestID()].IncludeAllTimestampsFlag)
	}
}
