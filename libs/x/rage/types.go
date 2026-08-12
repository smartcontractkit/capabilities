// Package rage is the rage p2p transport DON-to-DON traffic rides on: a peer, the shared peer that
// keeps a group and a stream per remote node, and the types they exchange.
//
// It lives under libs/x because it is on its way somewhere else. crecore is meant to own DON-to-DON
// communication outright, at which point this moves into crecore and stops being importable at all;
// it is here only because core still uses it and both need to build against the same code during
// the move. Nothing else should import it - if you need to talk to another DON, ask crecore.
//
// Moved from chainlink's core/services/p2p, with the interfaces and their rage implementation
// collapsed into one package and the two dependencies on core replaced: the keyring adapter is now
// local (see keyring.go) and the shared peer takes a PeerSource instead of core's
// SingletonPeerWrapper.
package rage

import (
	"context"

	"github.com/smartcontractkit/libocr/ragep2p"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

const PeerIDLength = 32

type PeerID = ragetypes.PeerID

type Peer interface {
	services.Service
	ID() PeerID
	UpdateConnections(peers map[PeerID]StreamConfig) error
	Send(peerID PeerID, msg []byte) error
	Receive() <-chan Message
	IsBootstrap() bool
}

type DonPair [2]capabilities.DON
type SharedPeer interface {
	Peer
	UpdateConnectionsByDONs(ctx context.Context, donPairs []DonPair, streamConfig StreamConfig) error
}

type PeerWrapper interface {
	services.Service
	GetPeer() Peer
}

type Signer interface {
	Initialize() error
	Sign(data []byte) ([]byte, error)
}

type Message struct {
	Sender  PeerID
	Payload []byte
}

type StreamConfig struct {
	IncomingMessageBufferSize int
	OutgoingMessageBufferSize int
	MaxMessageLenBytes        int
	MessageRateLimiter        ragep2p.TokenBucketParams
	BytesRateLimiter          ragep2p.TokenBucketParams
}
