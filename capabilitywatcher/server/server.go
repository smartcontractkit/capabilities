package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/capabilities/capabilitywatcher/checks"
	"github.com/smartcontractkit/capabilities/capabilitywatcher/internal"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Ensure HealthCheckServer implements the StandardCapabilities interface
var _ loop.StandardCapabilities = (*CapabilityWatcherServer)(nil)

// CapabilityWatcherServer monitors the health of local capabilities in the node
type CapabilityWatcherServer struct {
	Lggr        logger.Logger
	capRegistry core.CapabilitiesRegistry

	// Service management
	runningServices map[string]context.CancelFunc
	servicesMutex   sync.Mutex
	serverCancel    context.CancelFunc
}

// Start begins the health check monitoring process
func (s *CapabilityWatcherServer) Start(ctx context.Context) error {
	s.Lggr.Info("Starting capability watcher server")
	serverCtx, serverCancel := context.WithCancel(ctx)
	s.serverCancel = serverCancel
	go s.runLoop(serverCtx)
	return nil
}

// Close shuts down the health check server
func (s *CapabilityWatcherServer) Close() error {
	s.Lggr.Info("Closing capability watcher server")

	// Cancel all running services
	s.servicesMutex.Lock()
	for capID, cancelFunc := range s.runningServices {
		s.Lggr.Infof("Stopping service for capability: %s", capID)
		cancelFunc()
	}
	s.runningServices = make(map[string]context.CancelFunc)

	// Cancel server context and set to nil
	if s.serverCancel != nil {
		s.serverCancel()
		s.serverCancel = nil
	}
	s.servicesMutex.Unlock()

	return nil
}

// Ready returns whether the server is ready to serve requests
func (s *CapabilityWatcherServer) Ready() error {
	s.servicesMutex.Lock()
	defer s.servicesMutex.Unlock()
	if s.serverCancel == nil {
		return errors.New("capability watcher server is not running")
	}
	return nil
}

// HealthReport returns the current health status of monitored components
func (s *CapabilityWatcherServer) HealthReport() map[string]error {
	s.servicesMutex.Lock()
	defer s.servicesMutex.Unlock()

	healthReport := make(map[string]error)

	// Check overall server health
	if s.serverCancel == nil {
		healthReport["server"] = errors.New("capability watcher server is not running")
		return healthReport
	}

	// Server is running
	healthReport["server"] = nil

	// Report on running services
	for capID := range s.runningServices {
		// Service is running (no error)
		healthReport[capID] = nil
	}

	// If no services are running, indicate this
	if len(s.runningServices) == 0 {
		healthReport["services"] = errors.New("no capability services are currently running")
	} else {
		healthReport["services"] = nil
	}

	return healthReport
}

// Name returns the name of this server
func (s *CapabilityWatcherServer) Name() string {
	return s.Lggr.Name()
}

// Initialise sets up the health check server with required dependencies
func (s *CapabilityWatcherServer) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	s.Lggr.Info("Initializing capability watcher server")

	if dependencies.CapabilityRegistry == nil {
		return errors.New("capability registry cannot be nil")
	}

	s.capRegistry = dependencies.CapabilityRegistry
	return s.Start(ctx)
}

// Infos returns information about the capabilities provided by this server
func (s *CapabilityWatcherServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	return []capabilities.CapabilityInfo{}, nil
}

// New creates a new CapabilityWatcherServer instance
func New(lggr logger.Logger) *CapabilityWatcherServer {
	if lggr == nil {
		panic("logger cannot be nil")
	}
	return &CapabilityWatcherServer{
		Lggr:            logger.Sugared(lggr).Named("CapabilityWatcherServer"),
		runningServices: make(map[string]context.CancelFunc),
	}
}

// runLoop continuously monitors capabilities and performs health checks
func (s *CapabilityWatcherServer) runLoop(ctx context.Context) {
	s.Lggr.Info("Starting capability watcher loop")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.Lggr.Info("Health check loop stopped due to context cancellation")
			return
		case <-ticker.C:
			s.performChecks(ctx)
		}
	}
}

