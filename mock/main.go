package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/pelletier/go-toml/v2"
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

type Mocks struct {
	ID          string `toml:"id"`
	Description string `toml:"description""`
	Type        string `toml:"type"`
}
type Config struct {
	Port         int     `toml:"port"`
	DefaultMocks []Mocks `toml:"defaultMocks"`
}

const (
	serviceName = "CapabilityRegistryProxy"
)

type CapProxyGRPCService struct {
	services.StateMachine
	lggr               logger.Logger
	capabilityRegistry core.CapabilitiesRegistry
	grpcServer         *grpc.Server
	mockServer         *internal.MockServer
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
	cs.mockServer.Close()
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
	return cs.mockServer.GetAllMockInfo()
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
	if len(config) < 1 {
		return errors.New("missing config")
	}
	var mockConfig Config
	err := toml.Unmarshal([]byte(config), &mockConfig)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %s %w", config, err)
	}

	if mockConfig.Port == 0 {
		return errors.New("must specify a port number")
	}

	cs.lggr.Info("Starting mock capability server")
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", mockConfig.Port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second, // Send keepalive ping every 30s
			Timeout: 10 * time.Second, // Wait 10s for ping response
		}),
	}
	cs.mockServer = internal.NewMockServer(capabilityRegistry, cs.lggr)

	for _, m := range mockConfig.DefaultMocks {
		capType := internal.ToMockServerEnum(capabilities.CapabilityType(m.Type))
		if capType == 0 {
			return fmt.Errorf("could not start mock capabilitie %s unknown capability type %s", m.ID, m.Type)
		}
		cs.mockServer.CreateCapability(ctx, &pb.CapabilityInfo{
			ID:             m.ID,
			CapabilityType: capType,
			Description:    m.Description,
			IsLocal:        true,
		})
	}

	cs.grpcServer = grpc.NewServer(opts...)
	pb.RegisterProxyServer(cs.grpcServer, cs.mockServer)

	go func() {
		if err2 := cs.grpcServer.Serve(lis); err2 != nil {
			cs.lggr.Error("gRPC server failed to serve: ", err2)
		}
	}()
	return nil
}
