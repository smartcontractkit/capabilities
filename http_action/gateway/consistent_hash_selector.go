package gateway

import (
	"stathat.com/c/consistent"
)

type consistentHashSelector struct {
	c *consistent.Consistent
}

func NewConsistentHashSelector(nodes []string) *consistentHashSelector {
	return &consistentHashSelector{
		c: setupRing(nodes),
	}
}

// MarkUnavailable removes a node from the consistent hash ring, marking it as unavailable.
// This is typically used when a node is temporarily down or should not be selected for requests.
func (s *consistentHashSelector) MarkUnavailable(node string) {
	s.c.Remove(node)
}

// Select returns the node selected by the consistent hash ring for the given request ID.
func (s *consistentHashSelector) Select(requestID string) (string, error) {
	return s.c.Get(requestID)
}

// Reset reinitializes the consistent hash ring with the provided nodes.
func (s *consistentHashSelector) Reset(nodes []string) {
	s.c = setupRing(nodes)
}

// Members returns the current members of the consistent hash ring.
func (s *consistentHashSelector) Members() []string {
	return s.c.Members()
}

// setupRing initializes a consistent hash ring with the provided nodes.
func setupRing(nodes []string) *consistent.Consistent {
	c := consistent.New()
	for _, node := range nodes {
		c.Add(node)
	}
	return c
}
