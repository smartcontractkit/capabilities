package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
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
						RequestId:                "test-request-with-longer-id",
						WorkflowExecutionId:      "workflow-exec-123",
						WorkflowStepReference:    "step-ref-456",
						WorkflowId:               "workflow-789",
						WorkflowOwner:            "owner@example.com",
						WorkflowName:             "test-workflow",
						WorkflowDonId:            12345,
						WorkflowDonConfigVersion: 1,
						ReportId:                 "report-abc",
						KeyBundleId:              "key-bundle-def",
						RequestType:              oracletypes.RequestType_VALUE_CONSENSUS,
					},
					RequestConsensusDescriptor: []byte("medium-sized-consensus-descriptor-data"),
				},
			},
			{
				name: "large request",
				request: &oracletypes.Request{
					Metadata: &oracletypes.RequestMetaData{
						RequestId:                "very-long-request-id-with-lots-of-characters-to-make-it-large",
						WorkflowExecutionId:      "very-long-workflow-execution-id-with-many-characters",
						WorkflowStepReference:    "very-long-workflow-step-reference-with-many-characters",
						WorkflowId:               "very-long-workflow-id-with-many-characters",
						WorkflowOwner:            "very-long-workflow-owner-email@example.com",
						WorkflowName:             "very-long-workflow-name-with-many-characters",
						WorkflowDonId:            4294967295, // max uint32
						WorkflowDonConfigVersion: 4294967295, // max uint32
						ReportId:                 "very-long-report-id-with-many-characters",
						KeyBundleId:              "very-long-key-bundle-id-with-many-characters",
						RequestType:              oracletypes.RequestType_REPORT_GENERATION,
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
				hasCapacity, estimatedTotalSize := BatchHasCapacity(initialSize, tc.request, maxSize, func() {}, func() {})
				require.True(t, hasCapacity, "Should have capacity for single request")

				// Create actual query with the request and measure real marshalled bytes length
				query := &oracletypes.Query{
					Requests: []*oracletypes.Request{tc.request},
				}
				marshalledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(query)
				require.NoError(t, err, "Failed to marshal query")
				actualMarshalledSize := len(marshalledBytes)

				t.Logf("Initial size: %d, Estimated total size: %d, Actual marshalled bytes length: %d",
					initialSize, estimatedTotalSize, actualMarshalledSize)

				// The estimation should be exactly equal to actual marshalled bytes length
				require.Equal(t, actualMarshalledSize, estimatedTotalSize,
					"Size estimation should be exactly equal to actual marshalled bytes length. Estimated: %d, Actual: %d",
					estimatedTotalSize, actualMarshalledSize)
			})
		}
	})

	t.Run("multiple requests deterministic size calculation", func(t *testing.T) {
		// Start with empty query
		initialSize := 0

		// Create multiple test requests with different sizes
		requests := []*oracletypes.Request{
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId: "req-1",
				},
				RequestConsensusDescriptor: []byte("small"),
			},
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId:                "req-2-with-longer-id",
					WorkflowExecutionId:      "workflow-exec-123",
					WorkflowStepReference:    "step-ref-456",
					WorkflowId:               "workflow-789",
					WorkflowOwner:            "owner@example.com",
					WorkflowName:             "test-workflow",
					WorkflowDonId:            12345,
					WorkflowDonConfigVersion: 1,
				},
				RequestConsensusDescriptor: []byte("medium-sized-consensus-descriptor"),
			},
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId: "req-3-large",
				},
				RequestConsensusDescriptor: make([]byte, 500), // Large descriptor
			},
		}

		var actualRequests []*oracletypes.Request
		currentSize := initialSize

		// Add requests one by one and verify size calculation at each step
		for i, newReq := range requests {
			// Calculate estimated size after adding this request
			maxSize := 10000 // Large enough limit
			hasCapacity, estimatedSize := BatchHasCapacity(currentSize, newReq, maxSize, func() {}, func() {})
			require.True(t, hasCapacity, "Should have capacity for request %d", i)

			// Actually add the request and calculate real marshalled bytes length
			actualRequests = append(actualRequests, newReq)
			queryWithAllData := &oracletypes.Query{Requests: actualRequests}
			marshalledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(queryWithAllData)
			require.NoError(t, err, "Failed to marshal query at step %d", i+1)
			actualSize := len(marshalledBytes)

			t.Logf("Step %d: Estimated size: %d, Actual marshalled bytes length: %d", i+1, estimatedSize, actualSize)

			// Verify exact match
			require.Equal(t, actualSize, estimatedSize,
				"Size estimation should be exactly equal at step %d. Estimated: %d, Actual: %d",
				i+1, estimatedSize, actualSize)

			// Update current size for next iteration
			currentSize = estimatedSize
		}
	})

	t.Run("capacity config respected", func(t *testing.T) {
		request := &oracletypes.Request{
			Metadata: &oracletypes.RequestMetaData{
				RequestId: "test",
			},
			RequestConsensusDescriptor: make([]byte, 100),
		}

		// First, calculate the actual size of the request to set realistic config
		actualRequestSize := CalculateMessageSize(request)
		t.Logf("Actual request size: %d bytes", actualRequestSize)

		// Test with limit smaller than request size
		smallLimit := actualRequestSize - 1
		hasCapacity, _ := BatchHasCapacity(0, request, smallLimit, func() {}, func() {})
		require.False(t, hasCapacity, "Should not have capacity when request exceeds limit")

		// Test with adequate limit
		largeLimit := actualRequestSize + 100
		hasCapacity, _ = BatchHasCapacity(0, request, largeLimit, func() {}, func() {})
		require.True(t, hasCapacity, "Should have capacity when request is within limit")

		// Test cumulative size checking - use current size that would cause overflow
		currentSize := largeLimit - actualRequestSize + 1
		hasCapacity, _ = BatchHasCapacity(currentSize, request, largeLimit, func() {}, func() {})
		require.False(t, hasCapacity, "Should not have capacity when cumulative size would exceed limit")
	})
}

