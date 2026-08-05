package registry

import (
	"context"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	regserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/server"
)

// Metadata adapts the Syncer to regserver.MetadataSource.
//
// The registry and its wire adapter live in chainlink-common; only the on-chain
// read lives here, because only it needs an EVM client and contract bindings.
// Each call resolves the current snapshot, so a sync that lands between calls is
// picked up without the registry holding a stale view.
func (s *Syncer) Metadata() regserver.MetadataSource { return metadataSource{s} }

type metadataSource struct{ s *Syncer }

var _ regserver.MetadataSource = metadataSource{}

func (m metadataSource) LocalNode(ctx context.Context) (capabilities.Node, error) {
	lr, err := m.s.Current()
	if err != nil {
		return capabilities.Node{}, err
	}
	return lr.LocalNode(ctx)
}

func (m metadataSource) NodeByPeerID(ctx context.Context, peerID ragetypes.PeerID) (capabilities.Node, error) {
	lr, err := m.s.Current()
	if err != nil {
		return capabilities.Node{}, err
	}
	return lr.NodeByPeerID(ctx, peerID)
}

func (m metadataSource) DONsForCapability(ctx context.Context, capabilityID string) ([]capabilities.DONWithNodes, error) {
	lr, err := m.s.Current()
	if err != nil {
		return nil, err
	}
	return lr.DONsForCapability(ctx, capabilityID)
}

func (m metadataSource) DONByID(ctx context.Context, donID uint32) (capabilities.DON, error) {
	lr, err := m.s.Current()
	if err != nil {
		return capabilities.DON{}, err
	}
	return lr.DONByID(ctx, donID)
}

func (m metadataSource) RawConfigForCapability(ctx context.Context, capabilityID string, donID uint32) ([]byte, error) {
	lr, err := m.s.Current()
	if err != nil {
		return nil, err
	}
	return lr.RawConfigForCapability(ctx, capabilityID, donID)
}
