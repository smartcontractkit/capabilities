package oracle

import (
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
	"google.golang.org/protobuf/proto"
)

// idKey represents a unique identifier for a ConsensusRequest used for deduplication
type idKey struct {
	workflowExecutionID string
	referenceID         string
	workflowID          string
	workflowOwner       string
}

// GetIDKey creates a unique identifier from a ConsensusRequest for deduplication
func GetIDKey(rq *ConsensusRequest) idKey {
	return idKey{
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
	lengthSize := varintSize(uint64(newRequestSize))

	totalSizeWithNewRequest := cachedQuerySize + tagSize + lengthSize + newRequestSize

	// Check against limits
	if totalSizeWithNewRequest > MaxQueryLengthBytes {
		// Stop adding more requests
		return false, cachedQuerySize
	}

	return true, totalSizeWithNewRequest
}
