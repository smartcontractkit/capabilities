package ocr

import (
	"fmt"
	"math"
	"slices"
	"sync"

	"github.com/smartcontractkit/libocr/commontypes"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// This file implements the rage networking factories as in-process message passing, for embed
// mode: several instances of a binary running in one process, talking to each other over channels
// instead of over the network.
//
// libocr exports no portless transport, so there is one here. Every instance holds a peer
// identity as usual (a derived one, see embed.go) and endpoints are still created per config
// digest from the peer IDs in the OCR config, so what runs on top cannot tell the difference -
// but nothing listens, dials, announces or discovers, and no port is needed. Which is the point:
// instances of one process have nothing to gain from a loopback socket between them, and needing
// a free port per instance is exactly the kind of setup embedding exists to avoid.
//
// What is deliberately dropped: the rate limits and message length limits libocr passes in (the
// send side is a channel, and enforcing a byte budget on it would only slow down a test), the
// bootstrapper locators (there are no addresses to dial), and delivery to peers that have not
// created their endpoint yet or whose mailbox is full (dropped, as ragep2p drops when a peer's
// buffer is full). A message to the sender itself is not among them: libocr's own endpoints deliver
// those, and the protocol needs them. Payloads are copied on send, so a caller reusing its buffer cannot corrupt a
// message in flight the way it could not over a real socket.

// network is the in-process transport shared by every instance in the process: a registry of
// mailboxes keyed by config digest and peer ID.
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

// ocr2Factory creates in-process OCR2 endpoints for one peer.
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

// ocr2Endpoint is a commontypes.BinaryNetworkEndpoint delivering to other endpoints registered on
// the same network under the same config digest.
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

// Broadcast delivers to every oracle of this configuration, this one included.
//
// Including itself is not a detail: a libocr endpoint loops a broadcast back to its own receive
// channel (see ocrEndpointV2.SendTo's sendToSelf), and the protocol relies on it - a leader learns
// what round it started by receiving its own round-start like anyone else. An endpoint that skipped
// itself would leave a leader broadcasting rounds it never enters, dropping every observation those
// rounds produced as having the wrong sequence number, until the progress timer moved the epoch on
// and it happened again.
func (e *ocr2Endpoint) Broadcast(payload []byte) {
	for _, peerID := range e.peerIDs {
		e.deliver(payload, peerID)
	}
}

// deliver hands a copy of payload to peerID's mailbox, dropping it if that peer has no endpoint
// for this config digest or its mailbox is full. Never blocks: libocr requires SendTo and
// Broadcast not to.
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

// Close unregisters the endpoint. The receive channel is left open: libocr may still be selecting
// on it, and a closed channel would hand it an endless stream of zero-valued messages.
func (e *ocr2Endpoint) Close() error {
	e.closeOnce.Do(func() {
		close(e.closed)
		e.net.mu.Lock()
		delete(e.net.ocr2, e.key)
		e.net.mu.Unlock()
	})
	return nil
}
