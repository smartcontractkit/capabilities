package oracle

import (
	"testing"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func TestErrorMode(t *testing.T) {
	const requestID = "req-1"
	const requestNotPresent = "request-not-present"
	const requestIsNil = "request-is-nil"
	const nonError = "non-error"
	const emptyError = "empty-error"
	strToObservation := func(newSuccessOb, newErrorOb func(ob string) *types.RequestObservation, ob string) *types.Observation {
		switch ob {
		case requestNotPresent:
			return &types.Observation{
				Observations: map[string]*types.RequestObservation{},
			}
		case requestIsNil:
			return &types.Observation{
				Observations: map[string]*types.RequestObservation{
					requestID: nil,
				},
			}
		case nonError:
			return &types.Observation{
				Observations: map[string]*types.RequestObservation{
					requestID: newSuccessOb(ob),
				},
			}
		case emptyError:
			ob = ""
			fallthrough
		default:
			return &types.Observation{
				Observations: map[string]*types.RequestObservation{
					requestID: newErrorOb(ob),
				},
			}
		}
	}
	runTest := func(t *testing.T, strToObservation func(ob string) *types.Observation) {
		testCases := []struct {
			Name           string
			F              int
			ObservedErrors []string
			ExpectedResult []string
			ExpectedError  string
			ExpectedCount  int
		}{
			{
				Name:           "Insufficient number of valid observations",
				F:              1,
				ObservedErrors: []string{"error-1", "error-2"},
				ExpectedError:  "insufficient number of observations: expected 3, got 2",
				ExpectedCount:  0, // totalNum gate fires before any counting
			},
			{
				Name:           "Insufficient number of observations: request is not present or nil",
				F:              1,
				ObservedErrors: []string{"error-1", requestNotPresent, requestIsNil},
				ExpectedError:  "insufficient number of observations: expected 3, got 1",
				ExpectedCount:  0, // totalNum gate fires before any counting
			},
			{
				Name:           "Insufficient number of valid errors",
				F:              1,
				ObservedErrors: []string{"error-1", nonError, nonError, emptyError},
				ExpectedError:  "insufficient number of errors: expected 2, got 1",
				ExpectedCount:  1, // "error-1" was seen by 1 node; non-error and empty-error are excluded
			},
			{
				Name:           "Happy path F+1 identical errors",
				F:              1,
				ObservedErrors: []string{"another-error", "happy-path-error", "happy-path-error"},
				ExpectedResult: []string{"happy-path-error"},
				ExpectedCount:  2, // "happy-path-error" seen by 2 nodes
			},
			{
				Name:           "Happy path: returns slice of most common errors",
				F:              2,
				ObservedErrors: []string{"error-1", "error-2", "error-2", "error-3", "error-4"},
				ExpectedResult: []string{"error-2", "error-1"},
				ExpectedCount:  3, // accumulated count: "error-2"(2) + "error-1"(1) = 3, just enough to reach F+1=3
			},
		}
		for _, tc := range testCases {
			t.Run(tc.Name, func(t *testing.T) {
				N := 3*tc.F + 1
				aos := make([]attributedObservation, N)
				for i, ob := range tc.ObservedErrors {
					aos[i] = attributedObservation{
						// G115: integer overflow conversion int -> uint8
						//nolint:gosec
						Observer:    commontypes.OracleID(i),
						Observation: strToObservation(ob),
					}
				}
				rawActualErrors, actualCount, err := modeForError(N, tc.F, tc.F+1, requestID, aos)
				if tc.ExpectedError != "" {
					require.ErrorContains(t, err, tc.ExpectedError)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, tc.ExpectedCount, actualCount)
				var actualErrors []string
				for _, actualError := range rawActualErrors {
					actualErrors = append(actualErrors, string(actualError))
				}
				require.Equal(t, tc.ExpectedResult, actualErrors)
			})
		}
	}
	t.Run("Eventually Consistent Errors", func(t *testing.T) {
		newObservation := func(ob string) *types.Observation {
			return strToObservation(func(ob string) *types.RequestObservation {
				return &types.RequestObservation{
					Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte(ob)},
				}
			}, func(ob string) *types.RequestObservation {
				return &types.RequestObservation{
					Observation: &types.RequestObservation_Error{Error: []byte(ob)},
				}
			}, ob)
		}
		runTest(t, newObservation)
	})
	t.Run("Volatile Errors", func(t *testing.T) {
		newObservation := func(ob string) *types.Observation {
			return strToObservation(func(ob string) *types.RequestObservation {
				return &types.RequestObservation{
					Observation: &types.RequestObservation_Volatile{
						Volatile: &types.VolatileObservations{Observations: []*types.VolatileObservation{
							{
								Height: 1,
								Hash:   []byte(ob),
							},
						}}},
				}
			}, func(ob string) *types.RequestObservation {
				return &types.RequestObservation{
					Observation: &types.RequestObservation_Volatile{
						Volatile: &types.VolatileObservations{Error: []byte(ob)},
					},
				}
			}, ob)
		}
		runTest(t, newObservation)
	})
}

