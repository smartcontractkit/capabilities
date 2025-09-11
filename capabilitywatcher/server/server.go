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

// HealthCheckServer monitors the health of local capabilities in the node
type CapabilityWatcherServer struct {
	Lggr        logger.Logger
	capRegistry core.CapabilitiesRegistry

	runningChecks map[string]bool
	checksMutex   sync.Mutex
}

// Start begins the health check monitoring process
func (s *CapabilityWatcherServer) Start(ctx context.Context) error {
	s.Lggr.Info("Starting health check server")
	go s.runLoop(ctx) // Use the provided context instead of creating a new one
	return nil
}

// Close shuts down the health check server
func (s *CapabilityWatcherServer) Close() error {
	s.Lggr.Info("Closing health check server")
	return nil
}

// Ready returns whether the server is ready to serve requests
func (s *CapabilityWatcherServer) Ready() error {
	return nil
}

// HealthReport returns the current health status of monitored components
func (s *CapabilityWatcherServer) HealthReport() map[string]error {
	return nil
}

// Name returns the name of this server
func (s *CapabilityWatcherServer) Name() string {
	return "CapabilityWatcherServer"
}

// Initialise sets up the health check server with required dependencies
func (s *CapabilityWatcherServer) Initialise(ctx context.Context, config string, telemetryService core.TelemetryService, store core.KeyValueStore, capabilityRegistry core.CapabilitiesRegistry, errorLog core.ErrorLog, pipelineRunner core.PipelineRunnerService, relayerSet core.RelayerSet, oracleFactory core.OracleFactory, gatewayConnector core.GatewayConnector, p2pKeystore core.Keystore) error {
	s.Lggr.Info("Initializing health check server")

	if capabilityRegistry == nil {
		return errors.New("capability registry cannot be nil")
	}

	s.capRegistry = capabilityRegistry
	return s.Start(ctx)
}

// Infos returns information about the capabilities provided by this server
func (s *CapabilityWatcherServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	return []capabilities.CapabilityInfo{
		{
			ID:             "healthcheck",
			CapabilityType: "trigger",
			Description:    "Health check proof of concept",
			IsLocal:        true,
		},
	}, nil
}

// New creates a new HealthCheckServer instance
func New(lggr logger.Logger) *CapabilityWatcherServer {
	return &CapabilityWatcherServer{
		Lggr:          logger.Sugared(lggr),
		runningChecks: make(map[string]bool),
	}
}

// runLoop continuously monitors capabilities and performs health checks
func (s *CapabilityWatcherServer) runLoop(ctx context.Context) {
	s.Lggr.Info("Starting health check monitoring loop")

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
	// Get capability information
	info, err := c.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to get capability info: %w", err)
	}

	// Only check local capabilities
	if !info.IsLocal {
		s.Lggr.Debugf("Skipping remote capability: %s", info.ID)
		return nil
	}

	// Check if this capability check is already running
	s.checksMutex.Lock()
	isRunning := s.runningChecks[info.ID]
	s.checksMutex.Unlock()

	if isRunning {
		s.Lggr.Debugf("Health check already running for capability: %s", info.ID)
		return nil
	}

	s.Lggr.Debugf("Performing health check for local capability: %s", info.ID)

	// Handle specific capability types
	switch info.ID {
	case "cron-trigger@1.0.0":
		return s.checkCronTrigger(ctx)
	case "read-contract@1.0.0":
		return s.checkCronTrigger(ctx)
	default:
		s.Lggr.Debugf("No specific health check for capability: %s", info.ID)
	}

	return nil
}

// checkCronTrigger performs health check specifically for cron trigger capability
func (s *CapabilityWatcherServer) checkCronTrigger(ctx context.Context) error {
	s.checksMutex.Lock()
	s.runningChecks["cron-trigger@1.0.0"] = true // TODO: make the lock less hacky + add cleanup
	s.checksMutex.Unlock()

	cc := checks.CronChecker{}

	cronWatcher, err := internal.NewTriggerWatcher(s.Lggr, s.capRegistry, "cron-trigger@1.0.0", cc)
	if err != nil {
		return fmt.Errorf("failed to create cron trigger watcher: %w", err)
	}

	// Run the watcher with the provided context
	cronWatcher.Run(ctx)
	s.Lggr.Debug("Cron trigger health check completed successfully")

	return nil
}

// checkReadContract performs health check specifically for cron trigger capability
func (s *CapabilityWatcherServer) checkReadContract(ctx context.Context) error {
	s.checksMutex.Lock()
	s.runningChecks["read-contract@1.0.0"] = true // TODO: make the lock less hacky + add cleanup
	s.checksMutex.Unlock()

	cc := checks.ReadContractChecker{}

	cronWatcher, err := internal.NewExecutableWatcher(s.Lggr, s.capRegistry, "read-contract@1.0.0", cc)
	if err != nil {
		return fmt.Errorf("failed to create read contract watcher: %w", err)
	}

	// Run the watcher with the provided context
	cronWatcher.Run(ctx)
	s.Lggr.Debug("Read contract executable health check completed successfully")

	return nil
}
