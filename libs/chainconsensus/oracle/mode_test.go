package oracle

import (
	"iter"
	"testing"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/stretchr/testify/require"
)

// stringObs returns an iterator over the given values, attributed to sequential oracle IDs.
// An empty string produces a nil observation (counts toward total but not toward any key).
func stringObs(values ...string) iter.Seq2[commontypes.OracleID, *observation[string, string]] {
	return func(yield func(commontypes.OracleID, *observation[string, string]) bool) {
		for i, v := range values {
			//nolint:gosec
			id := commontypes.OracleID(i)
			if v == "" {
				if !yield(id, nil) {
					return
				}
				continue
			}
			if !yield(id, &observation[string, string]{Key: v, Value: v}) {
				return
			}
		}
	}
}

func TestMode_DefaultFPlusOne(t *testing.T) {
	// N=7, F=2 → byzQuorumSize=5, default minMatching=F+1=3
	N, F := 7, 2
	got, actualCount, err := mode[string, string](N, F, F+1, stringObs("a", "a", "a", "b", "b", "c", ""))
	require.NoError(t, err)
	require.Equal(t, "a", got)
	require.Equal(t, 3, actualCount)
}

func TestMode_DefaultFPlusOne_InsufficientMatching(t *testing.T) {
	// N=7, F=2 → byzQuorumSize=5; default minMatching=F+1=3
	N, F := 7, 2
	_, actualCount, err := mode[string, string](N, F, F+1, stringObs("a", "a", "b", "b", "c", "c", "d"))
	require.ErrorContains(t, err, "insufficient number of identical observations: expected 3, got 2")
	require.Equal(t, 2, actualCount)
}

func TestMode_TwoFPlusOne(t *testing.T) {
	// N=7, F=2 → minMatching=5; 5 agree on "a"
	N, F := 7, 2
	got, actualCount, err := mode[string, string](N, F, 2*F+1, stringObs("a", "a", "a", "a", "a", "b", "c"))
	require.NoError(t, err)
	require.Equal(t, "a", got)
	require.Equal(t, 5, actualCount)
}

func TestMode_TwoFPlusOne_InsufficientMatching(t *testing.T) {
	// N=7, F=2 → minMatching=5; only 4 agree on "a" → fail
	N, F := 7, 2
	_, actualCount, err := mode[string, string](N, F, 2*F+1, stringObs("a", "a", "a", "a", "b", "b", "c"))
	require.ErrorContains(t, err, "insufficient number of identical observations: expected 5, got 4")
	require.Equal(t, 4, actualCount)
}

func TestMode_InsufficientTotalObservations(t *testing.T) {
	// N=7, F=2 → byzQuorumSize=5; only 4 provided → fail before matching check
	N, F := 7, 2
	_, actualCount, err := mode[string, string](N, F, F+1, stringObs("a", "a", "a", "a"))
	require.ErrorContains(t, err, "insufficient number of observations: expected 5, got 4")
	require.Equal(t, 0, actualCount)
}