func TestErrorMode_TwoFPlusOneThreshold(t *testing.T) {
	// N=7, F=2: with minMatching=5 (2F+1), we need 5 matching errors to succeed.
	const requestID = "req-2f1"
	N, F := 7, 2
	minMatching := 2*F + 1 // 5

	makeAos := func(errors []string) []attributedObservation {
		aos := make([]attributedObservation, len(errors))
		for i, e := range errors {
			aos[i] = attributedObservation{
				//nolint:gosec
				Observer: commontypes.OracleID(i),
				Observation: &types.Observation{
					Observations: map[string]*types.RequestObservation{
						requestID: {Observation: &types.RequestObservation_Error{Error: []byte(e)}},
					},
				},
			}
		}
		return aos
	}

	t.Run("succeeds when minMatching number of identical errors are reported", func(t *testing.T) {
		errors := []string{"err", "err", "err", "err", "err", "other", "other"}
		result, actualCount, err := modeForError(N, F, minMatching, requestID, makeAos(errors))
		require.NoError(t, err)
		require.Equal(t, 5, actualCount)
		require.Equal(t, []string{"err"}, func() []string {
			s := make([]string, len(result))
			for i, b := range result {
				s[i] = string(b)
			}
			return s
		}())
	})

	t.Run("succeeds when minMatching number of different errors are reported", func(t *testing.T) {
		errors := []string{"err-a", "err-a", "err-b", "err-b", "err-c", "err-d", "err-e"}
		result, actualCount, err := modeForError(N, F, minMatching, requestID, makeAos(errors))
		require.NoError(t, err)
		require.Equal(t, 5, actualCount)
		require.Equal(t, []string{"err-a", "err-b", "err-c"}, func() []string {
			s := make([]string, len(result))
			for i, b := range result {
				s[i] = string(b)
			}
			return s
		}())
	})

	t.Run("fails when combined error observations don't reach minMatching", func(t *testing.T) {
		// Only 4 error observations total (3 distinct errors), non-errors fill the remaining 3 slots
		allObs := make([]attributedObservation, N)
		errPayloads := []string{"err-a", "err-b", "err-a", "err-c"}
		for i, e := range errPayloads {
			allObs[i] = attributedObservation{
				//nolint:gosec
				Observer: commontypes.OracleID(i),
				Observation: &types.Observation{
					Observations: map[string]*types.RequestObservation{
						requestID: {Observation: &types.RequestObservation_Error{Error: []byte(e)}},
					},
				},
			}
		}
		// remaining 3 are non-error (EventuallyConsistent) — count toward totalNum but not error count
		for i := len(errPayloads); i < N; i++ {
			allObs[i] = attributedObservation{
				//nolint:gosec
				Observer: commontypes.OracleID(i),
				Observation: &types.Observation{
					Observations: map[string]*types.RequestObservation{
						requestID: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value")}},
					},
				},
			}
		}
		_, actualCount, err := modeForError(N, F, minMatching, requestID, allObs)
		require.ErrorContains(t, err, "insufficient number of errors")
		require.Equal(t, 4, actualCount)
	})
}
