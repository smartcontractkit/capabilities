package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

// Helper function to create a test requestCtx
func createTestRequest(t *testing.T, id string, attempt int) *requestCtx {
	ctx, cancel := context.WithCancel(t.Context())
	request := types.NewEventuallyConsistentRequest(id, nil)
	return &requestCtx{
		Request:    request,
		Ctx:        ctx,
		Cancel:     cancel,
		ResultChan: make(chan []byte, 1),
		Attempt:    attempt,
	}
}

func TestPriorityQueuePushAndPop(t *testing.T) {
	queue := newPriorityQueue()

	require.Equal(t, 0, queue.Len(), "Queue should be empty")

	// Push items
	req1 := createTestRequest(t, "req1", 1)
	req2 := createTestRequest(t, "req2", 2)
	req3 := createTestRequest(t, "req3", 3)

	queue.Push(req1)
	require.Equal(t, 1, queue.Len(), "Queue should have 1 item")

	queue.Push(req2)
	queue.Push(req3)
	require.Equal(t, 3, queue.Len(), "Queue should have 3 items")

	// Lower attempt requests should be prioritized
	item := queue.Peek()
	require.Equal(t, req1, item, "Lower attempt item should be popped first")
	require.Equal(t, 3, queue.Len(), "Queue should remain 3 items after peek")

	item = queue.Pop()
	require.Equal(t, req1, item, "Lower attempt item should be popped first")
	require.Equal(t, 2, queue.Len(), "Queue should have 2 items after pop")

	item = queue.Pop()
	require.Equal(t, req2, item, "Second lowest attempt item should be popped next")
	require.Equal(t, 1, queue.Len(), "Queue should have 1 item after second pop")

	item = queue.Pop()
	require.Equal(t, req3, item, "Last item should be popped last")
	require.Equal(t, 0, queue.Len(), "Queue should be empty after all pops")
}

func TestPriorityQueueIncreaseAttempt(t *testing.T) {
	queue := newPriorityQueue()

	req1 := createTestRequest(t, "req1", 1)
	req2 := createTestRequest(t, "req2", 2)

	queue.Push(req1)
	queue.Push(req2)

	// Initially req1 has higher priority
	require.Equal(t, req1, queue.Peek(), "req1 should be lower priority")

	// Increase req1's attempt to make it lower priority
	queue.IncreaseAttempt("req1")
	queue.IncreaseAttempt("req1")
	require.Equal(t, 3, req1.Attempt, "Attempt should be increased by 2")
	require.Equal(t, req2, queue.Peek(), "req2 should now be highest priority")

	// Non-existent ID should not cause errors
	queue.IncreaseAttempt("non-existent")
	require.Equal(t, req2, queue.Peek(), "req2 should remain the highest priority")
	require.Equal(t, 2, queue.Len(), "Queue length should remain unchanged")
}

func TestPriorityQueueGetByID(t *testing.T) {
	queue := newPriorityQueue()

	req1 := createTestRequest(t, "req1", 1)
	queue.Push(req1)

	// Get existing item
	item, found := queue.GetByID("req1")
	require.True(t, found, "Should find existing item")
	require.Equal(t, req1, item, "Should return correct item")
	require.Equal(t, 1, queue.Len(), "GetByID should not remove items")

	// Get non-existent item
	item, found = queue.GetByID("non-existent")
	require.False(t, found, "Should not find non-existent item")
	require.Nil(t, item, "Should return nil for non-existent item")
}

func TestPriorityQueueRemove(t *testing.T) {
	queue := newPriorityQueue()

	req1 := createTestRequest(t, "req1", 1)
	req2 := createTestRequest(t, "req2", 2)

	queue.Push(req1)
	queue.Push(req2)
	require.Equal(t, 2, queue.Len(), "Queue should have 2 items")

	// Remove existing item
	item, found := queue.Remove("req1")
	require.True(t, found, "Should find and remove existing item")
	require.Equal(t, req1, item, "Should return removed item")
	require.Equal(t, 1, queue.Len(), "Queue should have 1 item after remove")

	// Try to get removed item
	_, found = queue.GetByID("req1")
	require.False(t, found, "Should not find removed item")

	// Remove non-existent item
	item, found = queue.Remove("non-existent")
	require.False(t, found, "Should not find non-existent item")
	require.Nil(t, item, "Should return nil for non-existent item")
	require.Equal(t, 1, queue.Len(), "Queue length should remain unchanged")
}

func TestPriorityQueueOrdering(t *testing.T) {
	queue := newPriorityQueue()

	// Create items with different attempts
	items := []*requestCtx{
		createTestRequest(t, "req_attempt_3", 3),
		createTestRequest(t, "req_attempt_1", 1),
		createTestRequest(t, "req_attempt_5", 5),
		createTestRequest(t, "req_attempt_2", 2),
		createTestRequest(t, "req_attempt_4", 4),
	}

	// Push all items
	for _, item := range items {
		queue.Push(item)
	}

	require.Equal(t, 5, queue.Len(), "Queue should have 5 items")

	// Pop items and verify they come out in descending attempt order
	expected := []string{"req_attempt_1", "req_attempt_2", "req_attempt_3", "req_attempt_4", "req_attempt_5"}
	for i, expectedID := range expected {
		item := queue.Pop()
		require.Equal(t, expectedID, item.ID(), "Item %d should have ID %s", i, expectedID)
	}

	require.Equal(t, 0, queue.Len(), "Queue should be empty after all pops")
}

func TestEmptyQueueOperations(t *testing.T) {
	queue := newPriorityQueue()

	// These operations should not panic on an empty queue
	_, found := queue.GetByID("any")
	require.False(t, found, "GetByID on empty queue should return not found")

	_, found = queue.Remove("any")
	require.False(t, found, "Remove on empty queue should return not found")

	queue.IncreaseAttempt("any")
	// No assertion needed, just making sure it doesn't panic
}

func TestPriorityQueueResourceCleanup(t *testing.T) {
	queue := newPriorityQueue()

	// Create and push request
	req1 := createTestRequest(t, "req1", 1)
	queue.Push(req1)

	// Remove it
	_, found := queue.Remove("req1")
	require.True(t, found, "Should find and remove the item")

	// Verify idToIndex map was cleaned up
	require.Equal(t, 0, len(queue.queue.idToIndex), "idToIndex map should be empty after removal")

	// Push and pop to verify cleanup
	req2 := createTestRequest(t, "req2", 1)
	queue.Push(req2)
	_ = queue.Pop()
	require.Equal(t, 0, len(queue.queue.idToIndex), "idToIndex map should be empty after pop")
}
