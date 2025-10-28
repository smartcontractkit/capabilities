package oracle

import (
	"slices"

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

func (po *ProcessedObservation) Observe(request kvrequests.Request, observer commontypes.OracleID) {
	if !po.request.Equal(request) {
		po.lggr.Infow("Requests are not equal",
			"request", request,
			"po.request", po.request,
		)
		return
	}

	if slices.Contains(po.observers, observer) {
		po.lggr.Infow("Observer already observed",
			"po.observationCount", po.observationCount,
			"observers", po.observers,
		)
		return
	}

	po.observers = append(po.observers, observer)
	po.observationCount++
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

// GetOrdered returns the ProcessedObservations in request type order:
// 1. RequestTypeRemoveNamespaceReference
// 2. RequestTypeAddNamespaceReference
// 3. RequestTypeWrite
// 4. RequestTypeRead
func (po *ProcessedObservations) GetOrdered() []*ProcessedObservation {
	orderedObservations := make([]*ProcessedObservation, 0)
	AddNamespaceReferencesObservations := make([]*ProcessedObservation, 0)
	writeObservations := make([]*ProcessedObservation, 0)
	readObservations := make([]*ProcessedObservation, 0)

	for _, processedObservation := range po.observations {
		switch processedObservation.request.Type {
		case kvrequests.RequestTypeAddNamespaceReference:
			AddNamespaceReferencesObservations = append(AddNamespaceReferencesObservations, processedObservation)
		case kvrequests.RequestTypeRemoveNamespaceReference:
			orderedObservations = append(orderedObservations, processedObservation)
		case kvrequests.RequestTypeWrite:
			writeObservations = append(writeObservations, processedObservation)
		case kvrequests.RequestTypeRead:
			readObservations = append(readObservations, processedObservation)
		case kvrequests.RequestTypeUnspecified:
			continue
		}
	}

	return append(orderedObservations,
		append(AddNamespaceReferencesObservations,
			append(writeObservations, readObservations...)...)...)
}
