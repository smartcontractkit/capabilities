package oracle

import (
	"errors"
	"fmt"
	"iter"

	"github.com/smartcontractkit/libocr/commontypes"
)

type counter[valueT any] struct {
	value    valueT
	count    int
	observer commontypes.OracleID
}

type observation[keyT comparable, valueT any] struct {
	Key   keyT
	Value valueT
}

// mode - returns most frequent value, if total number of observations is at least (N+F)/2+1 and
// number of values with identical keys is at least F+1. Returns error, otherwise.
// If multiple values have identical number of observations, prefers value reported by oracle with the lowest oracleID.
func mode[keyT comparable, valueT any](N, F int, observations iter.Seq2[commontypes.OracleID, observation[keyT, valueT]]) (valueT, error) {
	counters := make(map[keyT]*counter[valueT])
	var totalNum int
	for nodeID, nodeObservation := range observations {
		totalNum++
		elem, ok := counters[nodeObservation.Key]
		if !ok {
			counters[nodeObservation.Key] = &counter[valueT]{
				value:    nodeObservation.Value,
				count:    1,
				observer: nodeID,
			}
		} else {
			elem.count++
			elem.observer = min(elem.observer, nodeID)
		}
	}

	expectedObservations := byzQuorumSize(N, F)
	if totalNum < expectedObservations {
		var zero valueT
		return zero, fmt.Errorf("insufficient number of observations: expected %d, got %d", expectedObservations, totalNum)
	}

	var highestCounter *counter[valueT]
	for _, newCounter := range counters {
		if highestCounter == nil ||
			highestCounter.count < newCounter.count ||
			(highestCounter.count == newCounter.count && highestCounter.observer > newCounter.observer) {
			highestCounter = newCounter
		}
	}

	if highestCounter == nil {
		var zero valueT
		return zero, errors.New("unexpected state: highestCounter is nil")
	}

	if highestCounter.count < F+1 {
		var zero valueT
		return zero, fmt.Errorf("insufficient number of identical observations: expected %d, got %d", F+1, highestCounter.count)
	}

	return highestCounter.value, nil
}

func byzQuorumSize(N, F int) int {
	return (N+F)/2 + 1
}
