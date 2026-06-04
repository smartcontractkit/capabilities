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

// mode - returns most frequent value and its support count, if total number of observations is at least (N+F)/2+1 and
// number of values with identical keys is at least F+1. Returns error, otherwise.
// If multiple values have identical number of observations, prefers value reported by oracle with the lowest oracleID.
// The returned int is the count of nodes that observed the winning value (i.e. the number of identical responses).
func mode[keyT comparable, valueT any](N, F int, observations iter.Seq2[commontypes.OracleID, *observation[keyT, valueT]]) (valueT, int, error) {
	counters := make(map[keyT]*counter[valueT])
	var totalNum int
	for nodeID, nodeObservation := range observations {
		totalNum++
		// node provided an observation for a request, but it's of a different type.
		// we should count it towards totalNum, but not towards any specific value.
		if nodeObservation == nil {
			continue
		}
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
		return zero, 0, fmt.Errorf("insufficient number of observations: expected %d, got %d", expectedObservations, totalNum)
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
		return zero, 0, errors.New("unexpected state: highestCounter is nil")
	}

	if highestCounter.count < F+1 {
		var zero valueT
		return zero, highestCounter.count, fmt.Errorf("insufficient number of identical observations: expected %d, got %d", F+1, highestCounter.count)
	}

	return highestCounter.value, highestCounter.count, nil
}

func byzQuorumSize(N, F int) int {
	return (N+F)/2 + 1
}
