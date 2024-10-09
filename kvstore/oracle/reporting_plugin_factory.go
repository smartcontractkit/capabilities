package oracle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

var _ ocr3types.ReportingPluginFactory[[]byte] = (*reportingPluginFactory)(nil)

type reportingPluginFactory struct {
	logger        logger.SugaredLogger
	requestsStore *kvrequests.RequestsStore
}

func NewReportingPluginFactory(
	logger logger.SugaredLogger,
	requestsStore *kvrequests.RequestsStore,
) *reportingPluginFactory {
	return &reportingPluginFactory{
		logger:        logger,
		requestsStore: requestsStore,
	}
}

func (rpf *reportingPluginFactory) NewReportingPlugin(
	config ocr3types.ReportingPluginConfig,
) (
	ocr3types.ReportingPlugin[[]byte],
	ocr3types.ReportingPluginInfo,
	error,
) {
	return &reportingPlugin{
			config:        config,
			logger:        rpf.logger,
			requestsStore: rpf.requestsStore,
		}, ocr3types.ReportingPluginInfo{
			Name: "kv-store-oracle@1.0.0",
			Limits: ocr3types.ReportingPluginLimits{
				MaxQueryLength:       ocr3types.MaxMaxQueryLength,
				MaxObservationLength: ocr3types.MaxMaxObservationLength,
				MaxOutcomeLength:     ocr3types.MaxMaxOutcomeLength,
				MaxReportLength:      ocr3types.MaxMaxReportLength,
				MaxReportCount:       ocr3types.MaxMaxReportCount,
			},
		}, nil
}

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type reportingPlugin struct {
	config        ocr3types.ReportingPluginConfig
	logger        logger.SugaredLogger
	requestsStore *kvrequests.RequestsStore
}

func (rp *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	requests, err := rp.requestsStore.Get(ctx, &kvrequests.Filters{
		Status: kvrequests.RequestStatusPending,
	})
	if err != nil {
		return nil, fmt.Errorf("could not retrieve requests: %w", err)
	}

	if len(requests) == 0 {
		rp.logger.Debugw("No pending requests. Skipping query.")
		return json.Marshal([]kvrequests.RequestID{})
	}

	var requestIDs []kvrequests.RequestID
	for _, request := range requests {
		requestIDs = append(requestIDs, request.ID())
	}

	rp.logger.Debugw("Query complete",
		"requestIDsLen", len(requestIDs),
		"requestIDs", requestIDs,
	)
	return json.Marshal(requestIDs)
}

func (rp *reportingPlugin) Observation(
	ctx context.Context,
	outctx ocr3types.OutcomeContext,
	query types.Query,
) (types.Observation, error) {
	var requestIDs []kvrequests.RequestID
	if err := json.Unmarshal(query, &requestIDs); err != nil {
		return nil, fmt.Errorf("could not unmarshal query: %w", err)
	}

	if (len(requestIDs)) == 0 {
		rp.logger.Debugw("Empty query. Skipping observation.")
		return json.Marshal([]kvrequests.Request{})
	}

	requests, err := rp.requestsStore.Get(ctx, &kvrequests.Filters{RequestIDs: requestIDs})
	if err != nil {
		return nil, fmt.Errorf("could not retrieve requests: %w", err)
	}

	rp.logger.Debugw("Observation complete",
		"requestsLen", len(requests),
		"requests", requests,
	)
	return json.Marshal(requests)
}

func (rp *reportingPlugin) ValidateObservation(outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	return nil
}

func (rp *reportingPlugin) ObservationQuorum(outctx ocr3types.OutcomeContext, query types.Query) (ocr3types.Quorum, error) {
	return ocr3types.QuorumTwoFPlusOne, nil
}

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