func TestGetIDKey_DuplicateRecognition(t *testing.T) {
	t.Run("identical requests produce same key", func(t *testing.T) {
		metadata := oracle.ConsensusRequestMetadata{
			RequestMetadata: capabilities.RequestMetadata{
				WorkflowExecutionID: "exec-123",
				ReferenceID:         "ref-456",
				WorkflowID:          "workflow-789",
				WorkflowOwner:       "owner@example.com",
			},
		}

		value1Pb := values.Proto(values.NewString("value1"))
		value2Pb := values.Proto(values.NewString("value2"))

		request1 := &oracle.ConsensusRequest{
			Metadata: metadata,
			Input: &sdk.SimpleConsensusInputs{
				// Different input data
				Observation: &sdk.SimpleConsensusInputs_Value{
					Value: value1Pb,
				},
			},
		}

		request2 := &oracle.ConsensusRequest{
			Metadata: metadata,
			Input: &sdk.SimpleConsensusInputs{
				// Different input data but same metadata
				Observation: &sdk.SimpleConsensusInputs_Value{
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
			metadata oracle.ConsensusRequestMetadata
		}{
			{
				name: "different execution ID",
				metadata: oracle.ConsensusRequestMetadata{
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
				metadata: oracle.ConsensusRequestMetadata{
					RequestMetadata: capabilities.RequestMetadata{
						WorkflowExecutionID: baseMetadata.WorkflowExecutionID,
						ReferenceID:         "ref-different",
						WorkflowID:          baseMetadata.WorkflowID,
						WorkflowOwner:       baseMetadata.WorkflowOwner,
					},
				},
			},
		}

		baseRequest := &oracle.ConsensusRequest{
			Metadata: oracle.ConsensusRequestMetadata{RequestMetadata: baseMetadata},
		}
		baseKey := GetIDKey(baseRequest)

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				request := &oracle.ConsensusRequest{Metadata: tc.metadata}
				key := GetIDKey(request)

				require.NotEqual(t, baseKey, key, "Different metadata should produce different keys")
			})
		}
	})

	t.Run("duplicate detection in map", func(t *testing.T) {
		// Simulate the duplicate detection logic from plugin.go
		seenIDs := make(map[IDKey]bool)

		metadata := oracle.ConsensusRequestMetadata{
			RequestMetadata: capabilities.RequestMetadata{
				WorkflowExecutionID: "exec-123",
				ReferenceID:         "ref-456",
				WorkflowID:          "workflow-789",
				WorkflowOwner:       "owner@example.com",
			},
		}

		requests := []*oracle.ConsensusRequest{
			{Metadata: metadata, RequestID: "req-1"},
			{Metadata: metadata, RequestID: "req-2"}, // Same metadata, different ID
			{
				Metadata: oracle.ConsensusRequestMetadata{
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

		var processedRequests []*oracle.ConsensusRequest

		for _, rq := range requests {
			key := GetIDKey(rq)

			if seenIDs[key] {
				continue // Skip duplicate
			}

			seenIDs[key] = true
			processedRequests = append(processedRequests, rq)
		}

		require.Len(t, processedRequests, 2, "Should process 2 unique requests (first two are duplicates)")
		require.Equal(t, "req-1", processedRequests[0].RequestID, "First unique request should be processed")
		require.Equal(t, "req-3", processedRequests[1].RequestID, "Second unique request should be processed")
	})
}

func TestObservationsBatchHasCapacity_SizeEstimation(t *testing.T) {
	t.Run("observations size estimation accuracy", func(t *testing.T) {
		// Create test observation message
		obs := &oracletypes.Observation{
			Observations: []*oracletypes.RequestObservation{},
		}

		// Test initial size calculation
		initialSize := CalculateMessageSize(obs)
		require.GreaterOrEqual(t, initialSize, 0, "Empty observation should have non-negative size")

		// Create a test RequestObservation
		requestObs := &oracletypes.RequestObservation{
			Metadata: &oracletypes.RequestMetaData{
				RequestId:           "test-request-123",
				WorkflowExecutionId: "exec-456",
			},
			Observation: []byte("test-observation-data"),
		}

		// Test capacity checking
		maxSize := 1000
		hasCapacity, estimatedNewSize := BatchHasCapacity(initialSize, requestObs, maxSize, func() {}, func() {})
		require.True(t, hasCapacity, "Should have capacity for reasonable-sized observation")
		require.Greater(t, estimatedNewSize, initialSize, "New size should be larger than initial size")

		// Create actual observation with the request observation and calculate real marshalled bytes length
		obsWithData := &oracletypes.Observation{
			Observations: []*oracletypes.RequestObservation{requestObs},
		}
		marshalledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(obsWithData)
		require.NoError(t, err, "Failed to marshal observation")
		actualMarshalledSize := len(marshalledBytes)

		t.Logf("Initial size: %d, Estimated new size: %d, Actual marshalled bytes length: %d",
			initialSize, estimatedNewSize, actualMarshalledSize)

		// The estimation should be exactly equal to actual marshalled bytes length
		require.Equal(t, actualMarshalledSize, estimatedNewSize,
			"Size estimation should be exactly equal to actual marshalled bytes length. Estimated: %d, Actual: %d",
			estimatedNewSize, actualMarshalledSize)
	})

	t.Run("multiple observations deterministic size calculation", func(t *testing.T) {
		// Start with empty observation
		obs := &oracletypes.Observation{Observations: []*oracletypes.RequestObservation{}}
		currentSize := CalculateMessageSize(obs)

		// Create multiple test RequestObservations with different sizes
		observations := []*oracletypes.RequestObservation{
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId:           "req-1",
					WorkflowExecutionId: "exec-1",
				},
				Observation: []byte("small"),
			},
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId:                "req-2-with-longer-id",
					WorkflowExecutionId:      "exec-2-longer",
					WorkflowStepReference:    "step-ref",
					WorkflowId:               "workflow-id",
					WorkflowOwner:            "owner@example.com",
					WorkflowName:             "test-workflow",
					WorkflowDonId:            12345,
					WorkflowDonConfigVersion: 1,
				},
				Observation: []byte("medium-sized-observation-data"),
			},
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId: "req-3",
				},
				Observation: make([]byte, 200), // Large observation
			},
		}

		var actualObservations []*oracletypes.RequestObservation

		// Add observations one by one and verify size calculation at each step
		for i, newObs := range observations {
			// Calculate estimated size after adding this observation
			maxSize := 10000 // Large enough limit
			hasCapacity, estimatedSize := BatchHasCapacity(currentSize, newObs, maxSize, func() {}, func() {})
			require.True(t, hasCapacity, "Should have capacity for observation %d", i)

			// Actually add the observation and calculate real marshalled bytes length
			actualObservations = append(actualObservations, newObs)
			obsWithAllData := &oracletypes.Observation{Observations: actualObservations}
			marshalledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(obsWithAllData)
			require.NoError(t, err, "Failed to marshal observation at step %d", i+1)
			actualSize := len(marshalledBytes)

			t.Logf("Step %d: Estimated size: %d, Actual marshalled bytes length: %d", i+1, estimatedSize, actualSize)

			// Verify exact match
			require.Equal(t, actualSize, estimatedSize,
				"Size estimation should be exactly equal at step %d. Estimated: %d, Actual: %d",
				i+1, estimatedSize, actualSize)

			// Update current size for next iteration
			currentSize = estimatedSize
		}
	})

	t.Run("observations capacity config respected", func(t *testing.T) {
		obs := &oracletypes.Observation{Observations: []*oracletypes.RequestObservation{}}
		initialSize := CalculateMessageSize(obs)

		// Create a large observation
		largeObs := &oracletypes.RequestObservation{
			Metadata: &oracletypes.RequestMetaData{
				RequestId: "test",
			},
			Observation: make([]byte, 500), // 500 bytes of data
		}

		actualObsSize := CalculateMessageSize(largeObs)
		t.Logf("Large observation size: %d bytes", actualObsSize)

		// Test with limit smaller than observation size
		smallLimit := actualObsSize - 1
		hasCapacity, _ := BatchHasCapacity(initialSize, largeObs, smallLimit, func() {}, func() {})
		require.False(t, hasCapacity, "Should not have capacity when observation would exceed limit")

		// Test with adequate limit
		largeLimit := initialSize + actualObsSize + 100
		hasCapacity, _ = BatchHasCapacity(initialSize, largeObs, largeLimit, func() {}, func() {})
		require.True(t, hasCapacity, "Should have capacity when observation is within limit")
	})
}

