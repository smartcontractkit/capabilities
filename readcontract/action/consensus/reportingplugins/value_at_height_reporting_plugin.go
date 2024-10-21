package reportingplugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"
)

type valueStore interface {
	GetValueAtHeight(requestID string, height uint64) []byte
}

type pendingRequests interface {
	GetRequestsWithConsensusHeight() []requests.RequestWithConsensusHeight
}

type valueAtHeightReportingPlugin struct {
	config          ocr3types.ReportingPluginConfig
	pendingRequests pendingRequests
	valueStore      valueStore
}

func NewValueAtHeightReportingPlugin(
	config ocr3types.ReportingPluginConfig,
	pendingRequests pendingRequests,
	valueStore valueStore,
) ocr3types.ReportingPlugin[[]byte] {
	return &valueAtHeightReportingPlugin{
		config:          config,
		pendingRequests: pendingRequests,
		valueStore:      valueStore,
	}
}

func (v *valueAtHeightReportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	requestsWithConsensusHeights := v.pendingRequests.GetRequestsWithConsensusHeight()
	query, err := json.Marshal(requestsWithConsensusHeights)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal requestsWithConsensusHeights: %w", err)
	}
	return query, nil
}

type ObservedValue struct {
	RequestID string
	Value     []byte
}

func (v *valueAtHeightReportingPlugin) Observation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query) (types.Observation, error) {
	var requestsWithConsensusHeights []requests.RequestWithConsensusHeight
	if err := json.Unmarshal(query, &requestsWithConsensusHeights); err != nil {
		return nil, fmt.Errorf("failed to unmarshal query: %w", err)
	}

	observations := make([]ObservedValue, 0)
	for _, req := range requestsWithConsensusHeights {
		value := v.valueStore.GetValueAtHeight(req.RequestID, req.Height)
		if value != nil {
			observations = append(observations, ObservedValue{
				RequestID: req.RequestID,
				Value:     value,
			})
		}
	}

	observation, err := json.Marshal(observations)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal observations: %w", err)
	}
	return observation, nil
}

func (v *valueAtHeightReportingPlugin) ValidateObservation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	var observations []ObservedValue
	if err := json.Unmarshal(ao.Observation, &observations); err != nil {
		return fmt.Errorf("failed to unmarshal observations: %w", err)
	}

	seen := make(map[string]bool)
	for _, obs := range observations {
		if seen[obs.RequestID] {
			return fmt.Errorf("duplicate request ID found: %s", obs.RequestID)
		}
		seen[obs.RequestID] = true
	}

	return nil
}

func (v *valueAtHeightReportingPlugin) ObservationQuorum(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (quorumReached bool, err error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumFPlusOne, v.config.N, v.config.F, aos), nil
}

func (v *valueAtHeightReportingPlugin) Outcome(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (ocr3types.Outcome, error) {
	// Group observations by request ID
	observationMap := make(map[string][][]byte)
	for _, ao := range aos {
		var observations []ObservedValue
		if err := json.Unmarshal(ao.Observation, &observations); err != nil {
			return nil, fmt.Errorf("failed to unmarshal observations: %w", err)
		}
		for _, obs := range observations {
			observationMap[obs.RequestID] = append(observationMap[obs.RequestID], obs.Value)
		}
	}

	// Check for f + 1 values for the same request ID that are equal and add them to the outcome
	outcome := make(map[string][]byte)
	for reqID, values := range observationMap {
		valueCount := make(map[string]int)
		for _, value := range values {
			hash := sha256.Sum256(value)
			hashStr := hex.EncodeToString(hash[:])
			valueCount[hashStr]++
			if valueCount[hashStr] >= v.config.F+1 {
				outcome[reqID] = value
				break
			}
		}
	}

	// Sort the outcome by id to ensure deterministic outcome
	sortedResult := make([]ObservedValue, 0, len(outcome))
	for reqID, value := range outcome {
		sortedResult = append(sortedResult, ObservedValue{
			RequestID: reqID,
			Value:     value,
		})
	}

	sort.Slice(sortedResult, func(i, j int) bool {
		return sortedResult[i].RequestID < sortedResult[j].RequestID
	})

	outcomeBytes, err := json.Marshal(sortedResult)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outcomeBytes: %w", err)
	}

	return outcomeBytes, nil
}

func (v *valueAtHeightReportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	return []ocr3types.ReportPlus[[]byte]{
		{
			ReportWithInfo:               ocr3types.ReportWithInfo[[]byte]{Report: types.Report(outcome)},
			TransmissionScheduleOverride: nil,
		},
	}, nil
}

func (v *valueAtHeightReportingPlugin) ShouldAcceptAttestedReport(ctx context.Context, seqNr uint64, reportWithInfo ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	return true, nil
}

func (v *valueAtHeightReportingPlugin) ShouldTransmitAcceptedReport(ctx context.Context, seqNr uint64, reportWithInfo ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	var observations []ObservedValue
	if err := json.Unmarshal(reportWithInfo.Report, &observations); err != nil {
		return false, fmt.Errorf("failed to unmarshal report: %w", err)
	}

	if len(observations) == 0 {
		return false, nil
	}

	return true, nil
}

func (v *valueAtHeightReportingPlugin) Close() error {
	return nil
}
