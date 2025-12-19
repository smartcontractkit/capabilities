package oracle

import (
	"testing"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func TestErrorMode(t *testing.T) {
	const requestID = "req-1"
	strToErrorObservation := func(ob string) *types.Observation {
		return &types.Observation{
			Observations: map[string]*types.RequestObservation{
				requestID: {
					Observation: &types.RequestObservation_Error{Error: []byte(ob)},
				},
			},
		}
	}
	testCases := []struct {
		Name              string
		F                 int
		ObservedErrors    []string
		CreateObservation func(string) *types.Observation
		ExpectedResult    []string
		ExpectedError     string
	}{
		{
			Name:              "Insufficient number of valid observations",
			F:                 1,
			ObservedErrors:    []string{"error-1", "error-2"},
			CreateObservation: strToErrorObservation,
			ExpectedError:     "insufficient number of observations: expected 3, got 2",
		},
		{
			Name:           "Insufficient number of observations: request is not present or nil",
			F:              1,
			ObservedErrors: []string{"error-1", "request-not-present", "request-is-nil"},
			CreateObservation: func(ob string) *types.Observation {
				if ob == "request-not-present" {
					return &types.Observation{
						Observations: map[string]*types.RequestObservation{},
					}
				}
				if ob == "request-is-nil" {
					return &types.Observation{
						Observations: map[string]*types.RequestObservation{
							requestID: nil,
						},
					}
				}
				return strToErrorObservation(ob)
			},
			ExpectedError: "insufficient number of observations: expected 3, got 1",
		},
		{
			Name:           "Insufficient number of valid errors",
			F:              1,
			ObservedErrors: []string{"error-1", "non-error", "non-error", "empty-error"},
			CreateObservation: func(ob string) *types.Observation {
				if ob == "non-error" {
					return &types.Observation{
						Observations: map[string]*types.RequestObservation{
							requestID: {
								Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte(ob)},
							},
						},
					}
				}
				if ob == "empty-error" {
					ob = ""
				}
				return strToErrorObservation(ob)
			},
			ExpectedError: "insufficient number of errors: expected 2, got 1",
		},
		{
			Name:              "Happy path F+1 identical errors",
			F:                 1,
			ObservedErrors:    []string{"another-error", "happy-path-error", "happy-path-error"},
			CreateObservation: strToErrorObservation,
			ExpectedResult:    []string{"happy-path-error"},
		},
		{
			Name:              "Happy path: returns slice of most common errors",
			F:                 2,
			ObservedErrors:    []string{"error-1", "error-2", "error-2", "error-3", "error-4"},
			CreateObservation: strToErrorObservation,
			ExpectedResult:    []string{"error-2", "error-1"},
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
					Observation: tc.CreateObservation(ob),
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
