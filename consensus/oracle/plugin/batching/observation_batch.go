package batching

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

type ObservationBatch struct {
	oracletypes.Observation

	lggr                          logger.Logger
	currentSerialisedBatchSize    int
	metrics                       metrics
	maxObservationLengthBytes     int
	permanentlyExcludedRequestIDs []string // Track requests permanently excluded due to size
}

func NewObservationBatch(ctx context.Context, lggr logger.Logger, maxObservationLengthBytes int, metrics metrics) *ObservationBatch {
	metrics.IncBatchRequestsTotal(ctx, "observation")
	observations := make(map[string]*oracletypes.RequestObservation)
	obs := &oracletypes.Observation{Observations: observations}
	messageSize := calculateMessageSize(obs)

	return &ObservationBatch{
		Observation:                   oracletypes.Observation{Observations: observations},
		lggr:                          lggr,
		currentSerialisedBatchSize:    messageSize,
		maxObservationLengthBytes:     maxObservationLengthBytes,
		metrics:                       metrics,
		permanentlyExcludedRequestIDs: []string{},
	}
}

func (ob *ObservationBatch) AddObservation(ctx context.Context, reqObs *oracletypes.RequestObservation) bool {
	// Adding an entry to a map is the same as adding a key-value pair to a slice for size calculation purposes
	mapEntry := oracletypes.ObservationMapEntry{
		Key:   reqObs.Metadata.RequestId,
		Value: reqObs,
	}

	mapEntrySize := proto.Size(&mapEntry)
	ok, newSize := batchHasCapacityForSliceBytes(ob.currentSerialisedBatchSize, mapEntrySize, ob.maxObservationLengthBytes)

	if !ok {
		ob.metrics.IncBatchCapacityExceeded(ctx, "observation")

		// Check if observation would fit in empty batch
		// Similar to outcome batch pattern: check against initial empty batch size
		emptyBatch := &oracletypes.Observation{Observations: make(map[string]*oracletypes.RequestObservation)}
		initialBatchOverheadSize := calculateMessageSize(emptyBatch)
		fitsInEmptyBatch, _ := batchHasCapacityForSliceBytes(initialBatchOverheadSize, mapEntrySize, ob.maxObservationLengthBytes)

		if !fitsInEmptyBatch {
			// Observation is permanently too large - will never fit, even in empty batch
			ob.permanentlyExcludedRequestIDs = append(ob.permanentlyExcludedRequestIDs, reqObs.Metadata.RequestId)
			ob.lggr.Errorw("observation permanently too large - will be marked as failed in outcome phase",
				"requestID", reqObs.Metadata.RequestId,
				"observationSize", mapEntrySize,
				"maxObservationLengthBytes", ob.maxObservationLengthBytes,
				"initialBatchOverheadSize", initialBatchOverheadSize)
			return false
		}

		// Would fit in empty batch - just retry next round when batch is empty
		return false
	}

	ob.currentSerialisedBatchSize = newSize
	ob.Observations[reqObs.Metadata.RequestId] = reqObs

	return true
}

func (ob *ObservationBatch) CurrentSerialisedBatchSize() int {
	return ob.currentSerialisedBatchSize
}

func (ob *ObservationBatch) SerialiseObservationBatch(ctx context.Context) ([]byte, error) {
	// Include permanently excluded request IDs in observation metadata
	ob.Observation.PermanentlyExcludedRequestIds = ob.permanentlyExcludedRequestIDs

	serialisedBatch, err := proto.MarshalOptions{Deterministic: true}.Marshal(&ob.Observation)
	if err != nil {
		return nil, fmt.Errorf("failed to serialise batch of observations: %w", err)
	}

	allIDs := make([]string, 0, len(ob.Observations))
	for id := range ob.Observations {
		allIDs = append(allIDs, id)
	}

	batchSize := len(serialisedBatch)
	ob.metrics.RecordObservationBatchSize(ctx, float64(batchSize))
	ob.lggr.Debugw("serialised observation batch", "numObservations", len(ob.Observations), "actualSizeBytes", batchSize,
		"calculatedSizeBytes", ob.currentSerialisedBatchSize,
		"maxObservationLengthBytes", ob.maxObservationLengthBytes,
		"executionIDs", allIDs,
	)

	return serialisedBatch, nil
}

func (ob *ObservationBatch) NumObservationsInBatch() int {
	return len(ob.Observations)
}
