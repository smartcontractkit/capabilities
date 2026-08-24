package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/smartcontractkit/capabilities/libs/standalone/rage"

	"github.com/smartcontractkit/capabilities/crecore/nodekeys"
	"github.com/smartcontractkit/capabilities/libs/x/registry"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// Config is the root command's configuration, populated by flags.RegisterCommandFlags (see
// main.go). The libocr peer configuration lives on the ocr bootstrap dependency; the address this
// process serves on is the bootstrapper's shared gRPC server (--grpc.port).
type Config struct {
	// CapabilitiesRegistrySyncInterval is how often the on-chain registry is re-read.
	CapabilitiesRegistrySyncInterval config.Duration `usage:"how often the on-chain registry is re-read"`

	// Dispatcher configures the DON-to-DON dispatcher this process runs over its own rage peer.
	Dispatcher DispatcherConfig
}

var defaultConfig = Config{
	CapabilitiesRegistrySyncInterval: *config.MustNewDuration(registry.DefaultSyncInterval),
	Dispatcher:                       defaultDispatcherConfig,
}

// proxyService exposes the libocr rage networking factories over gRPC so that
// core can delegate its OCR networking to this process. The factories come from the ocr bootstrap
// dependency, which hosts a local peer, is backed by another proxy, or is in-process for an
// embedded instance - this service cannot tell, and neither can core.
type proxyService struct {
	services.Service
	eng *services.Engine

	lggr logger.Logger
	// grpcServer is the bootstrapper's shared gRPC server for this instance: this service only
	// registers its RPCs on it, and the bootstrapper serves it once every other service (e.g. the
	// CapabilitiesRegistry) has registered too, so they share one address instead of each opening a
	// listener of their own.
	grpcServer grpc.ServiceRegistrar
	factories  *rage.Factories
	// keys are what this process signs with on behalf of oracles and capabilities that hold none.
	keys nodekeys.Keys
}

var _ services.Service = (*proxyService)(nil)

// newProxyService builds the proxy service using the standard
// services.Config/Engine pattern, so its lifecycle and health integrate with
// the bootstrapper's aggregated health report.
func newProxyService(lggr logger.Logger, grpcServer grpc.ServiceRegistrar, factories *rage.Factories, keys nodekeys.Keys) *proxyService {
	s := &proxyService{lggr: lggr, grpcServer: grpcServer, factories: factories, keys: keys}
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

	// The factories back both surfaces over the same rage connection and discoverer.
	creproxy.RegisterBinaryNetworkEndpointProxyServer(s.grpcServer, NewServer(s.factories.OCR2Endpoint, metrics))
	creproxy.RegisterEndpoint2ProxyServer(s.grpcServer, NewEndpoint2Server(s.factories.OCR3_1Endpoint, metrics))

	// Signing goes on the same surface, and for the same reason: this process
	// holds the node's keys, so an oracle hosted elsewhere asks it to sign
	// rather than being given one.
	//
	// Registered without reading a key. Which keys are there is what a caller asking
	// for one finds out, so a node with no OCR key still proxies its peer and lends
	// its chain accounts.
	creproxy.RegisterSignerServer(s.grpcServer, newSignerServer(s.keys))

	// Chain keys go on the same surface for the same reason, when the node has any:
	// a chain capability transmits as this node, and this is the process that can
	// sign as it. A node holding no EVM keys serves nothing here, so a capability
	// pointed at it fails when it dials rather than when it first transmits.
	// Nil only for an embedded run, which has no node keystore behind it: its
	// instances derive what they sign with, so there is nothing to lend them.
	if chain := s.keys.Chain(); chain != nil {
		creproxy.RegisterKeystoreServer(s.grpcServer, newKeystoreServer(chain))
	} else {
		s.eng.Infow("No node keystore behind this process, so no chain signing is served")
	}

	return nil
}
