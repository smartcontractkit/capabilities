package oracle

import (
	"github.com/smartcontractkit/libocr/commontypes"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

type Outcome struct {
	// This is the local (in-memory) key-value store
	Values            map[string][]byte
	CompletedRequests []kvrequests.Request
}

// TODO: Requests need to be ordered and process by insert timestamp
// This is not a perfect solution, but it should be good enough for now

type ProcessedObservation struct {
	lggr             logger.SugaredLogger
	request          kvrequests.Request
	observationCount int
	observers        []commontypes.OracleID
}
type ProcessedObservations struct {
	lggr         logger.SugaredLogger
	observations map[kvrequests.RequestID]*ProcessedObservation
}

func (po *ProcessedObservations) Add(request kvrequests.Request, observer commontypes.OracleID) {
	observation := po.observations[request.ID()]

	// First observation of this request
	if observation == nil {
		po.observations[request.ID()] = &ProcessedObservation{
			lggr:             po.lggr,
			request:          request,
			observationCount: 1,
			observers:        []commontypes.OracleID{observer},
		}
	} else {
		observation.Observe(request, observer)
	}
}

func (po *ProcessedObservation) Observe(request kvrequests.Request, observer commontypes.OracleID) {
	// TODO: What if not equal? We should probably create a new entry to protect vs malicious actors
	// Request ID could be a hash of contents :)
	// TODO: Ensure that requrests that are completed are treated as equal as well. This is important for nodes
	// that have some data missing to be able process the same request.
	if !po.request.Equal(request) {
		po.lggr.Infow("Requests are not equal",
			"request", request,
			"po.request", po.request,
		)
		return
	}

	for _, existingObserver := range po.observers {
		if existingObserver == observer {
			po.lggr.Infow("Observer already observed",
				"po.observationCount", po.observationCount,
				"observers", po.observers,
			)
			return
		}
	}

	po.observers = append(po.observers, observer)
	po.observationCount++
}
