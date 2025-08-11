package oracle

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

func TestQueryBatchHasCapacity_SizeEstimation(t *testing.T) {
	t.Run("size estimation accuracy", func(t *testing.T) {
		// Create test requests with varying sizes
		testCases := []struct {
			name    string
			request *oracletypes.Request
		}{
			{
				name: "minimal request",
				request: &oracletypes.Request{
					Metadata: &oracletypes.RequestMetaData{
						RequestId: "test-1",
					},
					RequestConsensusDescriptor: []byte("small"),
				},
			},
			{
				name: "medium request",
				request: &oracletypes.Request{
					Metadata: &oracletypes.RequestMetaData{
						RequestId:               "test-request-with-longer-id",
						WorkflowExecutionId:     "workflow-exec-123",
						WorkflowStepReference:   "step-ref-456",
						WorkflowId:              "workflow-789",
						WorkflowOwner:           "owner@example.com",
						WorkflowName:            "test-workflow",
						WorkflowDonId:           12345,
						WorkflowDonConfigVersion: 1,
						ReportId:                "report-abc",
						KeyBundleId:             "key-bundle-def",
						RequestType:             oracletypes.RequestType_VALUE_CONSENSUS,
					},
					RequestConsensusDescriptor: []byte("medium-sized-consensus-descriptor-data"),
				},
			},
			{
				name: "large request",
				request: &oracletypes.Request{
					Metadata: &oracletypes.RequestMetaData{
						RequestId:               "very-long-request-id-with-lots-of-characters-to-make-it-large",
						WorkflowExecutionId:     "very-long-workflow-execution-id-with-many-characters",
						WorkflowStepReference:   "very-long-workflow-step-reference-with-many-characters",
						WorkflowId:              "very-long-workflow-id-with-many-characters",
						WorkflowOwner:           "very-long-workflow-owner-email@example.com",
						WorkflowName:            "very-long-workflow-name-with-many-characters",
						WorkflowDonId:           4294967295, // max uint32
						WorkflowDonConfigVersion: 4294967295, // max uint32
						ReportId:                "very-long-report-id-with-many-characters",
						KeyBundleId:             "very-long-key-bundle-id-with-many-characters",
						RequestType:             oracletypes.RequestType_REPORT_GENERATION,
					},
					RequestConsensusDescriptor: make([]byte, 1000), // 1KB of data
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Test starting from empty query
				initialSize := 0
				maxSize := 10000 // 10KB limit

				// Get estimated size from our function
				hasCapacity, estimatedTotalSize := QueryBatchHasCapacity(initialSize, tc.request, maxSize)
				require.True(t, hasCapacity, "Should have capacity for single request")

				// Create actual query with the request and measure real size
				query := &oracletypes.Query{
					Requests: []*oracletypes.Request{tc.request},
				}
				actualSize := proto.Size(query)

				// The estimation should be very close to actual size
				// Allow for small differences due to protobuf encoding details
				sizeDiff := abs(estimatedTotalSize - actualSize)
				tolerance := max(actualSize/20, 5) // 5% tolerance or minimum 5 bytes
				
				t.Logf("Estimated size: %d, Actual size: %d, Difference: %d, Tolerance: %d", 
					estimatedTotalSize, actualSize, sizeDiff, tolerance)
				
				require.LessOrEqual(t, sizeDiff, tolerance, 
					"Size estimation should be within tolerance. Estimated: %d, Actual: %d, Diff: %d", 
					estimatedTotalSize, actualSize, sizeDiff)
			})
		}
	})

	t.Run("capacity limits respected", func(t *testing.T) {
		request := &oracletypes.Request{
			Metadata: &oracletypes.RequestMetaData{
				RequestId: "test",
			},
			RequestConsensusDescriptor: make([]byte, 100),
		}

		// First, calculate the actual size of the request to set realistic limits
		actualRequestSize := calculateRequestSize(request)
		t.Logf("Actual request size: %d bytes", actualRequestSize)

		// Test with limit smaller than request size
		smallLimit := actualRequestSize - 1
		hasCapacity, _ := QueryBatchHasCapacity(0, request, smallLimit)
		require.False(t, hasCapacity, "Should not have capacity when request exceeds limit")

		// Test with adequate limit
		largeLimit := actualRequestSize + 100
		hasCapacity, _ = QueryBatchHasCapacity(0, request, largeLimit)
		require.True(t, hasCapacity, "Should have capacity when request is within limit")

		// Test cumulative size checking - use current size that would cause overflow
		currentSize := largeLimit - actualRequestSize + 1
		hasCapacity, _ = QueryBatchHasCapacity(currentSize, request, largeLimit)
		require.False(t, hasCapacity, "Should not have capacity when cumulative size would exceed limit")
	})
}

