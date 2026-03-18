package oracle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type reportingPlugin struct {
	config        ocr3types.ReportingPluginConfig
	logger        logger.SugaredLogger
	requestsStore *kvrequests.RequestsStore
}

func NewReportingPlugin(
	config ocr3types.ReportingPluginConfig,
	logger logger.SugaredLogger,
	requestsStore *kvrequests.RequestsStore,
) ocr3types.ReportingPlugin[[]byte] {
	return &reportingPlugin{
		config:        config,
		logger:        logger,
		requestsStore: requestsStore,
	}
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

	rp.logger.Debugw("Observation complete", "requestsLen", len(requests), "requestIDs", requestIDs)
	rp.logger.Tracew("Observation requests", "requests", requests)
	return json.Marshal(requests)
}

func (rp *reportingPlugin) ValidateObservation(_ context.Context, outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	return nil
}

func (rp *reportingPlugin) ObservationQuorum(_ context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (quorumReached bool, err error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumTwoFPlusOne, rp.config.N, rp.config.F, aos), nil
}

func (rp *reportingPlugin) Outcome(
	_ context.Context,
	outctx ocr3types.OutcomeContext,
	query types.Query,
	aos []types.AttributedObservation,
) (ocr3types.Outcome, error) {
	var outcome Outcome
	if outctx.SeqNr == 1 {
		rp.logger.Debugw("First outcome")
		outcome = NewOutcome()
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

	for _, processedObservation := range processedObservations.GetOrdered() {
		if processedObservation.observationCount <= rp.config.F {
			rp.logger.Debugw("Not enough observations",
				"requestID", processedObservation.request.ID(),
				"observationCount", processedObservation.observationCount,
				"observers", processedObservation.observers,
			)
			continue
		}

		switch processedObservation.request.Type {
		case kvrequests.RequestTypeAddNamespaceReference:
			outcome.AddNamespaceReferences(
				processedObservation.request.Namespace,
				processedObservation.request.Reference,
			)
		case kvrequests.RequestTypeRemoveNamespaceReference:
			outcome.RemoveNamespaceReference(processedObservation.request.Namespace, processedObservation.request.Reference)
		case kvrequests.RequestTypeWrite:
			outcome.Write(processedObservation.request.Namespace, processedObservation.request.KVPairs)
		case kvrequests.RequestTypeRead:
			processedObservation.request.KVPairs = outcome.Read(
				processedObservation.request.Namespace,
				processedObservation.request.KVPairs,
			)
		case kvrequests.RequestTypeUnspecified:
			rp.logger.Warnw("Unspecified request",
				"requestID", processedObservation.request.ID(),
			)
		}

		processedObservation.request.Status = kvrequests.RequestStatusCompleted
		outcome.CompletedRequests = append(outcome.CompletedRequests, processedObservation.request)
	}

	rp.logger.Debugw("Outcome complete",
		"completedRequestsLen", len(outcome.CompletedRequests),
		"outcome.CompletedRequests", outcome.CompletedRequests,
	)
	return json.Marshal(outcome)
}

func (rp *reportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	var o Outcome
	if err := json.Unmarshal(outcome, &o); err != nil {
		return nil, fmt.Errorf("could not unmarshal outcome: %w", err)
	}

	reports := make([]ocr3types.ReportPlus[[]byte], 0)

	for _, request := range o.CompletedRequests {
		requestBytes, err := request.Marshal()
		if err != nil {
			return nil, fmt.Errorf("could not marshall request: %w", err)
		}
		reports = append(reports, ocr3types.ReportPlus[[]byte]{
			ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
				Report: requestBytes,
			},
		})
	}

	rp.logger.Debugw("Reports complete",
		"reports", len(reports),
	)
	return reports, nil
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
