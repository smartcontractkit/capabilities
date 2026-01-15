package batching_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin/batching"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
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

func TestObservationPermanentlyExcludedDueToSize(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	testMetrics := newTestMetrics(t, "observation")
	// Create a batch with a very small max size (500 bytes)
	observationBatch := batching.NewObservationBatch(ctx, testLogger, 500, testMetrics)

	// Create an observation that is definitely too large to ever fit
	largeData := strings.Repeat("x", 2000) // Much larger than 500 bytes
	largeValue, err := values.Wrap(largeData)
	require.NoError(t, err)

	reqObs := &oracletypes.RequestObservation{
		Metadata: &oracletypes.RequestMetaData{
			RequestId:           "req-too-large",
			WorkflowExecutionId: "exec-1",
		},
		Input: &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(largeValue)},
		},
		ReceivedAt: timestamppb.Now(),
	}

	added := observationBatch.AddObservation(ctx, reqObs)
	require.False(t, added, "observation should not be added")

	// Verify the observation is tracked as permanently excluded
	serialisedBatch, err := observationBatch.SerialiseObservationBatch(ctx)
	require.NoError(t, err)

	obs := &oracletypes.Observation{}
	err = proto.Unmarshal(serialisedBatch, obs)
	require.NoError(t, err)

	require.Contains(t, obs.PermanentlyExcludedRequestIds, "req-too-large",
		"request should be in permanently excluded list")
	require.Equal(t, 1, testMetrics.batchCapacityExceeded)
}

func TestObservationDoesNotFitNowButWouldFitInEmptyBatch(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	testMetrics := newTestMetrics(t, "observation")
	// Create a batch with just enough space for one observation but not two
	observationBatch := batching.NewObservationBatch(ctx, testLogger, 100, testMetrics)

	// Add a first observation that should fit
	smallValue, err := values.Wrap("small-data")
	require.NoError(t, err)

	reqObs1 := &oracletypes.RequestObservation{
		Metadata: &oracletypes.RequestMetaData{
			RequestId:           "req-1",
			WorkflowExecutionId: "exec-1",
		},
		Input: &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(smallValue)},
		},
		ReceivedAt: timestamppb.Now(),
	}

	added := observationBatch.AddObservation(ctx, reqObs1)
	require.True(t, added, "first observation should fit")
	require.Equal(t, 0, testMetrics.batchCapacityExceeded)

	// Add a second observation - should not fit now, but would fit in an empty batch
	reqObs2 := &oracletypes.RequestObservation{
		Metadata: &oracletypes.RequestMetaData{
			RequestId:           "req-2",
			WorkflowExecutionId: "exec-2",
		},
		Input: &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(smallValue)},
		},
		ReceivedAt: timestamppb.Now(),
	}

	added = observationBatch.AddObservation(ctx, reqObs2)
	require.False(t, added, "second observation should not fit in current batch")
	require.Equal(t, 1, testMetrics.batchCapacityExceeded)

	// Verify the observation is NOT tracked as permanently excluded
	serialisedBatch, err := observationBatch.SerialiseObservationBatch(ctx)
	require.NoError(t, err)

	obs := &oracletypes.Observation{}
	err = proto.Unmarshal(serialisedBatch, obs)
	require.NoError(t, err)

	require.NotContains(t, obs.PermanentlyExcludedRequestIds, "req-2",
		"request should NOT be in permanently excluded list")
}

func TestObservationBatchMultiplePermanentlyExcluded(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	testMetrics := newTestMetrics(t, "observation")
	observationBatch := batching.NewObservationBatch(ctx, testLogger, 500, testMetrics)

	largeData := strings.Repeat("x", 2000)
	largeValue, err := values.Wrap(largeData)
	require.NoError(t, err)

	// Add multiple permanently excluded observations
	for i := 0; i < 3; i++ {
		reqObs := &oracletypes.RequestObservation{
			Metadata: &oracletypes.RequestMetaData{
				RequestId:           fmt.Sprintf("req-too-large-%d", i),
				WorkflowExecutionId: "exec-1",
			},
			Input: &sdk.SimpleConsensusInputs{
				Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(largeValue)},
			},
			ReceivedAt: timestamppb.Now(),
		}

		added := observationBatch.AddObservation(ctx, reqObs)
		require.False(t, added)
	}

	serialisedBatch, err := observationBatch.SerialiseObservationBatch(ctx)
	require.NoError(t, err)

	obs := &oracletypes.Observation{}
	err = proto.Unmarshal(serialisedBatch, obs)
	require.NoError(t, err)

	require.Len(t, obs.PermanentlyExcludedRequestIds, 3,
		"all three requests should be permanently excluded")
	require.Contains(t, obs.PermanentlyExcludedRequestIds, "req-too-large-0")
	require.Contains(t, obs.PermanentlyExcludedRequestIds, "req-too-large-1")
	require.Contains(t, obs.PermanentlyExcludedRequestIds, "req-too-large-2")
	require.Equal(t, 3, testMetrics.batchCapacityExceeded)
}

func TestObservationBatchEdgeCaseAtLimit(t *testing.T) {
	testLogger := logger.Test(t)
	ctx := t.Context()

	testMetrics := newTestMetrics(t, "observation")
	// Use a reasonable limit
	maxSize := 1000
	observationBatch := batching.NewObservationBatch(ctx, testLogger, maxSize, testMetrics)

	// Calculate size of empty batch
	emptyBatch := &oracletypes.Observation{Observations: make(map[string]*oracletypes.RequestObservation)}
	emptyBatchSize := proto.Size(emptyBatch)

	// Create observation that fits exactly (accounting for protobuf overhead)
	// Use a conservative estimate of 100 bytes overhead
	availableSize := maxSize - emptyBatchSize - 100
	if availableSize < 0 {
		availableSize = 100 // Minimum test size
	}
	data := strings.Repeat("x", availableSize)
	value, err := values.Wrap(data)
	require.NoError(t, err)

	reqObs := &oracletypes.RequestObservation{
		Metadata: &oracletypes.RequestMetaData{
			RequestId:           "req-at-limit",
			WorkflowExecutionId: "exec-1",
		},
		Input: &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(value)},
		},
		ReceivedAt: timestamppb.Now(),
	}

	added := observationBatch.AddObservation(ctx, reqObs)
	require.True(t, added, "observation at limit should be accepted")

	serialisedBatch, err := observationBatch.SerialiseObservationBatch(ctx)
	require.NoError(t, err)

	obs := &oracletypes.Observation{}
	err = proto.Unmarshal(serialisedBatch, obs)
	require.NoError(t, err)

	require.NotContains(t, obs.PermanentlyExcludedRequestIds, "req-at-limit",
		"request at limit should NOT be excluded")
}
