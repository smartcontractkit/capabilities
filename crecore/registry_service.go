package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/crecore/registry"

	regserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
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

	lggr   logger.Logger
	reader registry.Reader
	// peerID is this node's own, from the same identity the rage networking uses, so the node record
	// this process resolves is the node it fronts.
	peerID ragetypes.PeerID

	syncer   *registry.Syncer
	registry *regserver.Registry
}

var _ services.Service = (*registryService)(nil)

func newRegistryService(
	syncInterval time.Duration,
	lggr logger.Logger,
	reader registry.Reader,
	peerID ragetypes.PeerID,
) *registryService {
	s := &registryService{
		syncInterval: syncInterval,
		lggr:         lggr,
		reader:       reader,
		peerID:       peerID,
		// The Registry exists from construction so it can be attached to the
		// proxy's gRPC server before either service starts. Its metadata source is
		// installed later, in start.
		registry: regserver.New(lggr),
	}
	s.Service, s.eng = services.Config{
		Name:  "CapabilitiesRegistry",
		Start: s.start,
		Close: s.close,
	}.NewServiceEngine(lggr)
	return s
}

func (s *registryService) start(ctx context.Context) error {
	getPeerID := func() (ragetypes.PeerID, error) { return s.peerID, nil }

	syncer, err := registry.NewSyncer(s.lggr, s.reader, getPeerID, s.syncInterval)
	if err != nil {
		return fmt.Errorf("failed to create registry syncer: %w", err)
	}
	s.syncer = syncer
	s.registry.SetMetadata(syncer.Metadata())

	if err := syncer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start registry syncer: %w", err)
	}

	s.lggr.Infow("CapabilitiesRegistry started",
		"syncInterval", s.syncInterval, "peerID", s.peerID.String())
	return nil
}

func (s *registryService) close() error {
	if s.syncer != nil {
		return s.syncer.Close()
	}
	return nil
}

// Register attaches the CapabilitiesRegistry service to a gRPC server.
//
// Safe to call before start: the Registry is built in the constructor, and its
// metadata RPCs return a "not ready" error until start installs the syncer and
// the first sync lands. That decouples this service's startup from the proxy's,
// which must register everything before it Serves.
func (s *registryService) Register(srv *grpc.Server) {
	regserver.Register(srv, s.registry)
}
