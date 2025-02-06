package main

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/loadtestproxy/internal"
	"github.com/smartcontractkit/capabilities/loadtestproxy/internal/pb"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	serviceName = "CapabilityRegistryProxy"
)

type CapProxyGRPCService struct {
	services.StateMachine
	lggr               logger.Logger
	capabilityRegistry core.CapabilitiesRegistry
}

func main() {
	loopserver.Serve(serviceName, func(lggr logger.Logger) *CapProxyGRPCService {
		return &CapProxyGRPCService{lggr: lggr}
	})
}

func (cs *CapProxyGRPCService) Start(ctx context.Context) error {
	return nil
}

func (cs *CapProxyGRPCService) Close() error {
	//TODO: close GRPC server™£
	return nil
}

func (cs *CapProxyGRPCService) Ready() error {
	return nil
}

func (cs *CapProxyGRPCService) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *CapProxyGRPCService) Name() string {
	return cs.lggr.Name()
}

func (cs *CapProxyGRPCService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	//TODO: add this
	return nil, nil
}

func (cs *CapProxyGRPCService) Initialise(
	ctx context.Context,
	config string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	_ core.RelayerSet,
	_ core.OracleFactory,
) error {
	//TODO @george-dorin:
	// - Add port to config
	// - Add auto register in config
	cs.lggr.Info("Starting proxy")
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", 3456))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)
	pb.RegisterProxyServer(grpcServer, internal.NewMockServer(capabilityRegistry, cs.lggr))
	grpcServer.Serve(lis)

	return nil
}
