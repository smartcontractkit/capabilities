package requests

import (
	"errors"
	"fmt"
)

type request struct {
	id                            string
	observations                  []Observation
	consensusHeight               *uint64
	responseCh                    chan []byte
	observationsBeforeHeightReset int
}

func (r *request) GetLatestObservation() *Observation {
	if len(r.observations) == 0 {
		return nil
	}

	latestObservation := r.observations[len(r.observations)-1]
	return &latestObservation
}

var ErrObservationForHeightAlreadyExists = errors.New("observation for height already exists")

func (r *request) addObservation(height uint64, value []byte) error {
	if len(r.observations) > 0 {
		if r.observations[len(r.observations)-1].height != height-1 {
			lastHeight := r.observations[len(r.observations)-1].height
			if lastHeight == height {
				return ErrObservationForHeightAlreadyExists
			}

			if lastHeight > height {
				return fmt.Errorf("height is not increasing, last height: %d, height: %d", lastHeight, height)
			}

			if height-lastHeight > 1 {
				return fmt.Errorf("height is not sequential, last height: %d, height: %d", lastHeight, height)
			}
		}
	}

	r.observations = append(r.observations, Observation{height: height, value: value})

	// Reset consensus height if we have more observations than the threshold for the existing consensus height
	if len(r.observations) > r.observationsBeforeHeightReset {
		r.consensusHeight = nil
	}

	return nil
}

func (r *request) getObservationCount() int {
	return len(r.observations)
}
