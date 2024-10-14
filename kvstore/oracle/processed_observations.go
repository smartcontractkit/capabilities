package oracle

import (
	"github.com/smartcontractkit/libocr/commontypes"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

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
