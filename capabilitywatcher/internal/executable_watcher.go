package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var (
	// Built-in metrics with executable_id labels
	// Counter metrics
	executableRegistrationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_executable_registrations_total",
			Help: "Total number of executable registrations to workflow",
		},
		[]string{"executable_id"},
	)
	executableUnregistrationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_executable_unregistrations_total",
			Help: "Total number of executable unregistrations from workflow",
		},
		[]string{"executable_id"},
	)
	executableExecutionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_executable_executions_total",
			Help: "Total number of executable executions",
		},
		[]string{"executable_id"},
	)
	executableExecutionErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_executable_execution_errors_total",
			Help: "Total number of executable execution errors",
		},
		[]string{"executable_id"},
	)
	executableRegistrationErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_executable_registration_errors_total",
			Help: "Total number of failed executable registration attempts",
		},
		[]string{"executable_id"},
	)
	executableUnregistrationErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_executable_unregistration_errors_total",
			Help: "Total number of failed executable unregistration attempts",
		},
		[]string{"executable_id"},
	)

	// Histogram metrics
	executableRegistrationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "capability_checker_executable_registration_duration_milliseconds",
			Help:    "Time taken to register executables to workflow",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"executable_id"},
	)
	executableUnregistrationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "capability_checker_executable_unregistration_duration_milliseconds",
			Help:    "Time taken to unregister executables from workflow",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"executable_id"},
	)
	executableExecutionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "capability_checker_executable_execution_duration_milliseconds",
			Help:    "Time taken to execute executables",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"executable_id"},
	)
	executableLifecycleDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "capability_checker_executable_lifecycle_duration_seconds",
			Help:    "Full cycle time from register to execute to unregister",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"executable_id"},
	)
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
	lifecycleTimeStart := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			switch state {
			case StateRegisterToWorkflow:
				lifecycleTimeStart = time.Now()
				if err := e.registerToWorkflow(ctx); err != nil {
					executableRegistrationErrorsTotal.WithLabelValues(e.executableID).Inc()
					return fmt.Errorf("failed to register executable: %w", err)
				}
				executableRegistrationsTotal.WithLabelValues(e.executableID).Inc()
				state = StateExecute
				executeCount = 0
			case StateExecute:
				executeCount++
				response, err := e.execute(ctx)
				if err != nil {
					executableExecutionErrorsTotal.WithLabelValues(e.executableID).Inc()
					return fmt.Errorf("failed to execute executable: %w", err)
				}
				executableExecutionsTotal.WithLabelValues(e.executableID).Inc()
				e.lggr.Debugf("execute state %d of %d for executable %s", executeCount, e.executeSteps, e.executableID)

				if executeCount >= e.executeSteps {
					state = StateUnregisterFromWorkflow
				}
				e.checker.Assert(response)
			case StateUnregisterFromWorkflow:
				if err := e.unregisterFromWorkflow(ctx); err != nil {
					executableUnregistrationErrorsTotal.WithLabelValues(e.executableID).Inc()
					return fmt.Errorf("failed to unregister executable: %w", err)
				}
				executableUnregistrationsTotal.WithLabelValues(e.executableID).Inc()
				state = StateRegisterToWorkflow
				executableLifecycleDuration.WithLabelValues(e.executableID).Observe(time.Since(lifecycleTimeStart).Seconds())
			}
		}
	}
}

func (e *ExecutableWatcher) registerToWorkflow(ctx context.Context) error {
	registrationRequest, err := e.checker.CreateRegisterToWorkflowRequest()
	if err != nil {
		return err
	}

	start := time.Now()
	err = e.executableCapability.RegisterToWorkflow(ctx, registrationRequest)
	if err == nil {
		executableRegistrationDuration.WithLabelValues(e.executableID).Observe(float64(time.Since(start).Milliseconds()))
	}
	return err
}

func (e *ExecutableWatcher) unregisterFromWorkflow(ctx context.Context) error {
	unregistrationRequest, err := e.checker.CreateUnregisterFromWorkflowRequest()
	if err != nil {
		return err
	}

	start := time.Now()
	err = e.executableCapability.UnregisterFromWorkflow(ctx, unregistrationRequest)
	if err == nil {
		executableUnregistrationDuration.WithLabelValues(e.executableID).Observe(float64(time.Since(start).Milliseconds()))
	}
	return err
}

func (e *ExecutableWatcher) execute(ctx context.Context) (capabilities.CapabilityResponse, error) {
	request, err := e.checker.CreateExecuteRequest()
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	start := time.Now()
	res, err := e.executableCapability.Execute(ctx, request)
	if err == nil {
		executableExecutionDuration.WithLabelValues(e.executableID).Observe(float64(time.Since(start).Milliseconds()))
	}
	return res, err
}
