package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/loadtestproxy/internal"
	"github.com/smartcontractkit/capabilities/loadtestproxy/internal/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

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
	grpcServer         *grpc.Server
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
	cs.grpcServer.Stop()
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
	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second, // Send keepalive ping every 30s
			Timeout: 10 * time.Second, // Wait 10s for ping response
		}),
	}
	cs.grpcServer = grpc.NewServer(opts...)
	pb.RegisterProxyServer(cs.grpcServer, internal.NewServer(capabilityRegistry, cs.lggr))

	return cs.grpcServer.Serve(lis)
}
