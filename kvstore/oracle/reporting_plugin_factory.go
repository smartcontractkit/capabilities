package oracle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ ocr3types.ReportingPluginFactory[[]byte] = (*reportingPluginFactory)(nil)

type reportingPluginFactory struct {
	logger        logger.Logger
	requestsStore *kvrequests.RequestsStore
}

func NewReportingPluginFactory(
	logger logger.Logger,
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
	logger        logger.Logger
	requestsStore *kvrequests.RequestsStore
}

func (rp *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	requests, err := rp.requestsStore.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve requests: %w", err)
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

	requests, err := rp.requestsStore.GetByID(ctx, requestIDs)
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
	CompletedRequests []kvrequests.RequestID
}

// TODO: Requests need to be ordered and process by insert timestamp
// This is not a perfect solution, but it should be good enough for now

type ProcessedObservation struct {
	request          kvrequests.Request
	observationCount int
	observers        []commontypes.OracleID
}

func (rp *reportingPlugin) Outcome(
	outctx ocr3types.OutcomeContext,
	query types.Query,
	aos []types.AttributedObservation,
) (ocr3types.Outcome, error) {
	var outcome Outcome
	if err := json.Unmarshal(outctx.PreviousOutcome, &outcome); err != nil {
		return nil, fmt.Errorf("could not unmarshal PreviousOutcome: %w", err)
	}
	// Wipe out previously completed requests
	outcome.CompletedRequests = make([]kvrequests.RequestID, 0)

	processedObservations := make(map[kvrequests.RequestID]ProcessedObservation)
	for _, ao := range aos {
		var newRequests []kvrequests.Request
		if err := json.Unmarshal(ao.Observation, &newRequests); err != nil {
			return nil, fmt.Errorf("could not unmarshal observation: %w", err)
		}

		for _, newRequest := range newRequests {
			observation, ok := processedObservations[newRequest.ID()]

			// First observation of this request
			if !ok {
				processedObservations[newRequest.ID()] = struct {
					request          kvrequests.Request
					observationCount int
					observers        []commontypes.OracleID
				}{request: newRequest}
			}

			// TODO: What if not equal? We should probably create a new entry to protect vs malicious actors
			// Request ID could be a hash of contents :)
			if observation.request.Equal(newRequest) {
				observation.observationCount++
				// TODO: Can we get duplicates?
				observation.observers = append(observation.observers, ao.Observer)
			}
		}
	}

	for _, processedObservation := range processedObservations {
		if processedObservation.observationCount <= rp.config.F {
			rp.logger.Debug("Not enough observations",
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
			outcome.CompletedRequests = append(outcome.CompletedRequests, processedObservation.request.ID())
		case kvrequests.RequestKindRead:
			// TODO: Implement
		}
	}

	rp.logger.Debug("Outcome complete",
		"completedRequestsLen", len(outcome.CompletedRequests),
		"outcome", outcome,
	)
	return json.Marshal(outcome)
}

func (rp *reportingPlugin) Reports(seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportWithInfo[[]byte], error) {
	var o Outcome
	if err := json.Unmarshal(outcome, &o); err != nil {
		return nil, fmt.Errorf("could not unmarshal outcome: %w", err)
	}

	reportWithInfos := make([]ocr3types.ReportWithInfo[[]byte], 0)

	for _, requestID := range o.CompletedRequests {
		reportWithInfos = append(reportWithInfos, ocr3types.ReportWithInfo[[]byte]{
			Report: []byte(requestID),
		})
	}

	rp.logger.Debug("Reports complete",
		"reportWithInfosLen", len(reportWithInfos),
		"reportWithInfos", reportWithInfos,
	)
	return reportWithInfos, nil
}

func (rp *reportingPlugin) ShouldAcceptAttestedReport(context.Context, uint64, ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) ShouldTransmitAcceptedReport(context.Context, uint64, ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) Close() error {
	return nil
}
