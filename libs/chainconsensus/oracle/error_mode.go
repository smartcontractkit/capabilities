package oracle

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

// modeForError returns a slice of common errors for a given request when:
// 1. The total number of observations is at least (N+F)/2+1, and
// 2. The number of observed errors is at least F+1.
// If no single error was observed by at least F+1 nodes, it returns a slice
// of the most frequently observed errors whose combined observation count equals F+1.
func modeForError(N, F int, requestID string, aos []attributedObservation) ([][]byte, error) {
	type keyT [sha256.Size]byte
	counters := make(map[keyT]*counter[[]byte])
	var totalNum int
	for _, ao := range aos {
		nodeID := ao.Observer
		if ao.Observation == nil || ao.Observation.Observations == nil {
			continue
		}

		requestObservation, ok := ao.Observation.Observations[requestID]
		if !ok || requestObservation == nil {
			continue
		}
		totalNum++
		// non error observations must contribute to the total num, as we need to track number of nodes that reported
		// observation for the request.
		if _, isError := requestObservation.Observation.(*types.RequestObservation_Error); !isError {
			continue
		}

		observedErr := requestObservation.GetError()
		if len(observedErr) == 0 {
			continue
		}
		key := sha256.Sum256(observedErr)
		elem, ok := counters[key]
		if !ok {
			counters[key] = &counter[[]byte]{
				value:    observedErr,
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
		return nil, fmt.Errorf("insufficient number of observations: expected %d, got %d", expectedObservations, totalNum)
	}

	sortedCounters := make([]counter[[]byte], 0, len(counters))
	for _, c := range counters {
		sortedCounters = append(sortedCounters, *c)
	}

	sort.Slice(sortedCounters, func(i, j int) bool {
		if sortedCounters[i].count == sortedCounters[j].count {
			return sortedCounters[i].observer < sortedCounters[j].observer
		}
		return sortedCounters[i].count > sortedCounters[j].count
	})

	var result [][]byte
	var count int
	for _, c := range sortedCounters {
		result = append(result, c.value)
		count += c.count
		if count >= F+1 {
			break
		}
	}

	if count < F+1 {
		return nil, fmt.Errorf("insufficient number of errors: expected %d, got %d", F+1, count)
	}

	return result, nil
}
