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
		}{
			{
				Name:           "Insufficient number of valid observations",
				F:              1,
				ObservedErrors: []string{"error-1", "error-2"},
				ExpectedError:  "insufficient number of observations: expected 3, got 2",
			},
			{
				Name:           "Insufficient number of observations: request is not present or nil",
				F:              1,
				ObservedErrors: []string{"error-1", requestNotPresent, requestIsNil},
				ExpectedError:  "insufficient number of observations: expected 3, got 1",
			},
			{
				Name:           "Insufficient number of valid errors",
				F:              1,
				ObservedErrors: []string{"error-1", nonError, nonError, emptyError},
				ExpectedError:  "insufficient number of errors: expected 2, got 1",
			},
			{
				Name:           "Happy path F+1 identical errors",
				F:              1,
				ObservedErrors: []string{"another-error", "happy-path-error", "happy-path-error"},
				ExpectedResult: []string{"happy-path-error"},
			},
			{
				Name:           "Happy path: returns slice of most common errors",
				F:              2,
				ObservedErrors: []string{"error-1", "error-2", "error-2", "error-3", "error-4"},
				ExpectedResult: []string{"error-2", "error-1"},
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
				rawActualErrors, err := modeForError(N, tc.F, requestID, aos)
				if tc.ExpectedError != "" {
					require.ErrorContains(t, err, tc.ExpectedError)
				} else {
					require.NoError(t, err)
				}
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