// performChecks checks the health of all registered capabilities
func (s *CapabilityWatcherServer) performChecks(ctx context.Context) {
	caps, err := s.capRegistry.List(ctx)
	if err != nil {
		s.Lggr.Errorf("Failed to list capabilities: %v", err)
		return
	}

	s.Lggr.Debugf("Checking health of %d capabilities", len(caps))

	for _, c := range caps {
		if err2 := s.checkCapability(ctx, c); err2 != nil {
			s.Lggr.Errorf("Health check failed for capability: %v", err2)
		}
	}
}

// checkCapability performs health check on a single capability
func (s *CapabilityWatcherServer) checkCapability(ctx context.Context, c capabilities.BaseCapability) error {
	info, err := c.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to get capability info: %w", err)
	}

	if !info.IsLocal {
		s.Lggr.Debugf("Skipping remote capability: %s", info.ID)
		return nil
	}

	// Check if service is already running
	s.servicesMutex.Lock()
	_, isRunning := s.runningServices[info.ID]
	s.servicesMutex.Unlock()

	if isRunning {
		s.Lggr.Debugf("Service already running for capability: %s", info.ID)
		return nil
	}

	// Start service for specific capability types
	switch info.ID {
	case "cron-trigger@1.0.0":
		return s.startCronTriggerService(ctx, info.ID)
	case "http-actions@1.0.0-alpha":
		return s.startHTTPActionService(ctx, info.ID)
	default:
		s.Lggr.Infof("No service defined for capability: %s", info.ID)
	}

	return nil
}

func (s *CapabilityWatcherServer) startCronTriggerService(ctx context.Context, capID string) error {
	serviceCtx, cancel := context.WithCancel(ctx)

	s.servicesMutex.Lock()
	s.runningServices[capID] = cancel
	s.servicesMutex.Unlock()

	cronWatcher, err := internal.NewTriggerWatcher(s.Lggr, s.capRegistry, capID, checks.CronChecker{})
	if err != nil {
		cancel()
		s.servicesMutex.Lock()
		delete(s.runningServices, capID)
		s.servicesMutex.Unlock()
		return fmt.Errorf("failed to create cron trigger watcher: %w", err)
	}

	// Start service in goroutine
	go func() {
		defer func() {
			s.servicesMutex.Lock()
			delete(s.runningServices, capID)
			s.servicesMutex.Unlock()
			s.Lggr.Infof("Cron trigger service stopped: %s", capID)
		}()

		s.Lggr.Infof("Starting cron trigger service: %s", capID)
		if err := cronWatcher.Run(serviceCtx); err != nil && !errors.Is(err, context.Canceled) {
			s.Lggr.Errorf("Cron trigger service error: %v", err)
		}
	}()

	return nil
}

// checkHttpAction performs health check specifically for HTTP action capability
func (s *CapabilityWatcherServer) startHTTPActionService(ctx context.Context, capID string) error {
	serviceCtx, cancel := context.WithCancel(ctx)

	s.servicesMutex.Lock()
	s.runningServices[capID] = cancel
	s.servicesMutex.Unlock()

	httpWatcher, err := internal.NewExecutableWatcher(s.Lggr, s.capRegistry, capID, checks.HTTPActionChecker{})
	if err != nil {
		cancel()
		s.servicesMutex.Lock()
		delete(s.runningServices, capID)
		s.servicesMutex.Unlock()
		return fmt.Errorf("failed to create HTTP action watcher: %w", err)
	}

	// Start service in goroutine
	go func() {
		defer func() {
			s.servicesMutex.Lock()
			delete(s.runningServices, capID)
			s.servicesMutex.Unlock()
			s.Lggr.Infof("HTTP action service stopped: %s", capID)
		}()

		s.Lggr.Infof("Starting HTTP action service: %s", capID)
		if err := httpWatcher.Run(serviceCtx); err != nil && !errors.Is(err, context.Canceled) {
			s.Lggr.Errorf("HTTP action service error: %v", err)
		}
	}()

	return nil
}
