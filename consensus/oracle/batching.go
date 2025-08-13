package oracle

import (
	"google.golang.org/protobuf/proto"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

// IDKey represents a unique identifier for a ConsensusRequest used for deduplication
type IDKey struct {
	workflowExecutionID string
	referenceID         string
	workflowID          string
	workflowOwner       string
}

// GetIDKey creates a unique identifier from a ConsensusRequest for deduplication
func GetIDKey(rq *ConsensusRequest) IDKey {
	return IDKey{
		workflowExecutionID: rq.Metadata.WorkflowExecutionID,
		referenceID:         rq.Metadata.ReferenceID,
		workflowID:          rq.Metadata.WorkflowID,
		workflowOwner:       rq.Metadata.WorkflowOwner,
	}
}

// varintSize calculates the size of a varint encoding
func varintSize(x uint64) int {
	switch {
	case x < 1<<7:
		return 1
	case x < 1<<14:
		return 2
	case x < 1<<21:
		return 3
	case x < 1<<28:
		return 4
	case x < 1<<35:
		return 5
	case x < 1<<42:
		return 6
	case x < 1<<49:
		return 7
	case x < 1<<56:
		return 8
	case x < 1<<63:
		return 9
	default:
		return 10
	}
}

// calculateRequestSize estimates the marshalled size of a Request
func calculateRequestSize(request *oracletypes.Request) int {
	if request == nil {
		return 0
	}

	// Use proto.Size which gives us the exact marshalled size
	return proto.Size(request)
}

// QueryBatchHasCapacity checks if adding a new request would exceed the size limit
func QueryBatchHasCapacity(cachedQuerySize int, request *oracletypes.Request, MaxQueryLengthBytes int) (bool, int) {
	// Calculate size if we add one more request
	newRequestSize := calculateRequestSize(request)

	// Add protobuf field overhead: tag (field number + wire type) + length prefix
	// For repeated fields in protobuf, each element gets:
	// - Tag: field number (1 for requests field) << 3 | wire type (2 for length-delimited)
	// - Length: varint encoding of the message size
	tagSize := varintSize(uint64(1<<3 | 2))
	if newRequestSize < 0 {
		return false, cachedQuerySize
	}
	lengthSize := varintSize(uint64(newRequestSize))

	totalSizeWithNewRequest := cachedQuerySize + tagSize + lengthSize + newRequestSize

	// Check against limits
	if totalSizeWithNewRequest > MaxQueryLengthBytes {
		// Stop adding more requests
		return false, cachedQuerySize
	}

	return true, totalSizeWithNewRequest
}

// CalculateObservationsMessageSize calculates the marshalled size of an Observation message
func CalculateObservationsMessageSize(obs *oracletypes.Observation) int {
	if obs == nil {
		return 0
	}

	// Use proto.Size which gives us the exact marshalled size
	return proto.Size(obs)
}

// calculateRequestObservationSize estimates the marshalled size of a RequestObservation
func calculateRequestObservationSize(requestObs *oracletypes.RequestObservation) int {
	if requestObs == nil {
		return 0
	}

	// Use proto.Size which gives us the exact marshalled size
	return proto.Size(requestObs)
}

// ObservationsBatchHasCapacity checks if adding a new RequestObservation would exceed the size limit
func ObservationsBatchHasCapacity(cachedObsSize int, newOb *oracletypes.RequestObservation, maxObservationLengthBytes int) (bool, int) {
	// Calculate size if we add one more observation
	newObservationSize := calculateRequestObservationSize(newOb)

	// Add protobuf field overhead: tag (field number + wire type) + length prefix
	// For repeated fields in protobuf, each element gets:
	// - Tag: field number (1 for observations field) << 3 | wire type (2 for length-delimited)
	// - Length: varint encoding of the message size
	tagSize := varintSize(uint64(1<<3 | 2))
	if newObservationSize < 0 {
		return false, cachedObsSize
	}
	lengthSize := varintSize(uint64(newObservationSize))

	totalSizeWithNewObservation := cachedObsSize + tagSize + lengthSize + newObservationSize

	// Check against limits
	if totalSizeWithNewObservation > maxObservationLengthBytes {
		// Stop adding more observations
		return false, cachedObsSize
	}

	return true, totalSizeWithNewObservation
}

// CalculateOutcomeMessageSize calculates the marshalled size of an Outcome message
func CalculateOutcomeMessageSize(outcome *oracletypes.Outcome) int {
	if outcome == nil {
		return 0
	}

	// Use proto.Size which gives us the exact marshalled size
	return proto.Size(outcome)
}

// calculateRequestOutcomeSize estimates the marshalled size of a RequestOutcome
func calculateRequestOutcomeSize(requestOutcome *oracletypes.RequestOutcome) int {
	if requestOutcome == nil {
		return 0
	}

	// Use proto.Size which gives us the exact marshalled size
	return proto.Size(requestOutcome)
}

// OutcomeBatchHasCapacity checks if adding a new RequestOutcome would exceed the size limit
func OutcomeBatchHasCapacity(cachedOutcomeSize int, newRequestOutcome *oracletypes.RequestOutcome, maxOutcomeLengthBytes int) (bool, int) {
	// Calculate size if we add one more outcome
	newOutcomeSize := calculateRequestOutcomeSize(newRequestOutcome)

	// Add protobuf field overhead: tag (field number + wire type) + length prefix
	// For repeated fields in protobuf, each element gets:
	// - Tag: field number (1 for outcomes field) << 3 | wire type (2 for length-delimited)
	// - Length: varint encoding of the message size
	tagSize := varintSize(uint64(1<<3 | 2))
	if newOutcomeSize < 0 {
		return false, cachedOutcomeSize
	}
	lengthSize := varintSize(uint64(newOutcomeSize))

	totalSizeWithNewOutcome := cachedOutcomeSize + tagSize + lengthSize + newOutcomeSize

	// Check against limits
	if totalSizeWithNewOutcome > maxOutcomeLengthBytes {
		// Stop adding more outcomes
		return false, cachedOutcomeSize
	}

	return true, totalSizeWithNewOutcome
}
