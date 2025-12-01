package environment

import "github.com/smartcontractkit/capabilities/ring/internal/pb"

// Constants for ring configuration
const (
	NodesPerRing = 10
	F            = 3 // fault tolerance parameter
)

// Scaler interface provides the current desired state and health of rings
type Scaler interface {
	// Status returns the current health status of rings and desired ring count
	Status() *pb.Status
}

// SimpleScaler is a basic implementation of Scaler
type SimpleScaler struct {
	wantRings  uint32
	ringHealth map[uint32]bool
}

// NewSimpleScaler creates a new SimpleScaler
func NewSimpleScaler(wantRings uint32) *SimpleScaler {
	return &SimpleScaler{
		wantRings:  wantRings,
		ringHealth: make(map[uint32]bool),
	}
}

// Status returns the current status
func (s *SimpleScaler) Status() *pb.Status {
	return &pb.Status{
		WantRings: s.wantRings,
		Status:    s.ringHealth,
	}
}

// SetRingHealth updates the health status of a ring
func (s *SimpleScaler) SetRingHealth(ringID uint32, healthy bool) {
	s.ringHealth[ringID] = healthy
}

// SetWantRings updates the desired number of rings
func (s *SimpleScaler) SetWantRings(wantRings uint32) {
	s.wantRings = wantRings
}
