package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	capregv2 "github.com/smartcontractkit/chainlink-evm/gethwrappers/workflow/generated/capabilities_registry_wrapper_v2"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// DefaultSyncInterval matches chainlink's registrysyncer tick so a node fronted
// by crecore sees registry changes on the same cadence it did before.
const DefaultSyncInterval = 12 * time.Second

// pageLimit bounds each view call. The v2 contract's getters are paginated;
// this reads one large page rather than implementing pagination, matching
// chainlink's registrysyncer/v2. If a registry ever exceeds it, the sync logs a
// warning rather than silently truncating.
const pageLimit = 1024

// contractCapabilityType is the on-chain capability type enum, in the
// CapabilitiesRegistry contract's ordering.
type contractCapabilityType uint8

const (
	contractCapabilityTypeTrigger contractCapabilityType = iota
	contractCapabilityTypeAction
	contractCapabilityTypeConsensus
	contractCapabilityTypeTarget
)

// capabilityMetadata is the JSON blob the v2 contract stores per capability.
type capabilityMetadata struct {
	CapabilityType uint8 `json:"capabilityType"`
	ResponseType   uint8 `json:"responseType"`
}

// registryCaller is the subset of the generated v2 wrapper this syncer uses.
// Narrowing it keeps the syncer testable without a chain.
type registryCaller interface {
	GetCapabilities(opts *bind.CallOpts, start, limit *big.Int) ([]capregv2.CapabilitiesRegistryCapabilityInfo, error)
	GetDONs(opts *bind.CallOpts, start, limit *big.Int) ([]capregv2.CapabilitiesRegistryDONInfo, error)
	GetNodes(opts *bind.CallOpts, start, limit *big.Int) ([]capregv2.INodeInfoProviderNodeInfo, error)
}

// Syncer polls the on-chain CapabilitiesRegistry and publishes immutable
// LocalRegistry snapshots.
//
// It polls rather than following events, for the same reason chainlink's syncer
// does: a whole-world snapshot cannot desync from a reorg or a dropped log
// subscription, so there is no missed-event failure mode to reason about. The
// cost is a fixed propagation delay bounded by the tick interval.
type Syncer struct {
	services.StateMachine

	lggr      logger.Logger
	caller    registryCaller
	getPeerID func() (ragetypes.PeerID, error)
	interval  time.Duration

	// current holds the most recent successful snapshot, or nil before the
	// first successful sync.
	current atomic.Pointer[LocalRegistry]

	stopCh services.StopChan
	done   chan struct{}
}

// NewSyncer builds a Syncer against an already-dialed EVM client.
//
// backend is typically chainlink-evm's multinode-backed client.Client, which
// satisfies bind.ContractCaller; the reliability behaviour (node health, dead
// node declaration, primary selection) comes from there rather than being
// reimplemented here.
func NewSyncer(
	lggr logger.Logger,
	backend bind.ContractCaller,
	registryAddress common.Address,
	getPeerID func() (ragetypes.PeerID, error),
	interval time.Duration,
) (*Syncer, error) {
	caller, err := capregv2.NewCapabilitiesRegistryCaller(registryAddress, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to bind CapabilitiesRegistry at %s: %w", registryAddress, err)
	}
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	return &Syncer{
		lggr:      logger.Named(lggr, "RegistrySyncer"),
		caller:    caller,
		getPeerID: getPeerID,
		interval:  interval,
		stopCh:    make(services.StopChan),
		done:      make(chan struct{}),
	}, nil
}

func (s *Syncer) Name() string { return s.lggr.Name() }

func (s *Syncer) Start(context.Context) error {
	return s.StartOnce("RegistrySyncer", func() error {
		go s.syncLoop()
		return nil
	})
}

func (s *Syncer) Close() error {
	return s.StopOnce("RegistrySyncer", func() error {
		close(s.stopCh)
		<-s.done
		return nil
	})
}

func (s *Syncer) HealthReport() map[string]error {
	err := s.Healthy()
	if s.current.Load() == nil {
		err = errors.New("no successful registry sync yet")
	}
	return map[string]error{s.Name(): err}
}

// Current returns the latest snapshot, or an error if none has landed yet.
func (s *Syncer) Current() (*LocalRegistry, error) {
	lr := s.current.Load()
	if lr == nil {
		return nil, errors.New("registry not synced yet")
	}
	return lr, nil
}

func (s *Syncer) syncLoop() {
	defer close(s.done)

	ctx, cancel := s.stopCh.NewCtx()
	defer cancel()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Sync once up front: a ticker first fires at T+interval, and every reader
	// of this process blocks until the first snapshot exists.
	if err := s.Sync(ctx); err != nil {
		s.lggr.Errorw("initial registry sync failed", "err", err)
	}

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := s.Sync(ctx); err != nil {
				s.lggr.Errorw("registry sync failed", "err", err)
			}
		}
	}
}

