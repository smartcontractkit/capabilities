// Package registrysyncer is an in-memory snapshot of a CapabilitiesRegistry, plus the lookups a
// metadata API needs: who the DONs are, which nodes belong to them, which capabilities they host,
// and which node this process is.
//
// Where the registry actually lives is not this package's business - a Reader supplies whole
// snapshots from wherever it is written, and the on-chain one lives in chainlink-evm. Keeping a
// snapshot current on a timer is the caller's job too; this package only holds one, answers
// questions about it, and (see orm.go) can persist one so a restart has something to answer from
// before its first read lands.
//
// Was core/services/registrysyncer's LocalRegistry. The exported shape is kept as core had it so
// core can alias this package rather than edit its call sites; what changed is that the 130-line
// hand-rolled capability-config decoder is gone, replaced by a call to chainlink-common's
// capabilitiespb.CapabilityConfigFromProto, which is the same decoder the wire path already uses.
package registrysyncer

import (
	"context"
	"errors"
	"fmt"
	"sync"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	commonregistry "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// A snapshot answers exactly the metadata half of a CapabilitiesRegistry, which is what lets core
// hand one straight to anything expecting that interface.
var _ core.CapabilitiesRegistryMetadata = (*LocalRegistry)(nil)

// The registry's own vocabulary comes from the contract a Reader fills in, aliased so this package
// reads as though it owned them. Nothing is redefined here: a snapshot holds exactly what a Reader
// returned, so a second set of types could only be the same types with a conversion between them.
type (
	DonID                   = commonregistry.DonID
	DON                     = commonregistry.DON
	CapabilityConfiguration = commonregistry.CapabilityConfiguration
	NodeInfo                = commonregistry.NodeInfo
	Capability              = commonregistry.Capability
	Snapshot                = commonregistry.Snapshot
	Contract                = commonregistry.Contract
	Reader                  = commonregistry.Reader
)

// LocalRegistry is an immutable snapshot of the on-chain registry, plus the lookups the metadata
// API needs.
//
// A LocalRegistry is never mutated after construction; a caller that keeps polling swaps whole
// snapshots.
//
// Logger and GetPeerID are exported and set after construction on purpose: a snapshot restored from
// the database (see orm.go) cannot carry either across JSON, so whoever restores one puts them
// back.
type LocalRegistry struct {
	Logger            logger.Logger
	GetPeerID         func() (ragetypes.PeerID, error)
	IDsToDONs         map[DonID]DON
	IDsToNodes        map[ragetypes.PeerID]NodeInfo
	IDsToCapabilities map[string]Capability

	// Contract is the registry these records were read from. Zero when whatever
	// supplied them reads no contract, which is why anything needing it says so
	// rather than assuming.
	Contract Contract

	cacheMu             sync.RWMutex
	cachedLocalNodePeer ragetypes.PeerID
	cachedLocalNode     capabilities.Node
}

func NewLocalRegistry(
	lggr logger.Logger,
	getPeerID func() (ragetypes.PeerID, error),
	idsToDONs map[DonID]DON,
	idsToNodes map[ragetypes.PeerID]NodeInfo,
	idsToCapabilities map[string]Capability,
) *LocalRegistry {
	return &LocalRegistry{
		Logger:            logger.Named(lggr, "LocalRegistry"),
		GetPeerID:         getPeerID,
		IDsToDONs:         idsToDONs,
		IDsToNodes:        idsToNodes,
		IDsToCapabilities: idsToCapabilities,
	}
}

// FromSnapshot builds a LocalRegistry from what a Reader returned.
//
// There is nothing to convert: a snapshot holds the contract's own types, so this only supplies the
// two things a Reader has no way to know - who is asking, and where to log.
func FromSnapshot(
	lggr logger.Logger,
	getPeerID func() (ragetypes.PeerID, error),
	snap *Snapshot,
) *LocalRegistry {
	lr := NewLocalRegistry(lggr, getPeerID, snap.DONs, snap.Nodes, snap.Capabilities)
	// Set here rather than taken by NewLocalRegistry, whose signature is shared
	// with core.
	lr.Contract = snap.Contract
	return lr
}

// LocalNode resolves this process's own node record. The result is cached: the
// peer ID cannot change at runtime, so a changed peer ID is logged as a bug
// rather than silently accepted.
func (l *LocalRegistry) LocalNode(ctx context.Context) (capabilities.Node, error) {
	pid, err := l.GetPeerID()
	if err != nil {
		return capabilities.Node{}, fmt.Errorf("unable to get local node peer ID: %w", err)
	}

	l.cacheMu.RLock()
	if l.cachedLocalNodePeer != (ragetypes.PeerID{}) && l.cachedLocalNodePeer == pid {
		node := l.cachedLocalNode
		l.cacheMu.RUnlock()
		return node, nil
	}
	l.cacheMu.RUnlock()

	l.cacheMu.Lock()
	defer l.cacheMu.Unlock()
	if l.cachedLocalNodePeer != (ragetypes.PeerID{}) && l.cachedLocalNodePeer == pid {
		return l.cachedLocalNode, nil
	}

	if l.cachedLocalNodePeer != (ragetypes.PeerID{}) {
		l.Logger.Errorw("node's peerID changed at runtime, this should never happen",
			"cachedLocalNodePeer", l.cachedLocalNodePeer, "currentPeerID", pid)
	}

	n, err := l.NodeByPeerID(ctx, pid)
	if err != nil {
		return n, err
	}
	l.cachedLocalNode = n
	l.cachedLocalNodePeer = pid
	return n, nil
}

func (l *LocalRegistry) NodeByPeerID(_ context.Context, peerID ragetypes.PeerID) (capabilities.Node, error) {
	if err := l.ensureNotEmpty(); err != nil {
		return capabilities.Node{}, err
	}
	nodeInfo, ok := l.IDsToNodes[peerID]
	if !ok {
		return capabilities.Node{}, errors.New("could not find peerID " + peerID.String())
	}

	var workflowDON capabilities.DON
	var capabilityDONs []capabilities.DON
	for _, d := range l.IDsToDONs {
		for _, p := range d.Members {
			if p != peerID {
				continue
			}
			if d.AcceptsWorkflows {
				// The CapabilitiesRegistry enforces DON ID > 0, so a zero ID here
				// means workflowDON has not been assigned yet.
				if workflowDON.ID == 0 {
					workflowDON = d.DON
				} else {
					l.Logger.Errorw("Configuration error: node belongs to more than one workflowDON", "peerID", peerID)
				}
			}
			capabilityDONs = append(capabilityDONs, d.DON)
		}
	}

	return capabilities.Node{
		PeerID:              &peerID,
		NodeOperatorID:      nodeInfo.NodeOperatorID,
		Signer:              nodeInfo.Signer,
		EncryptionPublicKey: nodeInfo.EncryptionPublicKey,
		WorkflowDON:         workflowDON,
		CapabilityDONs:      capabilityDONs,
	}, nil
}

func (l *LocalRegistry) DONsForCapability(ctx context.Context, capabilityID string) ([]capabilities.DONWithNodes, error) {
	if err := l.ensureNotEmpty(); err != nil {
		return nil, err
	}

	found := []capabilities.DONWithNodes{}
	for _, don := range l.IDsToDONs {
		if _, ok := don.CapabilityConfigurations[capabilityID]; !ok {
			continue
		}
		nodes, err := l.nodesForDON(ctx, don.DON)
		if err != nil {
			return nil, fmt.Errorf("could not fetch nodes for DON %d: %w", don.ID, err)
		}
		found = append(found, capabilities.DONWithNodes{DON: don.DON, Nodes: nodes})
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("could not find DON for capability %s", capabilityID)
	}
	return found, nil
}

func (l *LocalRegistry) nodesForDON(ctx context.Context, don capabilities.DON) ([]capabilities.Node, error) {
	nodes := make([]capabilities.Node, 0, len(don.Members))
	for _, n := range don.Members {
		node, err := l.NodeByPeerID(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("could not find node for peerID %s: %w", n.String(), err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// DONByID resolves any DON known to the registry, including caller/workflow DONs
// that host no capability. Callers need this to read a caller DON's Families
// (e.g. zone membership) from its WorkflowDonID.
func (l *LocalRegistry) DONByID(_ context.Context, donID uint32) (capabilities.DON, error) {
	if err := l.ensureNotEmpty(); err != nil {
		return capabilities.DON{}, err
	}
	d, ok := l.IDsToDONs[DonID(donID)]
	if !ok {
		return capabilities.DON{}, fmt.Errorf("could not find don %d", donID)
	}
	return d.DON, nil
}

// ConfigForCapability returns the decoded configuration for a capability on a DON.
func (l *LocalRegistry) ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	cfg, err := l.capabilityConfig(capabilityID, donID)
	if err != nil {
		return capabilities.CapabilityConfiguration{}, err
	}
	return cfg.Unmarshal()
}

// RawConfigForCapability returns the same configuration undecoded, for a caller that only passes it
// on - the proxy serves these bytes verbatim rather than decoding and re-encoding them.
func (l *LocalRegistry) RawConfigForCapability(_ context.Context, capabilityID string, donID uint32) ([]byte, error) {
	cfg, err := l.capabilityConfig(capabilityID, donID)
	if err != nil {
		return nil, err
	}
	return cfg.Config, nil
}

func (l *LocalRegistry) capabilityConfig(capabilityID string, donID uint32) (CapabilityConfiguration, error) {
	if err := l.ensureNotEmpty(); err != nil {
		return CapabilityConfiguration{}, err
	}
	d, ok := l.IDsToDONs[DonID(donID)]
	if !ok {
		return CapabilityConfiguration{}, fmt.Errorf("could not find don %d", donID)
	}
	cfg, ok := d.CapabilityConfigurations[capabilityID]
	if !ok {
		return CapabilityConfiguration{}, fmt.Errorf("could not find capability configuration for capability %s and donID %d", capabilityID, donID)
	}
	return cfg, nil
}

func (l *LocalRegistry) ensureNotEmpty() error {
	if len(l.IDsToDONs) == 0 {
		return errors.New("empty local registry: no DONs registered")
	}
	if len(l.IDsToNodes) == 0 {
		return errors.New("empty local registry: no nodes registered")
	}
	if len(l.IDsToCapabilities) == 0 {
		return errors.New("empty local registry: no capabilities registered")
	}
	return nil
}
