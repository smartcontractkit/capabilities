package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// ExecutableState represents the current state of the executable watcher state machine
type ExecutableState int

const (
	StateRegisterToWorkflow     ExecutableState = iota // Register the trigger capability
	StateExecute                                       // Wait for configured number of cycles
	StateUnregisterFromWorkflow                        // Unregister the trigger capability
)

type ExecutableChecker interface {
	CreateRegisterToWorkflowRequest() (capabilities.RegisterToWorkflowRequest, error)
	CreateUnregisterFromWorkflowRequest() (capabilities.UnregisterFromWorkflowRequest, error)
	CreateExecuteRequest() (capabilities.CapabilityRequest, error)
	Assert(capabilities.CapabilityResponse)
}

type ExecutableWatcher struct {
	lggr                 logger.Logger
	capabilitiesRegistry core.CapabilitiesRegistry
	checker              ExecutableChecker
	tickPeriod           time.Duration
	executeSteps         int
	executableID         string
	executableCapability capabilities.Executable
}

// ExecutableWatcherOption allows customizing ExecutableWatcher configuration
type ExecutableWatcherOption func(*ExecutableWatcher)

// WithExecuteSteps sets custom execute steps
func WithExecuteSteps(steps int) ExecutableWatcherOption {
	return func(ew *ExecutableWatcher) {
		ew.executeSteps = steps
	}
}

// NewExecutableWatcher creates an ExecutableWatcher with optional configuration
func NewExecutableWatcher(
	logger logger.Logger,
	capabilitiesRegistry core.CapabilitiesRegistry,
	executableID string,
	checker ExecutableChecker,
	opts ...ExecutableWatcherOption,
) (*ExecutableWatcher, error) {
	// Validation
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if capabilitiesRegistry == nil {
		return nil, fmt.Errorf("capabilities registry is required")
	}
	if executableID == "" {
		return nil, fmt.Errorf("executable ID cannot be empty")
	}
	if checker == nil {
		return nil, fmt.Errorf("checker is required")
	}

	ew := &ExecutableWatcher{
		lggr:                 logger,
		capabilitiesRegistry: capabilitiesRegistry,
		executableID:         executableID,
		checker:              checker,
		// Defaults
		executeSteps: 5,
		tickPeriod:   30 * time.Second,
	}

	// Apply options
	for _, opt := range opts {
		opt(ew)
	}

	err := ew.initExecutableCapability(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize executable capability: %w", err)
	}

	return ew, nil
}

// initExecutableCapability initializes the executable capability from the registry
func (e *ExecutableWatcher) initExecutableCapability(ctx context.Context) error {
	capability, err := e.capabilitiesRegistry.GetExecutable(ctx, e.executableID)
	if err != nil {
		e.lggr.Errorf("Failed to get executable capability '%s' from registry: %v", e.executableID, err)
		return fmt.Errorf("failed to get executable capability '%s': %w", e.executableID, err)
	}
	e.executableCapability = capability
	e.lggr.Infof("Successfully initialized executable capability: %s", e.executableID)
	return nil
}

// Run runs the main state machine loop with 30-second (configurable `tickPeriod`) intervals.
// It cycles through: RegisterToWorkflow -> Execute (configurable `executeSteps`) -> UnregisterFromWorkflow -> repeat
func (e *ExecutableWatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.tickPeriod)
	defer ticker.Stop()

	state := StateRegisterToWorkflow
	executeCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			switch state {
			case StateRegisterToWorkflow:
				if err := e.registerToWorkflow(ctx); err != nil {
					return fmt.Errorf("failed to register executable: %w", err)
				}
				state = StateExecute
				executeCount = 0
			case StateExecute:
				executeCount++
				response, err := e.execute(ctx)
				if err != nil {
					return fmt.Errorf("failed to execute executable: %w", err)
				}
				e.lggr.Debugf("execute state %d of %d for executable %s", executeCount, e.executeSteps, e.executableID)

				if executeCount >= e.executeSteps {
					state = StateUnregisterFromWorkflow
				}
				e.checker.Assert(response)
			case StateUnregisterFromWorkflow:
				if err := e.unregisterFromWorkflow(ctx); err != nil {
					return fmt.Errorf("failed to unregister executable: %w", err)
				}
				state = StateRegisterToWorkflow
			}
		}
	}
}

func (e *ExecutableWatcher) registerToWorkflow(ctx context.Context) error {
	registrationRequest, err := e.checker.CreateRegisterToWorkflowRequest()
	if err != nil {
		return err
	}
	return e.executableCapability.RegisterToWorkflow(ctx, registrationRequest)
}

func (e *ExecutableWatcher) unregisterFromWorkflow(ctx context.Context) error {
	unregistrationRequest, err := e.checker.CreateUnregisterFromWorkflowRequest()
	if err != nil {
		return err
	}
	return e.executableCapability.UnregisterFromWorkflow(ctx, unregistrationRequest)
}

func (e *ExecutableWatcher) execute(ctx context.Context) (capabilities.CapabilityResponse, error) {
	request, err := e.checker.CreateExecuteRequest()
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	return e.executableCapability.Execute(ctx, request)
}