// Sync performs one full read and, on success, publishes a new snapshot.
func (s *Syncer) Sync(ctx context.Context) error {
	lr, err := s.importOnchainRegistry(ctx)
	if err != nil {
		return err
	}
	s.current.Store(lr)
	s.lggr.Debugw("registry synced",
		"capabilities", len(lr.IDsToCapabilities),
		"dons", len(lr.IDsToDONs),
		"nodes", len(lr.IDsToNodes))
	return nil
}

func (s *Syncer) importOnchainRegistry(ctx context.Context) (*LocalRegistry, error) {
	opts := &bind.CallOpts{Context: ctx}
	start, limit := big.NewInt(0), big.NewInt(pageLimit)

	caps, err := s.caller.GetCapabilities(opts, start, limit)
	if err != nil {
		return nil, fmt.Errorf("getCapabilities: %w", err)
	}
	s.warnIfTruncated("capabilities", len(caps))

	idsToCapabilities := make(map[string]Capability, len(caps))
	for _, c := range caps {
		if c.IsDeprecated {
			continue
		}
		capType, err := parseCapabilityType(c.Metadata)
		if err != nil {
			s.lggr.Warnw("failed to parse capability metadata, skipping",
				"capabilityID", c.CapabilityId, "err", err)
			continue
		}
		idsToCapabilities[c.CapabilityId] = Capability{ID: c.CapabilityId, CapabilityType: capType}
	}

	dons, err := s.caller.GetDONs(opts, start, limit)
	if err != nil {
		return nil, fmt.Errorf("getDONs: %w", err)
	}
	s.warnIfTruncated("dons", len(dons))

	idsToDONs := make(map[DonID]DON, len(dons))
	for _, d := range dons {
		cfgs := make(map[string][]byte, len(d.CapabilityConfigurations))
		for _, dc := range d.CapabilityConfigurations {
			cfgs[dc.CapabilityId] = dc.Config
		}
		idsToDONs[DonID(d.Id)] = DON{
			DON:                      toDON(d),
			CapabilityConfigurations: cfgs,
		}
	}

	nodes, err := s.caller.GetNodes(opts, start, limit)
	if err != nil {
		return nil, fmt.Errorf("getNodes: %w", err)
	}
	s.warnIfTruncated("nodes", len(nodes))

	idsToNodes := make(map[ragetypes.PeerID]NodeInfo, len(nodes))
	for _, n := range nodes {
		idsToNodes[n.P2pId] = NodeInfo{
			NodeOperatorID:      n.NodeOperatorId,
			ConfigCount:         n.ConfigCount,
			WorkflowDONID:       n.WorkflowDONId,
			Signer:              n.Signer,
			P2pID:               n.P2pId,
			EncryptionPublicKey: n.EncryptionPublicKey,
			CsaKey:              n.CsaKey,
			CapabilityIDs:       n.CapabilityIds,
		}
	}

	return NewLocalRegistry(s.lggr, s.getPeerID, idsToDONs, idsToNodes, idsToCapabilities), nil
}

// warnIfTruncated makes a hit against pageLimit visible instead of letting a
// full page read as "that is all there is".
func (s *Syncer) warnIfTruncated(what string, n int) {
	if n >= pageLimit {
		s.lggr.Warnw("registry read hit the page limit; results may be truncated",
			"what", what, "count", n, "limit", pageLimit)
	}
}

func toDON(d capregv2.CapabilitiesRegistryDONInfo) capabilities.DON {
	members := make([]ragetypes.PeerID, 0, len(d.NodeP2PIds))
	for _, p := range d.NodeP2PIds {
		members = append(members, p)
	}
	return capabilities.DON{
		Name:             d.Name,
		ID:               d.Id,
		Families:         d.DonFamilies,
		ConfigVersion:    d.ConfigCount,
		Members:          members,
		F:                d.F,
		IsPublic:         d.IsPublic,
		AcceptsWorkflows: d.AcceptsWorkflows,
		Config:           d.Config,
	}
}

func parseCapabilityType(metadata []byte) (capabilities.CapabilityType, error) {
	if len(metadata) == 0 {
		return capabilities.CapabilityTypeUnknown, errors.New("metadata is empty")
	}
	var meta capabilityMetadata
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return capabilities.CapabilityTypeUnknown, fmt.Errorf("invalid metadata: %w", err)
	}
	switch contractCapabilityType(meta.CapabilityType) {
	case contractCapabilityTypeTrigger:
		return capabilities.CapabilityTypeTrigger, nil
	case contractCapabilityTypeAction:
		return capabilities.CapabilityTypeAction, nil
	case contractCapabilityTypeConsensus:
		return capabilities.CapabilityTypeConsensus, nil
	case contractCapabilityTypeTarget:
		return capabilities.CapabilityTypeTarget, nil
	default:
		return capabilities.CapabilityTypeUnknown, fmt.Errorf("unknown on-chain capability type %d", meta.CapabilityType)
	}
}
