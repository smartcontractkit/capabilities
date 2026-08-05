// Package registry implements the CapabilitiesRegistry that crecore serves over
// plain gRPC (see libs/creregistry/pb).
//
// The on-chain view is read with an EVM client and generated gethwrappers
// directly. There is no relayer, no ContractReader, no codec and no
// ReadIdentifier indirection: three view calls, decoded by the wrapper.
package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// DonID is a DON's registry ID.
type DonID uint32

// DON pairs a DON's identity with the raw capability configuration blobs the
// contract stores for it.
type DON struct {
	capabilities.DON
	// CapabilityConfigurations maps capability ID to the wire-encoded
	// capabilities/pb.CapabilityConfig the contract holds for this DON.
	//
	// The bytes are deliberately left undecoded. crecore has no reason to
	// interpret them: it serves them verbatim over ConfigForCapability and the
	// caller unmarshals. That keeps ~150 lines of config-decoding logic (and its
	// drift risk) out of this process entirely.
	CapabilityConfigurations map[string][]byte
}

// NodeInfo is a node's on-chain record.
type NodeInfo struct {
	NodeOperatorID      uint32
	ConfigCount         uint32
	WorkflowDONID       uint32
	Signer              [32]byte
	P2pID               [32]byte
	EncryptionPublicKey [32]byte
	CsaKey              [32]byte
	CapabilityIDs       []string
}

// Capability is a capability's on-chain record.
type Capability struct {
	ID             string
	CapabilityType capabilities.CapabilityType
}

// LocalRegistry is an immutable snapshot of the on-chain registry, plus the
// lookups the metadata API needs. It is ported from chainlink's
// core/services/registrysyncer.LocalRegistry, minus the ORM/DB persistence and
// with capability config left as raw bytes.
//
// A LocalRegistry is never mutated after construction; Syncer swaps whole
// snapshots.
type LocalRegistry struct {
	Logger            logger.Logger
	GetPeerID         func() (ragetypes.PeerID, error)
	IDsToDONs         map[DonID]DON
	IDsToNodes        map[ragetypes.PeerID]NodeInfo
	IDsToCapabilities map[string]Capability

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

// RawConfigForCapability returns the wire-encoded capabilities/pb.CapabilityConfig
// for a capability on a DON, undecoded. See DON.CapabilityConfigurations.
func (l *LocalRegistry) RawConfigForCapability(_ context.Context, capabilityID string, donID uint32) ([]byte, error) {
	if err := l.ensureNotEmpty(); err != nil {
		return nil, err
	}
	d, ok := l.IDsToDONs[DonID(donID)]
	if !ok {
		return nil, fmt.Errorf("could not find don %d", donID)
	}
	cfg, ok := d.CapabilityConfigurations[capabilityID]
	if !ok {
		return nil, fmt.Errorf("could not find capability configuration for capability %s and donID %d", capabilityID, donID)
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