func TestOutcomeBatchHasCapacity_SizeEstimation(t *testing.T) {
	t.Run("outcome size estimation accuracy", func(t *testing.T) {
		// Create test outcome message
		outcome := &oracletypes.Outcome{
			Outcomes: []*oracletypes.RequestOutcome{},
		}

		// Test initial size calculation
		initialSize := CalculateMessageSize(outcome)
		require.GreaterOrEqual(t, initialSize, 0, "Empty outcome should have non-negative size")

		// Create a test RequestOutcome
		requestOutcome := &oracletypes.RequestOutcome{
			Metadata: &oracletypes.RequestMetaData{
				RequestId:           "test-request-123",
				WorkflowExecutionId: "exec-456",
			},
			Outcome: []byte("test-outcome-data"),
		}

		// Test capacity checking
		maxSize := 1000
		hasCapacity, estimatedNewSize := BatchHasCapacity(initialSize, requestOutcome, maxSize, func() {}, func() {})
		require.True(t, hasCapacity, "Should have capacity for reasonable-sized outcome")
		require.Greater(t, estimatedNewSize, initialSize, "New size should be larger than initial size")

		// Create actual outcome with the request outcome and calculate real marshalled bytes length
		outcomeWithData := &oracletypes.Outcome{
			Outcomes: []*oracletypes.RequestOutcome{requestOutcome},
		}
		marshalledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(outcomeWithData)
		require.NoError(t, err, "Failed to marshal outcome")
		actualMarshalledSize := len(marshalledBytes)

		t.Logf("Initial size: %d, Estimated new size: %d, Actual marshalled bytes length: %d",
			initialSize, estimatedNewSize, actualMarshalledSize)

		// The estimation should be exactly equal to actual marshalled bytes length
		require.Equal(t, actualMarshalledSize, estimatedNewSize,
			"Size estimation should be exactly equal to actual marshalled bytes length. Estimated: %d, Actual: %d",
			estimatedNewSize, actualMarshalledSize)
	})

	t.Run("multiple outcomes deterministic size calculation", func(t *testing.T) {
		// Start with empty outcome
		outcome := &oracletypes.Outcome{Outcomes: []*oracletypes.RequestOutcome{}}
		currentSize := CalculateMessageSize(outcome)

		// Create multiple test RequestOutcomes with different sizes
		outcomes := []*oracletypes.RequestOutcome{
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId:           "req-1",
					WorkflowExecutionId: "exec-1",
				},
				Outcome: []byte("small"),
			},
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId:                "req-2-with-longer-id",
					WorkflowExecutionId:      "exec-2-longer",
					WorkflowStepReference:    "step-ref",
					WorkflowId:               "workflow-id",
					WorkflowOwner:            "owner@example.com",
					WorkflowName:             "test-workflow",
					WorkflowDonId:            12345,
					WorkflowDonConfigVersion: 1,
				},
				Outcome: []byte("medium-sized-outcome-data"),
			},
			{
				Metadata: &oracletypes.RequestMetaData{
					RequestId: "req-3",
				},
				Outcome: make([]byte, 200), // Large outcome
			},
		}

		var actualOutcomes []*oracletypes.RequestOutcome

		// Add outcomes one by one and verify size calculation at each step
		for i, newOutcome := range outcomes {
			// Calculate estimated size after adding this outcome
			maxSize := 10000 // Large enough limit
			hasCapacity, estimatedSize := BatchHasCapacity(currentSize, newOutcome, maxSize, func() {}, func() {})
			require.True(t, hasCapacity, "Should have capacity for outcome %d", i)

			// Actually add the outcome and calculate real marshalled bytes length
			actualOutcomes = append(actualOutcomes, newOutcome)
			outcomeWithAllData := &oracletypes.Outcome{Outcomes: actualOutcomes}
			marshalledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(outcomeWithAllData)
			require.NoError(t, err, "Failed to marshal outcome at step %d", i+1)
			actualSize := len(marshalledBytes)

			t.Logf("Step %d: Estimated size: %d, Actual marshalled bytes length: %d", i+1, estimatedSize, actualSize)

			// Verify exact match
			require.Equal(t, actualSize, estimatedSize,
				"Size estimation should be exactly equal at step %d. Estimated: %d, Actual: %d",
				i+1, estimatedSize, actualSize)

			// Update current size for next iteration
			currentSize = estimatedSize
		}
	})

	t.Run("outcome capacity config respected", func(t *testing.T) {
		outcome := &oracletypes.Outcome{Outcomes: []*oracletypes.RequestOutcome{}}
		initialSize := CalculateMessageSize(outcome)

		// Create a large outcome
		largeOutcome := &oracletypes.RequestOutcome{
			Metadata: &oracletypes.RequestMetaData{
				RequestId: "test",
			},
			Outcome: make([]byte, 500), // 500 bytes of data
		}

		actualOutcomeSize := CalculateMessageSize(largeOutcome)
		t.Logf("Large outcome size: %d bytes", actualOutcomeSize)

		// Test with limit smaller than outcome size
		smallLimit := actualOutcomeSize - 1
		hasCapacity, _ := BatchHasCapacity(initialSize, largeOutcome, smallLimit, func() {}, func() {})
		require.False(t, hasCapacity, "Should not have capacity when outcome would exceed limit")

		// Test with adequate limit
		largeLimit := initialSize + actualOutcomeSize + 100
		hasCapacity, _ = BatchHasCapacity(initialSize, largeOutcome, largeLimit, func() {}, func() {})
		require.True(t, hasCapacity, "Should have capacity when outcome is within limit")
	})
}
