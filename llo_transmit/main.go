package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/llo_transmit/action"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Initialize logger
	lggr, err := logger.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}

	// Parse configuration from environment or config file
	config, err := loadConfig()
	if err != nil {
		lggr.Fatalw("Failed to load configuration", "error", err)
	}

	// Create sub-transmitters based on configuration
	subTransmitters, err := createSubTransmitters(lggr, config)
	if err != nil {
		lggr.Fatalw("Failed to create sub-transmitters", "error", err)
	}

	// Create the LLO Transmit action
	lloAction, err := action.NewLLOTransmitAction(lggr, config, subTransmitters)
	if err != nil {
		lggr.Fatalw("Failed to create LLO transmit action", "error", err)
	}

	// Start the capability server
	server := loopserver.NewCapabilityServer(lloAction, lggr)
	
	if err := server.Start(ctx); err != nil {
		lggr.Fatalw("Failed to start capability server", "error", err)
	}

	lggr.Info("LLO Transmit capability started successfully")

	// Wait for shutdown signal
	<-ctx.Done()

	lggr.Info("Shutting down LLO Transmit capability...")
	if err := lloAction.Close(); err != nil {
		lggr.Errorw("Error closing LLO action", "error", err)
	}

	lggr.Info("LLO Transmit capability shutdown complete")
}

func loadConfig() (action.Config, error) {
	// Load configuration from environment variable or file
	configJSON := os.Getenv("LLO_TRANSMIT_CONFIG")
	if configJSON == "" {
		// Try loading from file
		configPath := os.Getenv("LLO_TRANSMIT_CONFIG_FILE")
		if configPath == "" {
			configPath = "./config.json"
		}

		data, err := os.ReadFile(configPath)
		if err != nil {
			return action.Config{}, fmt.Errorf("failed to read config file: %w", err)
		}
		configJSON = string(data)
	}

	var config action.Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return action.Config{}, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate configuration
	if config.DonID == 0 {
		return action.Config{}, fmt.Errorf("donID must be specified and not zero")
	}

	if len(config.Servers) == 0 && len(config.Transmitters) == 0 {
		return action.Config{}, fmt.Errorf("at least one server or transmitter must be configured")
	}

	return config, nil
}

func createSubTransmitters(lggr logger.Logger, config action.Config) ([]action.SubTransmitter, error) {
	var subTransmitters []action.SubTransmitter

	// Create transmitters based on configuration
	for _, transmitterCfg := range config.Transmitters {
		switch transmitterCfg.Type {
		case "mercury":
			// TODO: Implement Mercury transmitter adapter
			return nil, fmt.Errorf("mercury transmitter not yet implemented")
		case "cre":
			// TODO: Implement CRE transmitter adapter
			return nil, fmt.Errorf("cre transmitter not yet implemented")
		case "mock":
			// Create mock transmitter for testing
			mockTransmitter := &MockTransmitter{lggr: lggr}
			subTransmitters = append(subTransmitters, mockTransmitter)
		default:
			return nil, fmt.Errorf("unknown transmitter type: %s", transmitterCfg.Type)
		}
	}

	if len(subTransmitters) == 0 {
		return nil, fmt.Errorf("no valid transmitters configured")
	}

	return subTransmitters, nil
}

// MockTransmitter is a simple mock implementation for testing
type MockTransmitter struct {
	lggr logger.Logger
}

func (m *MockTransmitter) Transmit(ctx context.Context, digest []byte, seqNr uint64, report interface{}, sigs interface{}) error {
	m.lggr.Infow("Mock transmitter: would transmit report", "seqNr", seqNr)
	return nil
}

func (m *MockTransmitter) FromAccount(ctx context.Context) ([]byte, error) {
	return []byte("mock-account"), nil
}

func (m *MockTransmitter) Start(ctx context.Context) error {
	m.lggr.Info("Mock transmitter started")
	return nil
}

func (m *MockTransmitter) Close() error {
	m.lggr.Info("Mock transmitter closed")
	return nil
}

func (m *MockTransmitter) HealthReport() map[string]error {
	return map[string]error{"MockTransmitter": nil}
}

func (m *MockTransmitter) Name() string {
	return "MockTransmitter"
}

func (m *MockTransmitter) Ready() error {
	return nil
}









