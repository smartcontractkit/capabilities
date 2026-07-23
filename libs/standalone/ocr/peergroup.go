package ocr

import (
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/networking"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/ragep2p"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// newNetworkingPeerGroupFactory adapts a libocr networking.PeerGroupFactory
// (from a local peer) to the creproxy.PeerGroupFactory the proxy server serves.
// The method sets are structurally identical; this only bridges the types so
// create and proxy modes both yield a creproxy.PeerGroupFactory.
func newNetworkingPeerGroupFactory(inner networking.PeerGroupFactory) creproxy.PeerGroupFactory {
	return networkingPeerGroupFactory{inner: inner}
}

type networkingPeerGroupFactory struct {
	inner networking.PeerGroupFactory
}

func (a networkingPeerGroupFactory) NewPeerGroup(configDigest [32]byte, peerIDs []string, bootstrappers []creproxy.BootstrapperInfo) (creproxy.PeerGroup, error) {
	locators := make([]commontypes.BootstrapperLocator, len(bootstrappers))
	for i, b := range bootstrappers {
		locators[i] = commontypes.BootstrapperLocator{PeerID: b.PeerID, Addrs: b.Addrs}
	}
	pg, err := a.inner.NewPeerGroup(ocr2types.ConfigDigest(configDigest), peerIDs, locators)
	if err != nil {
		return nil, err
	}
	return networkingPeerGroup{inner: pg}, nil
}

type networkingPeerGroup struct {
	inner networking.PeerGroup
}

func (a networkingPeerGroup) NewStream(remotePeerID string, args creproxy.StreamArgs) (creproxy.PeerGroupStream, error) {
	st, err := a.inner.NewStream(remotePeerID, networking.NewStreamArgs1{
		StreamName:         args.StreamName,
		OutgoingBufferSize: args.OutgoingBufferSize,
		IncomingBufferSize: args.IncomingBufferSize,
		MaxMessageLength:   args.MaxMessageLength,
		MessagesLimit:      ragep2p.TokenBucketParams{Rate: args.MessagesLimit.Rate, Capacity: args.MessagesLimit.Capacity},
		BytesLimit:         ragep2p.TokenBucketParams{Rate: args.BytesLimit.Rate, Capacity: args.BytesLimit.Capacity},
	})
	if err != nil {
		return nil, err
	}
	// networking.Stream's method set matches creproxy.PeerGroupStream.
	return st, nil
}

func (a networkingPeerGroup) Close() error {
	return a.inner.Close()
}
