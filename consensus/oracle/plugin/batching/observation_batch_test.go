package batching_test

import (
	"math/rand"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin/batching"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func TestObservationBatchCapacityCalculation(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	testMetrics := newTestMetrics(t, "observation")
	observationBatch := batching.NewObservationBatch(ctx, testLogger, 100_000_000, testMetrics)

	for i := 0; i < 1000; i++ {
		added := observationBatch.AddObservation(ctx, &oracletypes.RequestObservation{
			Metadata: &oracletypes.RequestMetaData{
				RequestId:           uuid.NewString(),
				WorkflowExecutionId: generateRandomStringBetweenBounds(1, 10000),
			},
			Input:      nil,
			ReceivedAt: nil,
		})

		require.True(t, added)

		serialisedBatch, err := observationBatch.SerialiseObservationBatch(t.Context())
		require.NoError(t, err)

		require.Equal(t, observationBatch.CurrentSerialisedBatchSize(), len(serialisedBatch))
	}

	require.Equal(t, 1, testMetrics.batchRequestsTotal)
	require.Equal(t, 0, testMetrics.batchCapacityExceeded)
}

func generateRandomStringBetweenBounds(lowerBound int, upperBound int) string {
	n := rand.Intn(upperBound-lowerBound) + lowerBound
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = 'A' + rune(rand.Intn(25))
	}
	return string(runes)
}

func TestObservationBatchCapacityExceeded(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	testMetrics := newTestMetrics(t, "observation")
	observationBatch := batching.NewObservationBatch(ctx, testLogger, 100, testMetrics)

	addedAtLeastOnce := false
	for i := 0; i < 1000; i++ {
		added := observationBatch.AddObservation(ctx, &oracletypes.RequestObservation{
			Metadata: &oracletypes.RequestMetaData{
				RequestId:           uuid.NewString(),
				WorkflowExecutionId: "exec-1",
			},
			Input:      nil,
			ReceivedAt: nil,
		})

		if !added {
			require.Equal(t, 1, testMetrics.batchRequestsTotal)
			require.Equal(t, 1, testMetrics.batchCapacityExceeded)
			return
		}

		addedAtLeastOnce = true
	}

	require.True(t, addedAtLeastOnce)
	t.Fatal("expected batch capacity to be exceeded")
}
