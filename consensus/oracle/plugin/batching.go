package plugin

import (
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
)

// IDKey represents a unique identifier for a ConsensusRequest used for deduplication
type IDKey struct {
	workflowExecutionID string
	referenceID         string
}

// GetIDKey creates a unique identifier from a ConsensusRequest for deduplication
func GetIDKey(rq *oracle.ConsensusRequest) IDKey {
	return IDKey{
		workflowExecutionID: rq.Metadata.WorkflowExecutionID,
		referenceID:         rq.Metadata.ReferenceID,
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

// CalculateMessageSize calculates the marshalled size of any proto message
func CalculateMessageSize(message proto.Message) int {
	if message == nil {
		return 0
	}

	// Use proto.Size which gives us the exact marshalled size
	return proto.Size(message)
}

// BatchHasCapacity checks if adding a new proto message would exceed the size limit
func BatchHasCapacity(cachedSize int, message proto.Message, maxSizeBytes int, incBatchRequestsMetric func(),
	incBatchSizeExceededMetric func()) (bool, int) {
	incBatchRequestsMetric()

	// Calculate size if we add one more message
	newMessageSize := proto.Size(message)

	// Add protobuf field overhead: tag (field number + wire type) + length prefix
	// For repeated fields in protobuf, each element gets:
	// - Tag: field number (1 for the repeated field) << 3 | wire type (2 for length-delimited)
	// - Length: varint encoding of the message size
	tagSize := varintSize(uint64(1<<3 | 2))
	if newMessageSize < 0 {
		return false, cachedSize
	}
	lengthSize := varintSize(uint64(newMessageSize))

	totalSizeWithNewMessage := cachedSize + tagSize + lengthSize + newMessageSize

	// Check against config
	if totalSizeWithNewMessage > maxSizeBytes {
		incBatchSizeExceededMetric()
		// Stop adding more messages
		return false, cachedSize
	}

	return true, totalSizeWithNewMessage
}
