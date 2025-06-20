package oracle

import (
	"errors"
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

	if totalNum < (N+F)/2+1 {
		var zero valueT
		return zero, errors.New("insufficient number of observations")
	}

	var highestCounter *counter[valueT]
	for _, newCounter := range counters {
		if highestCounter == nil ||
			highestCounter.count < newCounter.count ||
			(highestCounter.count == newCounter.count && highestCounter.observer < newCounter.observer) {
			highestCounter = newCounter
		}
	}

	if highestCounter == nil || highestCounter.count < F+1 {
		var zero valueT
		return zero, errors.New("insufficient number of identical observations")
	}

	return highestCounter.value, nil
}
