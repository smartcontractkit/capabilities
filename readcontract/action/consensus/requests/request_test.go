package requests

import (
	"errors"
	"testing"
)

func TestAddObservationToRequest(t *testing.T) {
	tests := []struct {
		name          string
		initialObs    []Observation
		height        uint64
		value         []byte
		expectedError error
	}{
		{
			name:          "First observation",
			initialObs:    []Observation{},
			height:        1,
			value:         []byte("value1"),
			expectedError: nil,
		},
		{
			name: "Sequential observation",
			initialObs: []Observation{
				{height: 5, value: []byte("value1")},
			},
			height:        6,
			value:         []byte("value2"),
			expectedError: nil,
		},
		{
			name: "Non-sequential observation",
			initialObs: []Observation{
				{height: 1, value: []byte("value1")},
			},
			height:        3,
			value:         []byte("value3"),
			expectedError: errors.New("height is not sequential, last height: 1, height: 3"),
		},
		{
			name: "Non-sequential observation reverse order",
			initialObs: []Observation{
				{height: 3, value: []byte("value1")},
			},
			height:        1,
			value:         []byte("value3"),
			expectedError: errors.New("height is not increasing, last height: 3, height: 1"),
		},

		{
			name: "Duplicate height",
			initialObs: []Observation{
				{height: 1, value: []byte("value1")},
			},
			height:        1,
			value:         []byte("value1"),
			expectedError: ErrObservationForHeightAlreadyExists,
		},

		{
			name: "Reset consensus height",
			initialObs: []Observation{
				{height: 1, value: []byte("value1")},
				{height: 2, value: []byte("value2")},
				{height: 3, value: []byte("value3")},
				{height: 4, value: []byte("value4")},
				{height: 5, value: []byte("value5")},
			},
			height:        6,
			value:         []byte("value6"),
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &request{
				observations:                  tt.initialObs,
				observationsBeforeHeightReset: 5,
			}
			err := r.addObservation(tt.height, tt.value)
			if err != nil && err.Error() != tt.expectedError.Error() {
				t.Errorf("expected error %v, got %v", tt.expectedError, err)
			}
			if err == nil && tt.expectedError == nil {
				lastObs := r.observations[len(r.observations)-1]
				if lastObs.height != tt.height || string(lastObs.value) != string(tt.value) {
					t.Errorf("expected observation height %d and value %s, got height %d and value %s", tt.height, tt.value, lastObs.height, lastObs.value)
				}
				if tt.name == "Reset consensus height" && r.consensusHeight != nil {
					t.Errorf("expected consensusHeight to be nil, got %v", r.consensusHeight)
				}
			}
		})
	}
}
