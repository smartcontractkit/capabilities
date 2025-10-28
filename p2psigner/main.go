package main

import (
	"context"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	action "github.com/smartcontractkit/capabilities/p2psigner/action"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

var _ loop.StandardCapabilities = (*capabilitiesServer)(nil)

type capabilitiesServer struct {
	action             capabilities.ExecutableCapability
	lggr               logger.SugaredLogger
	capabilityRegistry core.CapabilitiesRegistry
}

func New(lggr logger.Logger) *capabilitiesServer {
	return &capabilitiesServer{lggr: logger.Sugared(lggr)}
}

func (cs *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (cs *capabilitiesServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := cs.capabilityRegistry.Remove(ctx, "")
	if err != nil {
		return err
	}

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

	return []capabilities.CapabilityInfo{
		actionInfo,
	}, nil
}

func (cs *capabilitiesServer) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	cs.lggr.Debug("Initialising")

	var err error
	cs.action, err = action.New(action.Params{
		Logger:   cs.lggr,
		Keystore: dependencies.P2PKeystore,
	})
	if err != nil {
		return err
	}

	if err := dependencies.CapabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add P2P signer capability to the capability registry: %w", err)
	}

	cs.capabilityRegistry = dependencies.CapabilityRegistry

	return nil
}

func main() {
	loopserver.ServeNew("Signer", func(s *loop.Server) loop.StandardCapabilities {
		return New(s.Logger)
	})
}
