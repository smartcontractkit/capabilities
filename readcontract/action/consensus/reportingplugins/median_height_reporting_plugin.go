package reportingplugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"
)

type requestStore interface {
	GetAllRequestIDs() []string
	GetLatestObservedHeightForRequest(requestID string) *uint64
}

// medianHeightReportingPlugin is a reporting plugin that reports the median height observed for each request
// relies on the request store in each reporting plugin to get the latest observed height for each request
type medianHeightReportingPlugin struct {
	config  ocr3types.ReportingPluginConfig
	request requestStore
}

func NewMedianHeightReportingPlugin(
	config ocr3types.ReportingPluginConfig,
	requestsStore requestStore,
) ocr3types.ReportingPlugin[[]byte] {
	return &medianHeightReportingPlugin{
		config:  config,
		request: requestsStore,
	}
}

func (r *medianHeightReportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	requestIDs := r.request.GetAllRequestIDs()

	queryJSON, err := json.Marshal(requestIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request IDs: %w", err)
	}
	return queryJSON, nil
}

type RequestObservationHeight struct {
	RequestID string
	Height    uint64
}

func (r *medianHeightReportingPlugin) Observation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query) (types.Observation, error) {
	var requestIDs []string
	err := json.Unmarshal(query, &requestIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal request IDs: %w", err)
	}

	var requestsLatestHeight []RequestObservationHeight
	for _, requestID := range requestIDs {
		if height := r.request.GetLatestObservedHeightForRequest(requestID); height != nil {
			requestsLatestHeight = append(requestsLatestHeight, RequestObservationHeight{
				RequestID: requestID,
				Height:    *height,
			})
		}
	}

	observationsJSON, err := json.Marshal(requestsLatestHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Requests latest heights: %w", err)
	}

	return observationsJSON, nil
}

// ValidateObservation checks for duplicate request IDs to prevent a node maliciously influencing the median by submitting multiple observations for the same request ID
func (r *medianHeightReportingPlugin) ValidateObservation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	var requestsLatestHeight []RequestObservationHeight
	err := json.Unmarshal(ao.Observation, &requestsLatestHeight)
	if err != nil {
		return fmt.Errorf("failed to unmarshal Requests latest heights: %w", err)
	}

	// Check for duplicates
	seen := make(map[string]bool)
	for _, ro := range requestsLatestHeight {
		if seen[ro.RequestID] {
			return fmt.Errorf("duplicate request ID found: %s", ro.RequestID)
		}
		seen[ro.RequestID] = true
	}

	return nil
}

func (r *medianHeightReportingPlugin) ObservationQuorum(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (quorumReached bool, err error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumTwoFPlusOne, r.config.N, r.config.F, aos), nil
}

func (r *medianHeightReportingPlugin) Outcome(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (ocr3types.Outcome, error) {
	requestIDToHeights := map[string][]uint64{}
	for _, obs := range aos {
		var requestsLatestHeight []RequestObservationHeight
		err := json.Unmarshal(obs.Observation, &requestsLatestHeight)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal Requests latest heights: %w", err)
		}

		for _, ro := range requestsLatestHeight {
			requestIDToHeights[ro.RequestID] = append(requestIDToHeights[ro.RequestID], ro.Height)
		}
	}

	// Sort request IDs to ensure deterministic outcome
	var sortedRequestIDs []string
	for requestID := range requestIDToHeights {
		sortedRequestIDs = append(sortedRequestIDs, requestID)
	}
	sort.Strings(sortedRequestIDs)

	var medians []RequestObservationHeight
	for _, requestID := range sortedRequestIDs {
		heights := requestIDToHeights[requestID]

		sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
		median := heights[len(heights)/2]
		medians = append(medians, RequestObservationHeight{
			RequestID: requestID,
			Height:    median,
		})
	}

	return json.Marshal(medians)
}

func (r *medianHeightReportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	return []ocr3types.ReportPlus[[]byte]{
		{
			ReportWithInfo:               ocr3types.ReportWithInfo[[]byte]{Report: types.Report(outcome)},
			TransmissionScheduleOverride: nil,
		},
	}, nil
}

func (r *medianHeightReportingPlugin) ShouldAcceptAttestedReport(ctx context.Context, seqNr uint64, reportWithInfo ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	return true, nil
}

func (r *medianHeightReportingPlugin) ShouldTransmitAcceptedReport(ctx context.Context, seqNr uint64, reportWithInfo ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	var medianHeights []RequestObservationHeight
	err := json.Unmarshal(reportWithInfo.Report, &medianHeights)
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal median heights: %w", err)
	}

	return len(medianHeights) > 0, nil
}

func (r *medianHeightReportingPlugin) Close() error {
	return nil
}
