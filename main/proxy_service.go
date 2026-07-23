package main

import (
	"context"
	"errors"
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// Config is the proxy gRPC server configuration, populated from CLI flags (see
// main.go). The libocr peer / proxy-client configuration lives on the ocr
// bootstrap dependency instead.
type Config struct {
	// ProxyListenAddress is the address the proxy gRPC server listens on.
	ProxyListenAddress string
}

// proxyService exposes the libocr rage networking factories over gRPC so that
// core can delegate its OCR networking (and, in future, DON-to-DON networking)
// to this process. The factories come from the ocr bootstrap dependency, which
// either creates a local peer or is backed by another proxy.
//
// NOTE: per the standalone framework, Start blocks until shutdown (the
// Bootstrapper returns the result of Start directly).
type proxyService struct {
	services.Service
	eng *services.Engine

	cfg       *Config
	lggr      logger.Logger
	factories standalone.Dependency[*ocr.Factories]

	grpcServer     *grpc.Server
	factoriesClose func() error
}

var _ services.Service = (*proxyService)(nil)

// newProxyService builds the proxy service using the standard
// services.Config/Engine pattern, so its lifecycle and health integrate with
// the bootstrapper's aggregated health report.
func newProxyService(cfg *Config, lggr logger.Logger, factories standalone.Dependency[*ocr.Factories]) *proxyService {
	s := &proxyService{cfg: cfg, lggr: lggr, factories: factories}
	s.Service, s.eng = services.Config{
		Name:  "P2PProxy",
		Start: s.start,
		Close: s.close,
	}.NewServiceEngine(lggr)
	return s
}

func (s *proxyService) start(ctx context.Context) error {
	if s.cfg.ProxyListenAddress == "" {
		return errors.New("--proxy-listen-address is required")
	}

	factories, err := s.factories.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get libocr factories: %w", err)
	}
	s.factoriesClose = factories.Close

	lis, err := net.Listen("tcp", s.cfg.ProxyListenAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.cfg.ProxyListenAddress, err)
	}

	// The factories back both surfaces over the same rage connection and
	// discoverer: OCR endpoints and DON-to-DON peer groups.
	s.grpcServer = grpc.NewServer()
	creproxy.RegisterBinaryNetworkEndpointProxyServer(s.grpcServer, NewServer(factories.OCR2Endpoint))
	creproxy.RegisterEndpoint2ProxyServer(s.grpcServer, NewEndpoint2Server(factories.OCR3_1Endpoint))
	creproxy.RegisterPeerGroupProxyServer(s.grpcServer, NewPeerGroupServer(factories.PeerGroup))

	// Gracefully stop the gRPC server when the engine cancels this context on
	// Close; run the (blocking) Serve in a tracked goroutine so start returns
	// promptly, per the services.Engine contract.
	s.eng.Go(func(ctx context.Context) {
		<-ctx.Done()
		s.grpcServer.GracefulStop()
	})
	s.eng.Go(func(context.Context) {
		s.lggr.Infow("p2p proxy serving", "address", lis.Addr().String())
		if err := s.grpcServer.Serve(lis); err != nil {
			s.eng.Errorw("proxy gRPC server stopped", "err", err)
		}
	})
	return nil
}

// close tears down the libocr factories (local peer or proxy clients). The gRPC
// server is gracefully stopped by the goroutine started in start once the
// engine cancels its context.
func (s *proxyService) close() error {
	if s.factoriesClose != nil {
		return s.factoriesClose()
	}
	return nil
}
