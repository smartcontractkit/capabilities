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
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	libocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// makeMedianQuorumObs builds a median-aggregation observation carrying value, with
// median_2fplus1_quorum_flag set to medianQuorumFlag.
func makeMedianQuorumObs(
	t *testing.T,
	reqID string,
	md oracle.ConsensusRequestMetadata,
	observerID uint8,
	value *valuespb.Value,
	medianQuorumFlag bool,
) libocrtypes.AttributedObservation {
	t.Helper()

	ro := &oracletypes.RequestObservation{
		Metadata:   plugin.ToRequestMetaData(md),
		ReceivedAt: timestamppb.New(time.Now()),
		Input: &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: value},
			Descriptors: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
		},
		Median_2Fplus1QuorumFlag: medianQuorumFlag,
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

// Test_Outcome_median2fPlus1QuorumFlag checks the rollout gating in addRequestOutcomeToBatch:
// the 2f+1 median quorum is only applied when every observation in the round sets the flag,
// so a single node that has not yet been upgraded keeps the round on the f+1 quorum.
func Test_Outcome_median2fPlus1QuorumFlag(t *testing.T) {
	t.Parallel()

	const testF, testN = 2, 7

	// 2f+1 = 5 observations, of which only f+1 = 3 are int64. Under the f+1 quorum this
	// aggregates to a median of 20; under the 2f+1 quorum no type reaches quorum.
	observationValues := []*valuespb.Value{
		values.Proto(values.NewInt64(10)),
		values.Proto(values.NewInt64(20)),
		values.Proto(values.NewInt64(30)),
		values.Proto(values.NewString("malicious")),
		values.Proto(values.NewString("malicious")),
	}

	runOutcome := func(t *testing.T, flags []bool) *oracletypes.Outcome {
		t.Helper()

		lggr := logger.Test(t)
		ctx := context.Background()
		reportingPlugin, _ := createReportingPlugin(t, lggr, testF, testN, 5, defaultMaxLengthBytes)

		md := testMetaData()
		reqID := md.RequestID()

		attributed := make([]libocrtypes.AttributedObservation, 0, len(observationValues))
		for i, v := range observationValues {
			attributed = append(attributed, makeMedianQuorumObs(t, reqID, md, uint8(i), v, flags[i]))
		}

		qBytes, err := proto.Marshal(&oracletypes.Query{RequestIDs: []string{reqID}})
		require.NoError(t, err)

		outcomeBytes, err := reportingPlugin.Outcome(ctx, ocr3types.OutcomeContext{SeqNr: 1}, qBytes, attributed)
		require.NoError(t, err)

		outcome := &oracletypes.Outcome{}
		require.NoError(t, proto.Unmarshal(outcomeBytes, outcome))
		require.Len(t, outcome.Outcomes, 1, "expected exactly one consensus outcome")
		return outcome
	}

	t.Run("all observations set the flag so the 2f+1 quorum applies", func(t *testing.T) {
		t.Parallel()

		outcome := runOutcome(t, []bool{true, true, true, true, true})

		failure := outcome.Outcomes[0].GetFailure()
		require.NotNil(t, failure, "expected consensus to fail under the 2f+1 median quorum")
		require.Contains(t, failure.FailureMessage, oracle.ErrNoSingleValueTypeMeetsThreshold.Error())
	})

	t.Run("one observation missing the flag keeps the f+1 quorum", func(t *testing.T) {
		t.Parallel()

		outcome := runOutcome(t, []bool{true, true, true, true, false})

		requireSuccessValue(t, outcome, values.Proto(values.NewInt64(20)))
	})

	t.Run("no observation setting the flag keeps the f+1 quorum", func(t *testing.T) {
		t.Parallel()

		outcome := runOutcome(t, []bool{false, false, false, false, false})

		requireSuccessValue(t, outcome, values.Proto(values.NewInt64(20)))
	})
}

// requireSuccessValue asserts the single outcome succeeded and carries expected.
func requireSuccessValue(t *testing.T, outcome *oracletypes.Outcome, expected *valuespb.Value) {
	t.Helper()

	success := outcome.Outcomes[0].GetSuccess()
	require.NotNil(t, success, "expected consensus to succeed under the f+1 median quorum")

	got := &valuespb.Value{}
	require.NoError(t, proto.Unmarshal(success.GetOutcome(), got))
	require.True(t, proto.Equal(got, expected),
		"expected the median of the three int64 observations\nExpected: %+v\nActual:   %+v", expected, got)
}
