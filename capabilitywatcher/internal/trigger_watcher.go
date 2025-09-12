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
	// Built-in metrics with trigger_id labels
	// Counter metrics
	triggerRegistrationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_trigger_registrations_total",
			Help: "Total number of trigger registrations",
		},
		[]string{"trigger_id"},
	)
	triggerUnregistrationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_trigger_unregistrations_total",
			Help: "Total number of trigger unregistrations",
		},
		[]string{"trigger_id"},
	)
	triggerEventsReceivedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_trigger_events_received_total",
			Help: "Total number of trigger events received",
		},
		[]string{"trigger_id"},
	)
	triggerEventsErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_trigger_events_errors_total",
			Help: "Total number of trigger error events received",
		},
		[]string{"trigger_id"},
	)
	triggerRegistrationErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_trigger_registration_errors_total",
			Help: "Total number of failed trigger registration attempts",
		},
		[]string{"trigger_id"},
	)
	triggerUnregistrationErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "capability_checker_trigger_unregistration_errors_total",
			Help: "Total number of failed trigger unregistration attempts",
		},
		[]string{"trigger_id"},
	)

	// Histogram metrics
	triggerRegistrationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "capability_checker_trigger_registration_duration_milliseconds",
			Help:    "Time taken to register triggers",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"trigger_id"},
	)
	triggerUnregistrationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "capability_checker_trigger_unregistration_duration_milliseconds",
			Help:    "Time taken to unregister triggers",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"trigger_id"},
	)
	triggerLifecycleDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "capability_checker_trigger_lifecycle_duration_seconds",
			Help:    "Full cycle time from register to idle to unregister",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"trigger_id"},
	)
)

type TriggerChecker interface {
	NewRegistrationRequest() (capabilities.TriggerRegistrationRequest, error)
	Assert(capabilities.TriggerResponse)
}

// TriggerWatcher manages the lifecycle of a trigger capability with configurable idle periods.
// It operates as a state machine that cycles between registering, idling, and unregistering triggers.
type TriggerWatcher struct {
	lggr                       logger.Logger
	capabilitiesRegistry       core.CapabilitiesRegistry
	triggerCapability          capabilities.TriggerCapability
	triggerRegistrationRequest capabilities.TriggerRegistrationRequest
	triggerCh                  <-chan capabilities.TriggerResponse
	idleSteps                  int    // Number of idle cycles before unregistering
	triggerID                  string // Identifier for the trigger capability
	tickPeriod                 time.Duration
	lifecycleStartTime         time.Time
	triggerChecker             TriggerChecker
}

// TriggerWatcherOption allows customizing TriggerWatcher configuration
type TriggerWatcherOption func(*TriggerWatcher)

// WithIdleSteps sets custom idle steps
func WithIdleSteps(steps int) TriggerWatcherOption {
	return func(tw *TriggerWatcher) {
		tw.idleSteps = steps
	}
}

// WithTickPeriod sets custom tick period
func WithTickPeriod(period time.Duration) TriggerWatcherOption {
	return func(tw *TriggerWatcher) {
		tw.tickPeriod = period
	}
}

// TriggerState represents the current state of the trigger watcher state machine
type TriggerState int

const (
	StateRegister   TriggerState = iota // Register the trigger capability
	StateIdle                           // Wait for configured number of cycles
	StateUnregister                     // Unregister the trigger capability
)

// NewTriggerWatcher creates a TriggerWatcher with optional configuration
func NewTriggerWatcher(
	logger logger.Logger,
	capabilitiesRegistry core.CapabilitiesRegistry,
	triggerID string,
	triggerChecker TriggerChecker,
	opts ...TriggerWatcherOption,
) (*TriggerWatcher, error) {
	// Validation
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if capabilitiesRegistry == nil {
		return nil, fmt.Errorf("capabilities registry is required")
	}
	if triggerID == "" {
		return nil, fmt.Errorf("trigger ID cannot be empty")
	}
	triggerRegistrationRequest, err := triggerChecker.NewRegistrationRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to create trigger registration request: %w", err)
	}

	tw := &TriggerWatcher{
		lggr:                       logger,
		capabilitiesRegistry:       capabilitiesRegistry,
		triggerID:                  triggerID,
		triggerRegistrationRequest: triggerRegistrationRequest,
		// Defaults
		idleSteps:  5,
		tickPeriod: 30 * time.Second,
	}

	// Apply options
	for _, opt := range opts {
		opt(tw)
	}

	err = tw.initTriggerCapability(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize trigger capability: %w", err)
	}

	return tw, nil
}

