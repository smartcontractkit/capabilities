package main

import (
	"context"
	"fmt"

	"github.com/pelletier/go-toml/v2"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/echo/action"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

var _ loop.StandardCapabilities = (*capabilitiesServer)(nil)

type Config struct {
	StorageDir string `toml:"storageDir"`
}

type capabilitiesServer struct {
	action capabilities.ExecutableCapability
	lggr   logger.SugaredLogger
}

func New(lggr logger.Logger) *capabilitiesServer {
	return &capabilitiesServer{lggr: logger.Sugared(lggr)}
}

func (cs *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (cs *capabilitiesServer) Close() error {
	return nil
}

func (cs *capabilitiesServer) Ready() error {
	return nil
}

func (cs *capabilitiesServer) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *capabilitiesServer) Name() string {
	return cs.lggr.Name()
}

func (cs *capabilitiesServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	actionInfo, err := cs.action.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{actionInfo}, nil
}

func (cs *capabilitiesServer) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	cs.lggr.Debug("Initialising Echo capability")

	var cfg Config
	if err := toml.Unmarshal([]byte(dependencies.Config), &cfg); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if cfg.StorageDir == "" {
		cfg.StorageDir = "/tmp/echo-capability"
	}

	var err error
	cs.action, err = action.New(cs.lggr, cfg.StorageDir)
	if err != nil {
		return fmt.Errorf("failed to create action: %w", err)
	}

	if err := dependencies.CapabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add Echo capability to the capability registry: %w", err)
	}

	return nil
}

func main() {
	loopserver.ServeNew("Echo", func(s *loop.Server) loop.StandardCapabilities {
		return New(s.Logger)
	})
}
