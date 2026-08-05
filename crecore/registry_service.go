package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/crecore/registry"
	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"

	regserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/server"

	evmclient "github.com/smartcontractkit/chainlink-evm/pkg/client"
)

// registryService owns the on-chain registry poller and serves the
// CapabilitiesRegistry gRPC service.
//
// There is no enable switch: this binary running is what enables the registry,
// and core does not start without it. So a missing or malformed contract address
// is a startup failure rather than a reason to run degraded.
//
// It attaches to the proxy's gRPC server rather than opening a listener of its
// own: a node that delegates rage networking to this process already has a
// connection to it, and a second address would be one more thing to configure and
// keep in sync for no gain.
type registryService struct {
	services.Service
	eng *services.Engine

	// contractAddress is the on-chain CapabilitiesRegistry (v2) address, and
	// syncInterval how often it is re-read. Both come from CLI flags; see main.go.
	contractAddress string
	syncInterval    time.Duration

	lggr      logger.Logger
	evm       standalone.Dependency[evmclient.Client]
	factories standalone.Dependency[*ocr.Factories]

	syncer   *registry.Syncer
	registry *regserver.Registry
}

var _ services.Service = (*registryService)(nil)

func newRegistryService(
	contractAddress string,
	syncInterval time.Duration,
	lggr logger.Logger,
	evm standalone.Dependency[evmclient.Client],
	factories standalone.Dependency[*ocr.Factories],
) *registryService {
	s := &registryService{
		contractAddress: contractAddress,
		syncInterval:    syncInterval,
		lggr:            lggr,
		evm:             evm,
		factories:       factories,
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
	if !common.IsHexAddress(s.contractAddress) {
		return fmt.Errorf("--capabilities-registry-address is required and must be a hex address, got %q", s.contractAddress)
	}

	cl, err := s.evm.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get evm client: %w", err)
	}

	// The peer ID comes from the same keystore-backed identity the rage peer uses,
	// so the node record this process resolves is the node it fronts.
	factories, err := s.factories.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get libocr factories: %w", err)
	}
	peerID := factories.PeerID
	getPeerID := func() (ragetypes.PeerID, error) { return peerID, nil }

	syncer, err := registry.NewSyncer(
		s.lggr,
		cl,
		common.HexToAddress(s.contractAddress),
		getPeerID,
		s.syncInterval,
	)
	if err != nil {
		return fmt.Errorf("failed to create registry syncer: %w", err)
	}
	s.syncer = syncer
	s.registry.SetMetadata(syncer.Metadata())

	if err := syncer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start registry syncer: %w", err)
	}

	s.lggr.Infow("CapabilitiesRegistry started",
		"contract", s.contractAddress, "syncInterval", s.syncInterval, "peerID", peerID.String())
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
