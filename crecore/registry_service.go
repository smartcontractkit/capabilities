package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/crecore/registry"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	regserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/server"
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
		//
		// Capabilities registered here are served on loopback by the same-host LOOP
		// process registering them, so insecure credentials are stated explicitly
		// rather than defaulted in the client (mirrors creregistry.Select).
		registry: regserver.New(lggr, grpc.WithTransportCredentials(insecure.NewCredentials())),
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

// CapabilitiesRegistry returns the core.CapabilitiesRegistry don2don.NewDispatcher takes: real
// values (regserver.Registry.Local(), which Add resolves Handles into) plus this service's
// metadata. Both halves are backed by the same registry a LOOP-registered and a dispatcher-reached
// capability go through identically - one gRPC Add, one real entry, callable either way.
func (s *registryService) CapabilitiesRegistry() core.CapabilitiesRegistry {
	return registryAdapter{metadata: s.registry, base: s.registry.Local()}
}

type registryAdapter struct {
	metadata *regserver.Registry
	base     core.CapabilitiesRegistryBase
}

func (a registryAdapter) Add(ctx context.Context, c capabilities.BaseCapability) error {
	return a.base.Add(ctx, c)
}

func (a registryAdapter) Remove(ctx context.Context, id string) error {
	return a.base.Remove(ctx, id)
}

func (a registryAdapter) Get(ctx context.Context, id string) (capabilities.BaseCapability, error) {
	return a.base.Get(ctx, id)
}

func (a registryAdapter) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	return a.base.GetTrigger(ctx, id)
}

func (a registryAdapter) GetExecutable(ctx context.Context, id string) (capabilities.ExecutableCapability, error) {
	return a.base.GetExecutable(ctx, id)
}

func (a registryAdapter) List(ctx context.Context) ([]capabilities.BaseCapability, error) {
	return a.base.List(ctx)
}

func (a registryAdapter) LocalNode(ctx context.Context) (capabilities.Node, error) {
	return a.metadata.LocalNode(ctx)
}

func (a registryAdapter) NodeByPeerID(ctx context.Context, peerID ragetypes.PeerID) (capabilities.Node, error) {
	return a.metadata.NodeByPeerID(ctx, peerID)
}

func (a registryAdapter) DONsForCapability(ctx context.Context, capabilityID string) ([]capabilities.DONWithNodes, error) {
	return a.metadata.DONsForCapability(ctx, capabilityID)
}

func (a registryAdapter) DONByID(ctx context.Context, donID uint32) (capabilities.DON, error) {
	return a.metadata.DONByID(ctx, donID)
}

// ConfigForCapability decodes the same wire-encoded config the gRPC service's own
// ConfigForCapability RPC parses, since regserver.Registry only stores the raw bytes.
func (a registryAdapter) ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	raw, err := a.metadata.RawConfigForCapability(ctx, capabilityID, donID)
	if err != nil {
		return capabilities.CapabilityConfiguration{}, err
	}
	cfg := &capabilitiespb.CapabilityConfig{}
	if err := proto.Unmarshal(raw, cfg); err != nil {
		return capabilities.CapabilityConfiguration{}, fmt.Errorf(
			"capability %s on DON %d has an unparseable on-chain config: %w", capabilityID, donID, err)
	}
	return capabilitiespb.CapabilityConfigFromProto(cfg)
}
