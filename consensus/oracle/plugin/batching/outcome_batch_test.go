package batching_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin/batching"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

func TestOutcomeBatchCapacityCalculation(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	prevOutcome := &oracletypes.Outcome{
		HistoricalOutcomes: map[string]uint64{
			"req-1": 10,
			"req-2": 20,
			"req-3": 30,
		},
	}

	serialisedPrevOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(prevOutcome)
	require.NoError(t, err)

	testMetrics := newTestMetrics(t, "outcome")
	outcome, err := batching.NewOutcomeBatch(ctx, testLogger, ocr3types.OutcomeContext{
		PreviousOutcome: serialisedPrevOutcome,
		SeqNr:           1000,
	}, 1000,
		100_000_000, "evm", testMetrics, 1000)

	require.NoError(t, err)

	for i := 0; i < 1000; i++ {
		added, err := outcome.AddSuccessfulConsensusRequestOutcomeToBatch(ctx, &oracletypes.RequestMetaData{
			RequestId:           uuid.NewString(),
			WorkflowExecutionId: generateRandomStringBetweenBounds(1, 10000),
		}, values.Proto(values.NewString("test-outcome-data-1")), &timestamppb.Timestamp{})

		require.True(t, added)
		require.NoError(t, err)

		added, err = outcome.AddFailedConsensusRequestOutcomeToBatch(ctx, uuid.NewString(), generateRandomStringBetweenBounds(1, 10000),
			oracletypes.ConsensusFailureCode_CONSENSUS_CALCULATION_FAILED)

		require.True(t, added)
		require.NoError(t, err)

		serialisedBatch, err := outcome.SerialiseOutcomeBatch()
		require.NoError(t, err)

		require.Equal(t, outcome.CurrentSerialisedBatchSize(), len(serialisedBatch))
	}

	require.Equal(t, 1, testMetrics.batchRequestsTotal)
	require.Equal(t, 0, testMetrics.batchCapacityExceeded)
}

func TestOutcomeBatchCapacityExceeded(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	prevOutcome := &oracletypes.Outcome{
		HistoricalOutcomes: map[string]uint64{
			"req-1": 10,
			"req-2": 20,
			"req-3": 30,
		},
	}

	serialisedPrevOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(prevOutcome)
	require.NoError(t, err)

	testMetrics := newTestMetrics(t, "outcome")
	outcome, err := batching.NewOutcomeBatch(ctx, testLogger, ocr3types.OutcomeContext{
		PreviousOutcome: serialisedPrevOutcome,
		SeqNr:           1000,
	}, 1000,
		100, "evm", testMetrics, 1000)

	require.NoError(t, err)

	for i := 0; i < 1000; i++ {
		added, err := outcome.AddSuccessfulConsensusRequestOutcomeToBatch(ctx, &oracletypes.RequestMetaData{
			RequestId:           uuid.NewString(),
			WorkflowExecutionId: "exec-1",
		}, values.Proto(values.NewString("test-outcome-data-1")), &timestamppb.Timestamp{})

		if !added {
			require.Equal(t, 1, testMetrics.batchRequestsTotal)
			require.Equal(t, 1, testMetrics.batchCapacityExceeded)
			return
		}
		require.NoError(t, err)

		added, err = outcome.AddFailedConsensusRequestOutcomeToBatch(ctx, uuid.NewString(), "failed",
			oracletypes.ConsensusFailureCode_CONSENSUS_CALCULATION_FAILED)

		if !added {
			return
		}
		require.NoError(t, err)

		serialisedBatch, err := outcome.SerialiseOutcomeBatch()
		require.NoError(t, err)

		require.Equal(t, outcome.CurrentSerialisedBatchSize(), len(serialisedBatch))
	}

	t.Fatal("expected batch capacity to be exceeded")
}
