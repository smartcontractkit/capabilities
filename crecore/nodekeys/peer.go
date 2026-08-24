package nodekeys

import (
	"context"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// PeerKeyring is the peer half of keys, on its own.
//
// It exists because the peer is resolved by something that should know nothing
// about keystores: libs/standalone/rage listens, dials and announces, and is
// handed the identity to announce under. This is the adapter between the two -
// the same keys, seen through the one field a peer needs.
func PeerKeyring(keys standalone.BootstrapDependency[Keys]) standalone.BootstrapDependency[ragetypes.PeerKeyring] {
	return &peerKeyringDependency{keys: keys}
}

type peerKeyringDependency struct {
	keys standalone.BootstrapDependency[Keys]
}

var _ standalone.BootstrapDependency[ragetypes.PeerKeyring] = (*peerKeyringDependency)(nil)

// Namespace is empty and Config is nil: this adds no settings of its own, and the
// keys it narrows have already registered theirs.
func (d *peerKeyringDependency) Namespace() string { return "" }

func (d *peerKeyringDependency) Config() any { return nil }

func (d *peerKeyringDependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{d.keys}
}

func (d *peerKeyringDependency) ForEmbedding(i, instances int) standalone.BootstrapDependency[ragetypes.PeerKeyring] {
	return &peerKeyringDependency{keys: d.keys.ForEmbedding(i, instances)}
}

func (d *peerKeyringDependency) Get(ctx context.Context, cc standalone.CommonConfig) (ragetypes.PeerKeyring, error) {
	keys, err := d.keys.Get(ctx, cc)
	if err != nil {
		return nil, err
	}
	return keys.Peer(ctx)
}
