package main

import (
	"context"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

const (
	serviceName = "LoadTestWriteTarget"
)

type readContractAction interface {
	capabilities.ExecutableCapability
	Start(context.Context) error
	Close() error
}

type LoadTestWriteTargetGRPCService struct {
	services.StateMachine
	action             readContractAction
	lggr               logger.Logger
	capabilityRegistry core.CapabilitiesRegistry
}

func main() {
	loopserver.ServeNew(serviceName, func(s *loop.Server) loop.StandardCapabilities {
		return &LoadTestWriteTargetGRPCService{lggr: s.Logger}
	})
}

func (cs *LoadTestWriteTargetGRPCService) Start(ctx context.Context) error {
	return nil
}

func (cs *LoadTestWriteTargetGRPCService) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := cs.action.Info(ctx)
	if err != nil {
		return err
	}
	err = cs.capabilityRegistry.Remove(ctx, info.ID)
	if err != nil {
		return err
	}
	return nil
}

func (cs *LoadTestWriteTargetGRPCService) Ready() error {
	return nil
}

func (cs *LoadTestWriteTargetGRPCService) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *LoadTestWriteTargetGRPCService) Name() string {
	return cs.lggr.Name()
}

func (cs *LoadTestWriteTargetGRPCService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	triggerInfo, err := cs.action.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		triggerInfo,
	}, nil
}

func (cs *LoadTestWriteTargetGRPCService) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	return nil
}
