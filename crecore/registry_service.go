package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/crecore/registry"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// registryService keeps the registry snapshot fresh and serves the
// CapabilitiesRegistry gRPC service.
//
// Where the registry is read from is not its business: it is handed a reader
// (the on-chain one lives in chainlink-evm) and wraps it in the syncer that owns
// the polling, the snapshot and the health of both.
//
// There is no enable switch: this binary running is what enables the registry,
// and core does not start without it.
//
// It attaches to the proxy's gRPC server rather than opening a listener of its
// own: a node that delegates rage networking to this process already has a
// connection to it, and a second address would be one more thing to configure and
// keep in sync for no gain.
type registryService struct {
	services.Service
	eng *services.Engine

	// syncInterval is how often the registry is re-read; see main.go.
	syncInterval time.Duration

	lggr logger.Logger
	// peerID is this node's own, from the same identity the rage networking uses, so the node record
	// this process resolves is the node it fronts.
	peerID ragetypes.PeerID

	syncer   *registry.Syncer
	registry *registry.Registry
}

var _ services.Service = (*registryService)(nil)

func newRegistryService(
	syncInterval time.Duration,
	lggr logger.Logger,
	reader registry.Reader,
	orm registry.ORM,
	peerID ragetypes.PeerID,
) *registryService {
	syncer := registry.NewSyncer(lggr, reader, orm,
		peerID, syncInterval)

	s := &registryService{
		syncInterval: syncInterval,
		lggr:         lggr,
		peerID:       peerID,
		syncer:       syncer,
		// Both halves exist from construction so the service can be attached to the proxy's gRPC
		// server before either starts, and so the registry has somewhere to read metadata from
		// without being told about it a second time later.
		//
		// Capabilities registered here are served on loopback by the same-host LOOP
		// process registering them, so insecure credentials are stated explicitly
		// rather than defaulted in the client (mirrors creregistry.Select).
		registry: registry.New(lggr, syncer.Current,
			grpc.WithTransportCredentials(insecure.NewCredentials())),
	}
	s.Service, s.eng = services.Config{
		Name:  "CapabilitiesRegistry",
		Start: s.start,
		Close: s.close,
	}.NewServiceEngine(lggr)
	return s
}

func (s *registryService) start(ctx context.Context) error {
	if err := s.syncer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start registry syncer: %w", err)
	}

	s.lggr.Infow("CapabilitiesRegistry started",
		"syncInterval", s.syncInterval, "peerID", s.peerID.String())
	return nil
}

func (s *registryService) close() error {
	return s.syncer.Close()
}

// Register attaches the CapabilitiesRegistry service to a gRPC server.
//
// Safe to call before start: everything it serves exists from construction, and its metadata RPCs
// return a "not ready" error until the first snapshot lands. That decouples this service's startup
// from the proxy's, which must register everything before it Serves.
func (s *registryService) Register(srv *grpc.Server) {
	registry.Register(srv, s.registry)
}

// CapabilitiesRegistry returns the core.CapabilitiesRegistry don2don.NewDispatcher takes. Registry
// implements it directly - the same registry a LOOP-registered and a dispatcher-reached capability
// go through identically, one gRPC Add, one real entry, callable either way.
func (s *registryService) CapabilitiesRegistry() core.CapabilitiesRegistry {
	return s.registry
}
