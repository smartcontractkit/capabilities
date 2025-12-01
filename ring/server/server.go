package server

import (
	"context"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/ring/internal/environment"
	"github.com/smartcontractkit/capabilities/ring/internal/request"
	"github.com/smartcontractkit/capabilities/ring/plugin"
)

// Server implements the Ring capability server
type Server struct {
	lggr           logger.Logger
	store          *requests.Store[*request.Request]
	scaler         environment.Scaler
	requestTimeout time.Duration
	timeToSync     time.Duration
	f              int
}

// New creates a new Ring capability server
func New(lggr logger.Logger, requestTimeout, timeToSync time.Duration, f int) loop.StandardCapabilities {
	store := requests.NewStore[*request.Request]()
	scaler := environment.NewSimpleScaler(1)

	return &Server{
		lggr:           lggr,
		store:          store,
		scaler:         scaler,
		requestTimeout: requestTimeout,
		timeToSync:     timeToSync,
		f:              f,
	}
}

// GetPluginFactory returns the plugin factory for this capability
func (s *Server) GetPluginFactory() interface{} {
	return plugin.NewPluginFactory(
		s.store,
		s.scaler,
		s.requestTimeout,
		s.timeToSync,
		s.f,
	)
}

// GetStore returns the request store
func (s *Server) GetStore() *requests.Store[*request.Request] {
	return s.store
}

// GetScaler returns the environment scaler
func (s *Server) GetScaler() environment.Scaler {
	return s.scaler
}

// Close closes the server
func (s *Server) Close() error {
	return nil
}

// Name returns the name of the capability
func (s *Server) Name() string {
	return "RingCapability"
}

// Ready returns nil if the server is ready
func (s *Server) Ready() error {
	return nil
}

// HealthReport returns the health status of the server
func (s *Server) HealthReport() map[string]error {
	return map[string]error{s.Name(): nil}
}

// Start starts the server
func (s *Server) Start(ctx context.Context) error {
	return nil
}

// Initialise initializes the server with dependencies
func (s *Server) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	// Ring capability doesn't require special initialization
	return nil
}

// Infos returns the capability infos for this server
func (s *Server) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	// Ring capability provides consensus for request routing
	return []capabilities.CapabilityInfo{{
		ID:             "ring@1.0.0",
		CapabilityType: capabilities.CapabilityTypeConsensus,
		Description:    "Ring routing consensus capability using OCR3",
		IsLocal:        false,
	}}, nil
}
