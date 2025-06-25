package gateway

import "stathat.com/c/consistent"

type consistentHashSelector struct {
	c *consistent.Consistent
}

func NewConsistentHashSelector(nodes []string) *consistentHashSelector {
	return &consistentHashSelector{
		c: setupRing(nodes),
	}
}

func (s *consistentHashSelector) MarkUnavailable(node string) {
	s.c.Remove(node)
}

func (s *consistentHashSelector) Select(requestID string) (string, error) {
	return s.c.Get(requestID)
}

func (s *consistentHashSelector) Reset(nodes []string) {
	s.c = setupRing(nodes)
}

func setupRing(nodes []string) *consistent.Consistent {
	c := consistent.New()
	for _, node := range nodes {
		c.Add(node)
	}
	return c
}
