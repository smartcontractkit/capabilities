package main

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/smartcontractkit/capabilities/crecore/registry"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// Config is the root command's configuration, populated by flags.RegisterCommandFlags (see
// main.go). The libocr peer configuration lives on the ocr bootstrap dependency, and the address
// this process serves on lives on the listener dependency.
type Config struct {
	// CapabilitiesRegistryAddress is the on-chain CapabilitiesRegistry (v2) contract address. The registry
	// always runs, so this is as required in practice as the registry itself.
	CapabilitiesRegistryAddress string `usage:"on-chain CapabilitiesRegistry (v2) contract address" validate:"required" example:"'0xYourRegistryAddress'"`

	// CapabilitiesRegistrySyncInterval is how often the on-chain registry is re-read.
	CapabilitiesRegistrySyncInterval config.Duration `usage:"how often the on-chain registry is re-read"`
}

var defaultConfig = Config{
	CapabilitiesRegistrySyncInterval: *config.MustNewDuration(registry.DefaultSyncInterval),
}

// proxyService exposes the libocr rage networking factories over gRPC so that
// core can delegate its OCR networking (and, in future, DON-to-DON networking)
// to this process. The factories come from the ocr bootstrap dependency, which
// hosts a local peer, is backed by another proxy, or is in-process for an
// embedded instance - this service cannot tell, and neither can core.
type proxyService struct {
	services.Service
	eng *services.Engine

	lggr logger.Logger
	// listener is where this process serves. It arrives resolved rather than as an address to open,
	// so a process running several instances gives each of them a socket of its own without this
	// service knowing that more than one exists. Its lifetime is the bootstrapper's, as the
	// factories' is: both outlive this service's own start and close.
	listener  net.Listener
	factories *ocr.Factories

	// registrars attach additional gRPC services to this server before it
	// Serves. Used so co-located services (e.g. the CapabilitiesRegistry) share
	// one address instead of each opening a listener.
	registrars []func(*grpc.Server)

	grpcServer *grpc.Server
}

var _ services.Service = (*proxyService)(nil)

// newProxyService builds the proxy service using the standard
// services.Config/Engine pattern, so its lifecycle and health integrate with
// the bootstrapper's aggregated health report.
func newProxyService(lggr logger.Logger, listener net.Listener, factories *ocr.Factories, registrars ...func(*grpc.Server)) *proxyService {
	s := &proxyService{lggr: lggr, listener: listener, factories: factories, registrars: registrars}
	s.Service, s.eng = services.Config{
		Name:  "P2PProxy",
		Start: s.start,
	}.NewServiceEngine(lggr)
	return s
}

func (s *proxyService) start(context.Context) error {
	metrics, err := newProxyMetrics()
	if err != nil {
		return fmt.Errorf("failed to create proxy metrics: %w", err)
	}

	// The factories back both surfaces over the same rage connection and
	// discoverer: OCR endpoints and DON-to-DON peer groups.
	s.grpcServer = grpc.NewServer()
	creproxy.RegisterBinaryNetworkEndpointProxyServer(s.grpcServer, NewServer(s.factories.OCR2Endpoint, metrics))
	creproxy.RegisterEndpoint2ProxyServer(s.grpcServer, NewEndpoint2Server(s.factories.OCR3_1Endpoint, metrics))
	creproxy.RegisterPeerGroupProxyServer(s.grpcServer, NewPeerGroupServer(s.factories.PeerGroup, metrics))

	for _, register := range s.registrars {
		register(s.grpcServer)
	}

	// Gracefully stop the gRPC server when the engine cancels this context on
	// Close; run the (blocking) Serve in a tracked goroutine so start returns
	// promptly, per the services.Engine contract.
	s.eng.Go(func(ctx context.Context) {
		<-ctx.Done()
		s.grpcServer.GracefulStop()
	})
	s.eng.Go(func(context.Context) {
		s.lggr.Infow("p2p proxy serving", "address", s.listener.Addr().String())
		if err := s.grpcServer.Serve(s.listener); err != nil {
			s.eng.Errorw("proxy gRPC server stopped", "err", err)
		}
	})
	return nil
}