func TestGetIDKey_DuplicateRecognition(t *testing.T) {
	t.Run("identical requests produce same key", func(t *testing.T) {
		metadata := ConsensusRequestMetadata{
			RequestMetadata: capabilities.RequestMetadata{
				WorkflowExecutionID: "exec-123",
				ReferenceID:         "ref-456",
				WorkflowID:          "workflow-789",
				WorkflowOwner:       "owner@example.com",
			},
		}

		value1Pb := values.Proto(values.NewString("value1"))
		value2Pb := values.Proto(values.NewString("value2"))

		request1 := &ConsensusRequest{
			Metadata: metadata,
			Input: &pb.SimpleConsensusInputs{
				// Different input data
				Observation: &pb.SimpleConsensusInputs_Value{
					Value: value1Pb,
				},
			},
		}

		request2 := &ConsensusRequest{
			Metadata: metadata,
			Input: &pb.SimpleConsensusInputs{
				// Different input data but same metadata
				Observation: &pb.SimpleConsensusInputs_Value{
					Value: value2Pb,
				},
			},
		}

		key1 := GetIDKey(request1)
		key2 := GetIDKey(request2)

		require.Equal(t, key1, key2, "Requests with same metadata should produce identical keys")
	})

	t.Run("different requests produce different keys", func(t *testing.T) {
		baseMetadata := capabilities.RequestMetadata{
			WorkflowExecutionID: "exec-123",
			ReferenceID:         "ref-456",
			WorkflowID:          "workflow-789",
			WorkflowOwner:       "owner@example.com",
		}

		testCases := []struct {
			name     string
			metadata ConsensusRequestMetadata
		}{
			{
				name: "different execution ID",
				metadata: ConsensusRequestMetadata{
					RequestMetadata: capabilities.RequestMetadata{
						WorkflowExecutionID: "exec-different",
						ReferenceID:         baseMetadata.ReferenceID,
						WorkflowID:          baseMetadata.WorkflowID,
						WorkflowOwner:       baseMetadata.WorkflowOwner,
					},
				},
			},
			{
				name: "different reference ID",
				metadata: ConsensusRequestMetadata{
					RequestMetadata: capabilities.RequestMetadata{
						WorkflowExecutionID: baseMetadata.WorkflowExecutionID,
						ReferenceID:         "ref-different",
						WorkflowID:          baseMetadata.WorkflowID,
						WorkflowOwner:       baseMetadata.WorkflowOwner,
					},
				},
			},
			{
				name: "different workflow ID",
				metadata: ConsensusRequestMetadata{
					RequestMetadata: capabilities.RequestMetadata{
						WorkflowExecutionID: baseMetadata.WorkflowExecutionID,
						ReferenceID:         baseMetadata.ReferenceID,
						WorkflowID:          "workflow-different",
						WorkflowOwner:       baseMetadata.WorkflowOwner,
					},
				},
			},
			{
				name: "different workflow owner",
				metadata: ConsensusRequestMetadata{
					RequestMetadata: capabilities.RequestMetadata{
						WorkflowExecutionID: baseMetadata.WorkflowExecutionID,
						ReferenceID:         baseMetadata.ReferenceID,
						WorkflowID:          baseMetadata.WorkflowID,
						WorkflowOwner:       "different-owner@example.com",
					},
				},
			},
		}

		baseRequest := &ConsensusRequest{
			Metadata: ConsensusRequestMetadata{RequestMetadata: baseMetadata},
		}
		baseKey := GetIDKey(baseRequest)

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				request := &ConsensusRequest{Metadata: tc.metadata}
				key := GetIDKey(request)
				
				require.NotEqual(t, baseKey, key, "Different metadata should produce different keys")
			})
		}
	})

	t.Run("duplicate detection in map", func(t *testing.T) {
		// Simulate the duplicate detection logic from plugin.go
		seenIds := make(map[idKey]bool)
		
		metadata := ConsensusRequestMetadata{
			RequestMetadata: capabilities.RequestMetadata{
				WorkflowExecutionID: "exec-123",
				ReferenceID:         "ref-456",
				WorkflowID:          "workflow-789",
				WorkflowOwner:       "owner@example.com",
			},
		}

		requests := []*ConsensusRequest{
			{Metadata: metadata, RequestID: "req-1"},
			{Metadata: metadata, RequestID: "req-2"}, // Same metadata, different ID
			{
				Metadata: ConsensusRequestMetadata{
					RequestMetadata: capabilities.RequestMetadata{
						WorkflowExecutionID: "exec-different",
						ReferenceID:         "ref-456",
						WorkflowID:          "workflow-789",
						WorkflowOwner:       "owner@example.com",
					},
				},
				RequestID: "req-3",
			}, // Different metadata
		}

		var processedRequests []*ConsensusRequest
		
		for _, rq := range requests {
			key := GetIDKey(rq)
			
			if seenIds[key] {
				continue // Skip duplicate
			}
			
			seenIds[key] = true
			processedRequests = append(processedRequests, rq)
		}

		require.Len(t, processedRequests, 2, "Should process 2 unique requests (first two are duplicates)")
		require.Equal(t, "req-1", processedRequests[0].RequestID, "First unique request should be processed")
		require.Equal(t, "req-3", processedRequests[1].RequestID, "Second unique request should be processed")
	})
}

// Helper functions
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
