package oracle

import "sync"

// ObservationQuorumTracker records whether a request has ever received enough attributed
// observations in an Outcome round to attempt consensus (2f+1). Used when a request times out
// to distinguish insufficient observations from other timeout causes.
type ObservationQuorumTracker struct {
	mu              sync.Mutex
	reachedQuorum   map[string]bool
	maxObservations map[string]int
}

func NewObservationQuorumTracker() *ObservationQuorumTracker {
	return &ObservationQuorumTracker{
		reachedQuorum:   make(map[string]bool),
		maxObservations: make(map[string]int),
	}
}

// Record updates the highest observation count seen for requestID. quorumThreshold is the
// minimum count required to calculate an outcome (2f+1 for this capability).
func (t *ObservationQuorumTracker) Record(requestID string, observationCount, quorumThreshold int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if observationCount > t.maxObservations[requestID] {
		t.maxObservations[requestID] = observationCount
	}
	if observationCount >= quorumThreshold {
		t.reachedQuorum[requestID] = true
	}
}

// ReachedQuorum reports whether requestID has ever met the observation quorum threshold.
func (t *ObservationQuorumTracker) ReachedQuorum(requestID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reachedQuorum[requestID]
}

// MaxObservations returns the highest observation count recorded for requestID.
func (t *ObservationQuorumTracker) MaxObservations(requestID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxObservations[requestID]
}

// Forget removes tracking state for a completed or timed-out request.
func (t *ObservationQuorumTracker) Forget(requestID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.reachedQuorum, requestID)
	delete(t.maxObservations, requestID)
}
