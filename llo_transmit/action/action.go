package action

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	llotypes "github.com/smartcontractkit/chainlink-common/pkg/types/llo"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

var _ capabilities.ActionCapability = (*LLOTransmitAction)(nil)

// SubTransmitter represents a destination for LLO reports
// This interface allows for different transmission strategies (Mercury, CRE, etc.)
type SubTransmitter interface {
	llotypes.Transmitter
	services.Service
}

// LLOTransmitAction is a NoDAG action capability that transmits LLO reports
// to multiple configured destinations
type LLOTransmitAction struct {
	services.StateMachine
	lggr            logger.Logger
	verboseLogging  bool
	fromAccount     string
	subTransmitters []SubTransmitter
	capabilityInfo  capabilities.CapabilityInfo
	registry        core.CapabilitiesRegistry
	mu              sync.RWMutex
}

// Config represents the configuration for the LLO Transmit action
type Config struct {
	DonID            uint32                       `json:"donID"`
	Servers          map[string][]byte            `json:"servers"`          // Mercury servers
	Transmitters     []TransmitterConfig          `json:"transmitters"`     // Sub-transmitter configs
	VerboseLogging   bool                         `json:"verboseLogging"`
	FromAccount      string                       `json:"fromAccount"`
	Registry         core.CapabilitiesRegistry    `json:"-"` // Not serialized
}

type TransmitterConfig struct {
	Type string          `json:"type"` // e.g., "cre", "mercury"
	Opts json.RawMessage `json:"opts"` // Type-specific configuration
}

// Request represents the input to the Execute method
type Request struct {
	ConfigDigest types.ConfigDigest                   `json:"configDigest"`
	SeqNr        uint64                               `json:"seqNr"`
	Report       []byte                               `json:"report"`
	ReportInfo   llotypes.ReportInfo                  `json:"reportInfo"`
	Signatures   []types.AttributedOnchainSignature   `json:"signatures"`
}

// Response represents the output from the Execute method
type Response struct {
	Success                  bool                  `json:"success"`
	Error                    string                `json:"error,omitempty"`
	SuccessfulTransmissions  uint32                `json:"successfulTransmissions"`
	FailedTransmissions      uint32                `json:"failedTransmissions"`
	TransmitterResults       []TransmitterResult   `json:"transmitterResults"`
}

