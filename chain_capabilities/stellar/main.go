package main

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	stellarcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/actions"
	consmetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

const (
	CapabilityName = "stellar"
)

// capabilityGRPCService is the top-level server wrapping the Stellar capability.
// It implements loop.StandardCapabilities.
type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	chainSelector uint64
	capability
	lggr          logger.Logger
	limitsFactory limits.Factory
}

type capability struct {
	*actions.Stellar
}

var _ stellarcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return stellarcapserver.NewClientServer(&capabilityGRPCService{
			lggr:          s.Logger.Named(CapabilityName),
			limitsFactory: s.LimitsFactory,
		})
	}, loop.WithOtelViews(consmetrics.MetricViews()))
}

func (c *capabilityGRPCService) ChainSelector() uint64 {
	return c.chainSelector
}

func (c *capabilityGRPCService) Start(_ context.Context) error {
	return fmt.Errorf("unimplemented")
}

func (c *capabilityGRPCService) Close() error {
	return fmt.Errorf("unimplemented")
}

func (c *capabilityGRPCService) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *capabilityGRPCService) Name() string {
	return c.lggr.Name()
}

func (c *capabilityGRPCService) Description() string {
	return "Contains Stellar chain functionalities"
}

func (c *capabilityGRPCService) Ready() error {
	return fmt.Errorf("unimplemented")
}

func (c *capabilityGRPCService) Initialise(_ context.Context, _ core.StandardCapabilitiesDependencies) error {
	return fmt.Errorf("unimplemented")
}
