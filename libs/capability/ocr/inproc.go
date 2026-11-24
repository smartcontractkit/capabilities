package ocr

import (
	"fmt"
	"math"
	"slices"
	"sync"

	"github.com/smartcontractkit/libocr/commontypes"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

type network struct {
	mu   sync.Mutex
	ocr2 map[endpointKey]*ocr2Endpoint
}

// embedNetwork is the process's in-process network. It is package-level for the same reason
// prometheus.DefaultRegisterer is: there is exactly one process, so there is exactly one network
// inside it, and every instance resolving its factories has to find the same one.
var embedNetwork = newNetwork()

func newNetwork() *network {
	return &network{ocr2: map[endpointKey]*ocr2Endpoint{}}
}

// endpointKey identifies one peer's endpoint for one OCR instance. The digest is part of the key
// because a peer runs one endpoint per config digest, as it does over rage networking.
type endpointKey struct {
	digest ocr2types.ConfigDigest
	peerID string
}

// senderIndex is the oracle ID the receiver knows sender by. The two ends of a link index oracles
// by their own copy of the OCR config's peer IDs, so a message carries the sender's position in
// the receiver's list, not in the sender's.
func senderIndex(receiverPeerIDs []string, senderPeerID string) (commontypes.OracleID, bool) {
	i := slices.Index(receiverPeerIDs, senderPeerID)
	// An oracle ID is a uint8, so an oracle beyond the 256th cannot be named in a message at all -
	// there is no such DON, and truncating the index would attribute the message to another oracle.
	if i < 0 || i > math.MaxUint8 {
		return 0, false
	}

	return commontypes.OracleID(i), true
}

// defaultBufferSize is used when neither the caller nor the config asks for a size.
const defaultBufferSize = 100

type ocr2Factory struct {
	net        *network
	peerID     string
	bufferSize int
}

var _ ocr2types.BinaryNetworkEndpointFactory = ocr2Factory{}

func (f ocr2Factory) PeerID() string { return f.peerID }

func (f ocr2Factory) NewEndpoint(
	digest ocr2types.ConfigDigest,
	peerIDs []string,
	_ []commontypes.BootstrapperLocator,
	_ int,
	_ ocr2types.BinaryNetworkEndpointLimits,
) (commontypes.BinaryNetworkEndpoint, error) {
	if !slices.Contains(peerIDs, f.peerID) {
		return nil, fmt.Errorf("peer %s is not one of the oracles of config digest %s", f.peerID, digest)
	}

	e := &ocr2Endpoint{
		net:     f.net,
		key:     endpointKey{digest: digest, peerID: f.peerID},
		peerIDs: slices.Clone(peerIDs),
		in:      make(chan commontypes.BinaryMessageWithSender, f.bufferSize),
		closed:  make(chan struct{}),
	}

	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	if _, exists := f.net.ocr2[e.key]; exists {
		return nil, fmt.Errorf("peer %s already has an OCR2 endpoint for config digest %s", f.peerID, digest)
	}

	f.net.ocr2[e.key] = e
	return e, nil
}

type ocr2Endpoint struct {
	net     *network
	key     endpointKey
	peerIDs []string
	in      chan commontypes.BinaryMessageWithSender

	closeOnce sync.Once
	closed    chan struct{}
}

var _ commontypes.BinaryNetworkEndpoint = (*ocr2Endpoint)(nil)

func (e *ocr2Endpoint) Start() error { return nil }

func (e *ocr2Endpoint) SendTo(payload []byte, to commontypes.OracleID) {
	if int(to) >= len(e.peerIDs) {
		return
	}

	e.deliver(payload, e.peerIDs[to])
}
func (e *ocr2Endpoint) Broadcast(payload []byte) {
	for _, peerID := range e.peerIDs {
		e.deliver(payload, peerID)
	}
}

func (e *ocr2Endpoint) deliver(payload []byte, peerID string) {
	e.net.mu.Lock()
	peer := e.net.ocr2[endpointKey{digest: e.key.digest, peerID: peerID}]
	e.net.mu.Unlock()
	if peer == nil {
		return
	}

	sender, ok := senderIndex(peer.peerIDs, e.key.peerID)
	if !ok {
		return
	}

	msg := commontypes.BinaryMessageWithSender{Msg: slices.Clone(payload), Sender: sender}
	select {
	case <-peer.closed:
	case peer.in <- msg:
	default:
	}
}

func (e *ocr2Endpoint) Receive() <-chan commontypes.BinaryMessageWithSender { return e.in }

func (e *ocr2Endpoint) Close() error {
	e.closeOnce.Do(func() {
		close(e.closed)
		e.net.mu.Lock()
		delete(e.net.ocr2, e.key)
		e.net.mu.Unlock()
	})
	return nil
}