type TransmitterResult struct {
	Type    string `json:"type"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// NewLLOTransmitAction creates a new LLO Transmit action capability
func NewLLOTransmitAction(lggr logger.Logger, config Config, subTransmitters []SubTransmitter) (*LLOTransmitAction, error) {
	capInfo, err := capabilities.NewCapabilityInfo(
		"llo-transmit@1.0.0",
		capabilities.CapabilityTypeAction,
		"Transmits LLO reports to configured destinations",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create capability info: %w", err)
	}

	action := &LLOTransmitAction{
		StateMachine:    services.StateMachine{},
		lggr:            logger.Named(lggr, "LLOTransmitAction"),
		verboseLogging:  config.VerboseLogging,
		fromAccount:     config.FromAccount,
		subTransmitters: subTransmitters,
		capabilityInfo:  capInfo,
		registry:        config.Registry,
	}

	return action, nil
}

// Execute implements capabilities.ActionCapability
// It transmits the LLO report to all configured sub-transmitters
func (a *LLOTransmitAction) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	a.lggr.Debugw("Executing LLO transmit action", "requestID", request.Metadata.WorkflowExecutionID)

	// Parse the request
	var req Request
	if err := request.Inputs.UnwrapTo(&req); err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to unmarshal request: %w", err)
	}

	if a.verboseLogging {
		a.lggr.Debugw("Transmit request details",
			"configDigest", req.ConfigDigest,
			"seqNr", req.SeqNr,
			"reportLen", len(req.Report),
			"reportInfo", req.ReportInfo,
			"sigCount", len(req.Signatures))
	}

	// Execute transmission across all sub-transmitters
	resp := a.transmitToAll(ctx, req)

	// Convert response to CapabilityResponse
	value, err := capabilities.WrapAny(resp)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to wrap response: %w", err)
	}

	return capabilities.CapabilityResponse{
		Value: value,
	}, nil
}

// transmitToAll fans out the transmission to all configured sub-transmitters
func (a *LLOTransmitAction) transmitToAll(ctx context.Context, req Request) Response {
	a.mu.RLock()
	defer a.mu.RUnlock()

	resp := Response{
		Success:            true,
		TransmitterResults: make([]TransmitterResult, 0, len(a.subTransmitters)),
	}

	// Create report with info for OCR3
	reportWithInfo := ocr3types.ReportWithInfo[llotypes.ReportInfo]{
		Report: req.Report,
		Info:   req.ReportInfo,
	}

	// Fan out to all sub-transmitters in parallel
	g := new(errgroup.Group)
	resultChan := make(chan TransmitterResult, len(a.subTransmitters))

	for i, st := range a.subTransmitters {
		subTransmitter := st
		idx := i
		g.Go(func() error {
			result := TransmitterResult{
				Type:    fmt.Sprintf("transmitter-%d", idx),
				Success: true,
			}

			if err := subTransmitter.Transmit(ctx, req.ConfigDigest, req.SeqNr, reportWithInfo, req.Signatures); err != nil {
				result.Success = false
				result.Error = err.Error()
				a.lggr.Errorw("Sub-transmitter failed",
					"index", idx,
					"error", err,
					"seqNr", req.SeqNr)
			}

			resultChan <- result
			return nil // Don't propagate errors, collect them in results
		})
	}

	// Wait for all transmissions to complete
	_ = g.Wait()
	close(resultChan)

	// Collect results
	for result := range resultChan {
		resp.TransmitterResults = append(resp.TransmitterResults, result)
		if result.Success {
			resp.SuccessfulTransmissions++
		} else {
			resp.FailedTransmissions++
			resp.Success = false
			if resp.Error == "" {
				resp.Error = result.Error
			} else {
				resp.Error = fmt.Sprintf("%s; %s", resp.Error, result.Error)
			}
		}
	}

	a.lggr.Infow("Transmission complete",
		"seqNr", req.SeqNr,
		"successful", resp.SuccessfulTransmissions,
		"failed", resp.FailedTransmissions)

	return resp
}

// Info implements capabilities.BaseCapability
func (a *LLOTransmitAction) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return a.capabilityInfo, nil
}

// RegisterToWorkflow implements capabilities.ActionCapability
func (a *LLOTransmitAction) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	// No special registration needed for this action
	return nil
}

// UnregisterFromWorkflow implements capabilities.ActionCapability
func (a *LLOTransmitAction) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	// No special cleanup needed for this action
	return nil
}

// Start starts the action and all sub-transmitters
func (a *LLOTransmitAction) Start(ctx context.Context) error {
	return a.StartOnce("LLOTransmitAction", func() error {
		// Register with capability registry
		if a.registry != nil {
			if err := a.registry.Add(ctx, a); err != nil {
				return fmt.Errorf("failed to register with capability registry: %w", err)
			}
		}

		// Start all sub-transmitters
		for i, st := range a.subTransmitters {
			if err := st.Start(ctx); err != nil {
				return fmt.Errorf("failed to start sub-transmitter %d: %w", i, err)
			}
		}

		a.lggr.Info("LLO Transmit Action started successfully")
		return nil
	})
}

// Close stops the action and all sub-transmitters
func (a *LLOTransmitAction) Close() error {
	return a.StopOnce("LLOTransmitAction", func() error {
		// Unregister from capability registry
		if a.registry != nil {
			if err := a.registry.Remove(context.Background(), a.capabilityInfo.ID); err != nil {
				a.lggr.Errorw("Failed to unregister from capability registry", "error", err)
			}
		}

		// Stop all sub-transmitters
		var errs []error
		for i, st := range a.subTransmitters {
			if err := st.Close(); err != nil {
				errs = append(errs, fmt.Errorf("failed to close sub-transmitter %d: %w", i, err))
			}
		}

		if len(errs) > 0 {
			return fmt.Errorf("errors closing sub-transmitters: %v", errs)
		}

		a.lggr.Info("LLO Transmit Action closed successfully")
		return nil
	})
}

// HealthReport implements services.Service
func (a *LLOTransmitAction) HealthReport() map[string]error {
	report := map[string]error{a.Name(): a.Healthy()}

	a.mu.RLock()
	defer a.mu.RUnlock()

	for i, st := range a.subTransmitters {
		subReport := st.HealthReport()
		for name, err := range subReport {
			report[fmt.Sprintf("sub-%d-%s", i, name)] = err
		}
	}

	return report
}

// Name implements services.Service
func (a *LLOTransmitAction) Name() string {
	return "LLOTransmitAction"
}









