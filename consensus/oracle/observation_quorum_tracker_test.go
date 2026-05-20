package oracle

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObservationQuorumTracker_RecordAndReachedQuorum(t *testing.T) {
	t.Parallel()

	tracker := NewObservationQuorumTracker()
	const requestID = "exec-1-01"
	const threshold = 5

	tracker.Record(requestID, 2, threshold)
	require.False(t, tracker.ReachedQuorum(requestID))
	require.Equal(t, 2, tracker.MaxObservations(requestID))

	tracker.Record(requestID, 4, threshold)
	require.False(t, tracker.ReachedQuorum(requestID))
	require.Equal(t, 4, tracker.MaxObservations(requestID))

	tracker.Record(requestID, 5, threshold)
	require.True(t, tracker.ReachedQuorum(requestID))

	tracker.Record(requestID, 3, threshold)
	require.True(t, tracker.ReachedQuorum(requestID))
	require.Equal(t, 5, tracker.MaxObservations(requestID))
}

func TestObservationQuorumTracker_Forget(t *testing.T) {
	t.Parallel()

	tracker := NewObservationQuorumTracker()
	const requestID = "exec-1-01"

	tracker.Record(requestID, 5, 5)
	require.True(t, tracker.ReachedQuorum(requestID))

	tracker.Forget(requestID)
	require.False(t, tracker.ReachedQuorum(requestID))
	require.Equal(t, 0, tracker.MaxObservations(requestID))
}

func TestObservationQuorumTracker_NilSafe(t *testing.T) {
	t.Parallel()

	var tracker *ObservationQuorumTracker
	require.NotPanics(t, func() {
		tracker.Record("id", 1, 2)
		require.False(t, tracker.ReachedQuorum("id"))
		require.Equal(t, 0, tracker.MaxObservations("id"))
		tracker.Forget("id")
	})
}