func (rp *reportingPlugin) Outcome(
	outctx ocr3types.OutcomeContext,
	query types.Query,
	aos []types.AttributedObservation,
) (ocr3types.Outcome, error) {
	var outcome Outcome
	if outctx.SeqNr == 1 {
		rp.logger.Debugw("First outcome")
		outcome = Outcome{
			Values:            make(map[string][]byte),
			CompletedRequests: make([]kvrequests.Request, 0),
		}
	} else {
		if err := json.Unmarshal(outctx.PreviousOutcome, &outcome); err != nil {
			return nil, fmt.Errorf("could not unmarshal PreviousOutcome: %w", err)
		}
	}

	// Wipe out previously completed requests
	outcome.CompletedRequests = make([]kvrequests.Request, 0)

	processedObservations := ProcessedObservations{
		lggr:         rp.logger,
		observations: make(map[kvrequests.RequestID]*ProcessedObservation),
	}

	rp.logger.Debugw("Outcome start", "attributedObservationsLen", len(aos))
	for _, ao := range aos {
		var newRequests []kvrequests.Request
		if err := json.Unmarshal(ao.Observation, &newRequests); err != nil {
			return nil, fmt.Errorf("could not unmarshal observation: %w", err)
		}

		for _, newRequest := range newRequests {
			processedObservations.Add(newRequest, ao.Observer)
		}
	}

	rp.logger.Debugw("Processed observations",
		"processedObservationsLen", len(processedObservations.observations),
	)

	for _, processedObservation := range processedObservations.observations {
		if processedObservation.observationCount <= rp.config.F {
			rp.logger.Debugw("Not enough observations",
				"requestID", processedObservation.request.ID(),
				"observationCount", processedObservation.observationCount,
				"observers", processedObservation.observers,
			)
			continue
		}

		switch processedObservation.request.Type {
		case kvrequests.RequestKindWrite:
			for key, value := range processedObservation.request.KVPairs {
				outcome.Values[key] = value
			}
			processedObservation.request.Status = kvrequests.RequestStatusCompleted
			outcome.CompletedRequests = append(outcome.CompletedRequests, processedObservation.request)
		case kvrequests.RequestKindRead:
			keysWithValues := make(map[string][]byte)
			for key := range processedObservation.request.KVPairs {
				val, ok := outcome.Values[key]
				if !ok {
					keysWithValues[key] = []byte("")
				} else {
					keysWithValues[key] = val
				}
			}

			rp.logger.Debugw("Read request",
				"request", processedObservation.request,
			)
			processedObservation.request.KVPairs = keysWithValues
			processedObservation.request.Status = kvrequests.RequestStatusCompleted
			outcome.CompletedRequests = append(outcome.CompletedRequests, processedObservation.request)
		}
	}

	rp.logger.Debugw("Outcome complete",
		"completedRequestsLen", len(outcome.CompletedRequests),
		"outcome.CompletedRequests", outcome.CompletedRequests,
	)
	return json.Marshal(outcome)
}

func (rp *reportingPlugin) Reports(seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportWithInfo[[]byte], error) {
	var o Outcome
	if err := json.Unmarshal(outcome, &o); err != nil {
		return nil, fmt.Errorf("could not unmarshal outcome: %w", err)
	}

	reportWithInfos := make([]ocr3types.ReportWithInfo[[]byte], 0)

	for _, request := range o.CompletedRequests {
		requestBytes, err := request.Marshal()
		if err != nil {
			return nil, fmt.Errorf("could not marshall request: %w", err)
		}
		reportWithInfos = append(reportWithInfos, ocr3types.ReportWithInfo[[]byte]{
			Report: requestBytes,
		})
	}

	rp.logger.Debugw("Reports complete",
		"reportWithInfosLen", len(reportWithInfos),
	)
	return reportWithInfos, nil
}

func (rp *reportingPlugin) ShouldAcceptAttestedReport(
	ctx context.Context,
	seqNr uint64,
	reportWithInfo ocr3types.ReportWithInfo[[]byte],
) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) ShouldTransmitAcceptedReport(
	ctx context.Context,
	seqNr uint64,
	reportWithInfo ocr3types.ReportWithInfo[[]byte],
) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) Close() error {
	return nil
}
