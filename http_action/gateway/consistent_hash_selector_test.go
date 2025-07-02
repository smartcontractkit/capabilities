package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
	"stathat.com/c/consistent"
)

func TestNewConsistentHashSelector(t *testing.T) {
	nodes := []string{"node1", "node2", "node3"}
	selector := NewConsistentHashSelector(nodes)

	if selector == nil || selector.c == nil {
		t.Fatal("Expected non-nil selector and ring")
	}

	require.Len(t, selector.Members(), len(nodes), "Expected ring to have same number of members as nodes")
}

func TestSelectReturnsConsistentNode(t *testing.T) {
	nodes := []string{"nodeA", "nodeB", "nodeC"}
	selector := NewConsistentHashSelector(nodes)

	requestID := "test-request-id"
	node1, err := selector.Select(requestID)
	require.NoError(t, err, "Expected no error when selecting node")

	node2, err := selector.Select(requestID)
	require.NoError(t, err, "Expected no error when selecting node again")

	require.Equal(t, node1, node2, "Expected same node for same request ID")
}

func TestMarkUnavailable(t *testing.T) {
	nodes := []string{"alpha", "beta", "gamma"}
	selector := NewConsistentHashSelector(nodes)

	// Choose a node that will be removed
	targetNode := "beta"
	selector.MarkUnavailable(targetNode)

	// beta should not be in the members list anymore
	members := selector.c.Members()
	require.NotContains(t, members, targetNode, "Expected node %s to be removed from the ring", targetNode)
}

func TestReset(t *testing.T) {
	initialNodes := []string{"n1", "n2", "n3"}
	newNodes := []string{"x", "y"}

	selector := NewConsistentHashSelector(initialNodes)
	selector.Reset(newNodes)

	members := selector.c.Members()
	require.Len(t, members, len(newNodes), "Expected members to match new nodes after reset")

	for _, expected := range newNodes {
		require.Contains(t, members, expected, "Expected member %s to be in the ring after reset", expected)
	}
}

func TestSelectFromEmptyRing(t *testing.T) {
	selector := NewConsistentHashSelector([]string{})
	_, err := selector.Select("any-request")
	require.Equal(t, err, consistent.ErrEmptyCircle, "Expected ErrEmptyCircle when selecting from an empty ring")
}