// Run runs the main state machine loop with 30-second (configurable `tickPeriod`) intervals.
// It cycles through: Register -> Idle (configurable `idleSteps`) -> Unregister -> repeat
func (t *TriggerWatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(t.tickPeriod)
	defer ticker.Stop()

	state := StateRegister
	idleCounter := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			switch state {
			case StateRegister:
				if err := t.register(ctx); err != nil {
					return fmt.Errorf("failed to register trigger: %w", err)
				}
				state = StateIdle
				idleCounter = 0
			case StateIdle:
				idleCounter++
				t.lggr.Debugf("Idle state %d of %d for trigger %s", idleCounter, t.idleSteps, t.triggerID)

				if idleCounter >= t.idleSteps {
					state = StateUnregister
				}
			case StateUnregister:
				if err := t.unregister(ctx); err != nil {
					return fmt.Errorf("failed to unregister trigger: %w", err)
				}
				state = StateRegister
			}
		}
	}
}

// initTriggerCapability initializes the trigger capability from the registry
func (t *TriggerWatcher) initTriggerCapability(ctx context.Context) error {
	capability, err := t.capabilitiesRegistry.GetTrigger(ctx, t.triggerID)
	if err != nil {
		t.lggr.Errorf("Failed to get trigger capability '%s' from registry: %v", t.triggerID, err)
		return fmt.Errorf("failed to get trigger capability '%s': %w", t.triggerID, err)
	}
	t.triggerCapability = capability
	t.lggr.Infof("Successfully initialized trigger capability: %s", t.triggerID)
	return nil
}

// register registers the trigger capability and starts monitoring
func (t *TriggerWatcher) register(ctx context.Context) error {
	t.lifecycleStartTime = time.Now()
	triggerCh, err := t.triggerCapability.RegisterTrigger(ctx, t.triggerRegistrationRequest)
	triggerRegistrationDuration.WithLabelValues(t.triggerID).Observe(float64(time.Since(t.lifecycleStartTime).Milliseconds()))
	if err != nil {
		triggerRegistrationErrorsTotal.WithLabelValues(t.triggerID).Inc()
		t.lggr.Errorf("Failed to register trigger '%s': %v", t.triggerID, err)
		return fmt.Errorf("failed to register trigger '%s': %w", t.triggerID, err)
	}

	t.triggerCh = triggerCh

	triggerRegistrationsTotal.WithLabelValues(t.triggerID).Inc()
	t.lggr.Infof("Successfully registered trigger: %s", t.triggerID)
	go t.monitor()
	return nil
}

// unregister unregisters the trigger capability
func (t *TriggerWatcher) unregister(ctx context.Context) error {
	start := time.Now()
	err := t.triggerCapability.UnregisterTrigger(ctx, t.triggerRegistrationRequest)
	triggerUnregistrationDuration.WithLabelValues(t.triggerID).Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		triggerUnregistrationErrorsTotal.WithLabelValues(t.triggerID).Inc()
		t.lggr.Errorf("Failed to unregister trigger '%s': %v", t.triggerID, err)
		return fmt.Errorf("failed to unregister trigger '%s': %w", t.triggerID, err)
	}

	triggerUnregistrationsTotal.WithLabelValues(t.triggerID).Inc()
	// Record full lifecycle duration
	if !t.lifecycleStartTime.IsZero() {
		triggerLifecycleDuration.WithLabelValues(t.triggerID).Observe(time.Since(t.lifecycleStartTime).Seconds())
	}
	t.lggr.Infof("Successfully unregistered trigger: %s", t.triggerID)
	return nil
}

// monitor listens for trigger events on the channel and handles metrics
func (t *TriggerWatcher) monitor() {
	t.lggr.Infof("Starting monitor for trigger: %s", t.triggerID)

	for {
		select {
		case m, ok := <-t.triggerCh:
			if !ok {
				t.lggr.Infof("Trigger channel closed for: %s", t.triggerID)
				return
			}
			triggerEventsReceivedTotal.WithLabelValues(t.triggerID).Inc()

			if m.Err != nil {
				t.lggr.Errorf("Error received from trigger: %s, %v", t.triggerID, m.Err)
				triggerEventsErrorsTotal.WithLabelValues(t.triggerID).Inc()
				continue
			}
			t.triggerChecker.Assert(m)

			t.lggr.Debugf("Received trigger event for: %s", t.triggerID)
		}
	}
}
