package ocr

import (
	"fmt"
	"math"
	"slices"
	"sync"

	"github.com/smartcontractkit/libocr/commontypes"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// This file implements the rage networking factories as in-process message passing, for embed
// mode: several instances of a binary running in one process, talking to each other over channels
// instead of over the network.
//
// libocr exports no portless transport, so there is one here. Every instance holds a peer
// identity as usual (a derived one, see keyring.go) and endpoints are still created per config
// digest from the peer IDs in the OCR config, so what runs on top cannot tell the difference -
// but nothing listens, dials, announces or discovers, and no port is needed. Which is the point:
// instances of one process have nothing to gain from a loopback socket between them, and needing
// a free port per instance is exactly the kind of setup embedding exists to avoid.
//
// What is deliberately dropped: the rate limits and message length limits libocr passes in (the
// send side is a channel, and enforcing a byte budget on it would only slow down a test), the
// bootstrapper locators (there are no addresses to dial), and delivery to peers that have not
// created their endpoint yet or whose mailbox is full (dropped, as ragep2p drops when a peer's
// buffer is full). Payloads are copied on send, so a caller reusing its buffer cannot corrupt a
// message in flight the way it could not over a real socket.

// network is the in-process transport shared by every instance in the process: a registry of
// mailboxes keyed by config digest and peer ID, plus the streams peer groups open between them.
type network struct {
	mu      sync.Mutex
	ocr2    map[endpointKey]*ocr2Endpoint
	ocr31   map[endpointKey]*binaryNetworkEndpoint2
	streams map[streamKey]*peerGroupStream
}

// embedNetwork is the process's in-process network. It is package-level for the same reason
// prometheus.DefaultRegisterer is: there is exactly one process, so there is exactly one network
// inside it, and every instance resolving its factories has to find the same one. Tests that want
// an isolated network construct one with newNetwork.
var embedNetwork = newNetwork()

func newNetwork() *network {
	return &network{
		ocr2:    map[endpointKey]*ocr2Endpoint{},
		ocr31:   map[endpointKey]*binaryNetworkEndpoint2{},
		streams: map[streamKey]*peerGroupStream{},
	}
}

// endpointKey identifies one peer's endpoint for one OCR instance. The digest is part of the key
// because a peer runs one endpoint per config digest, as it does over rage networking.
type endpointKey struct {
	digest ocr2types.ConfigDigest
	peerID string
}

// streamKey identifies one direction of one peer group stream: the stream owner is where messages
// arrive, and remote is who sends them.
type streamKey struct {
	digest [32]byte
	name   string
	owner  string
	remote string
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

// bufferSize returns size, or fallback when size is not positive, so an unset buffer setting
// still yields a usable mailbox.
func bufferSize(size, fallback int) int {
	if size > 0 {
		return size
	}
	return fallback
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

func (e *ocr2Endpoint) Broadcast(payload []byte) {
	for _, peerID := range e.peerIDs {
		if peerID == e.key.peerID {
			continue
		}
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

// ocr31Factory creates in-process OCR3.1 endpoints for one peer.
type ocr31Factory struct {
	net        *network
	peerID     string
	bufferSize int
}

var _ ocr2types.BinaryNetworkEndpoint2Factory = ocr31Factory{}

func (f ocr31Factory) PeerID() string { return f.peerID }

func (f ocr31Factory) NewEndpoint(
	digest ocr2types.ConfigDigest,
	peerIDs []string,
	_ []commontypes.BootstrapperLocator,
	defaultPriorityConfig ocr2types.BinaryNetworkEndpoint2Config,
	_ ocr2types.BinaryNetworkEndpoint2Config,
) (ocr2types.BinaryNetworkEndpoint2, error) {
	if !slices.Contains(peerIDs, f.peerID) {
		return nil, fmt.Errorf("peer %s is not one of the oracles of config digest %s", f.peerID, digest)
	}

	// One mailbox carries both priorities, each message keeping the priority it was sent with, so
	// only the default priority config's buffer override is meaningful here.
	size := f.bufferSize
	if override := defaultPriorityConfig.OverrideIncomingMessageBufferSize; override != nil {
		size = bufferSize(*override, size)
	}

	e := &binaryNetworkEndpoint2{
		net:     f.net,
		key:     endpointKey{digest: digest, peerID: f.peerID},
		peerIDs: slices.Clone(peerIDs),
		in:      make(chan ocr2types.InboundBinaryMessageWithSender, size),
		closed:  make(chan struct{}),
	}

	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	if _, exists := f.net.ocr31[e.key]; exists {
		return nil, fmt.Errorf("peer %s already has an OCR3.1 endpoint for config digest %s", f.peerID, digest)
	}
	f.net.ocr31[e.key] = e
	return e, nil
}

// binaryNetworkEndpoint2 is an ocr2types.BinaryNetworkEndpoint2 over the in-process network.
type binaryNetworkEndpoint2 struct {
	net     *network
	key     endpointKey
	peerIDs []string
	in      chan ocr2types.InboundBinaryMessageWithSender

	closeOnce sync.Once
	closed    chan struct{}
}

var _ ocr2types.BinaryNetworkEndpoint2 = (*binaryNetworkEndpoint2)(nil)

func (e *binaryNetworkEndpoint2) SendTo(msg ocr2types.OutboundBinaryMessage, to commontypes.OracleID) {
	if int(to) >= len(e.peerIDs) {
		return
	}
	e.deliver(msg, e.peerIDs[to])
}

func (e *binaryNetworkEndpoint2) Broadcast(msg ocr2types.OutboundBinaryMessage) {
	for _, peerID := range e.peerIDs {
		if peerID == e.key.peerID {
			continue
		}
		e.deliver(msg, peerID)
	}
}

func (e *binaryNetworkEndpoint2) deliver(msg ocr2types.OutboundBinaryMessage, peerID string) {
	e.net.mu.Lock()
	peer := e.net.ocr31[endpointKey{digest: e.key.digest, peerID: peerID}]
	e.net.mu.Unlock()
	if peer == nil {
		return
	}

	sender, ok := senderIndex(peer.peerIDs, e.key.peerID)
	if !ok {
		return
	}

	inbound, ok := inboundMessage(msg)
	if !ok {
		return
	}

	select {
	case <-peer.closed:
	case peer.in <- ocr2types.InboundBinaryMessageWithSender{InboundBinaryMessage: inbound, Sender: sender}:
	default:
	}
}

func (e *binaryNetworkEndpoint2) Receive() <-chan ocr2types.InboundBinaryMessageWithSender {
	return e.in
}

func (e *binaryNetworkEndpoint2) Close() error {
	e.closeOnce.Do(func() {
		close(e.closed)
		e.net.mu.Lock()
		delete(e.net.ocr31, e.key)
		e.net.mu.Unlock()
	})
	return nil
}

// inboundMessage converts a message as the sender wrote it into the message the receiver reads,
// which is what a transport does: the wire has one representation, and each side sees its own view
// of it. A request arrives with a handle the receiver answers through; ok is false for a message
// type this transport does not know, which is dropped rather than delivered as something else.
func inboundMessage(msg ocr2types.OutboundBinaryMessage) (ocr2types.InboundBinaryMessage, bool) {
	switch m := msg.(type) {
	case ocr2types.OutboundBinaryMessagePlain:
		return ocr2types.InboundBinaryMessagePlain{
			Payload:  slices.Clone(m.Payload),
			Priority: m.Priority,
		}, true
	case ocr2types.OutboundBinaryMessageRequest:
		return ocr2types.InboundBinaryMessageRequest{
			RequestHandle: requestHandle{priority: m.Priority},
			Payload:       slices.Clone(m.Payload),
			Priority:      m.Priority,
		}, true
	case ocr2types.OutboundBinaryMessageResponse:
		return ocr2types.InboundBinaryMessageResponse{
			Payload:  slices.Clone(m.Payload),
			Priority: m.Priority,
		}, true
	default:
		return nil, false
	}
}

// requestHandle is what a receiver answers an inbound request through. It carries only the
// request's priority: with a ragep2p backend a response has to be sent at the priority its
// request used or it is dropped, and libocr's own endpoints keep that invariant by building the
// response from the handle. Where the response goes is not its business - the caller passes the
// requester to SendTo, as it does for any other message.
type requestHandle struct {
	priority ocr2types.BinaryMessageOutboundPriority
}

var _ ocr2types.RequestHandle = requestHandle{}

func (h requestHandle) MakeResponse(payload []byte) ocr2types.OutboundBinaryMessageResponse {
	return ocr2types.MustMakeOutboundBinaryMessageResponse(h, payload, h.priority)
}

// inprocPeerGroupFactory creates in-process DON-to-DON peer groups for one peer.
type inprocPeerGroupFactory struct {
	net    *network
	peerID string
}

var _ creproxy.PeerGroupFactory = inprocPeerGroupFactory{}

func (f inprocPeerGroupFactory) NewPeerGroup(digest [32]byte, peerIDs []string, _ []creproxy.BootstrapperInfo) (creproxy.PeerGroup, error) {
	if !slices.Contains(peerIDs, f.peerID) {
		return nil, fmt.Errorf("peer %s is not a member of peer group %x", f.peerID, digest)
	}
	return &inprocPeerGroup{net: f.net, digest: digest, peerID: f.peerID, peerIDs: slices.Clone(peerIDs)}, nil
}

// inprocPeerGroup hands out streams to other members of the group. Its streams are closed with
// it, per the creproxy.PeerGroup contract.
type inprocPeerGroup struct {
	net     *network
	digest  [32]byte
	peerID  string
	peerIDs []string

	mu      sync.Mutex
	streams []*peerGroupStream
	closed  bool
}

var _ creproxy.PeerGroup = (*inprocPeerGroup)(nil)

func (g *inprocPeerGroup) NewStream(remotePeerID string, args creproxy.StreamArgs) (creproxy.PeerGroupStream, error) {
	if !slices.Contains(g.peerIDs, remotePeerID) {
		return nil, fmt.Errorf("peer %s is not a member of peer group %x", remotePeerID, g.digest)
	}

	s := &peerGroupStream{
		net: g.net,
		// Keyed by where messages arrive, so the two ends of the same named stream are each
		// other's mirror: what this end sends is looked up under the remote's key.
		key:    streamKey{digest: g.digest, name: args.StreamName, owner: g.peerID, remote: remotePeerID},
		in:     make(chan []byte, bufferSize(args.IncomingBufferSize, defaultBufferSize)),
		closed: make(chan struct{}),
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, fmt.Errorf("peer group %x is closed", g.digest)
	}
	g.streams = append(g.streams, s)
	g.mu.Unlock()

	g.net.mu.Lock()
	defer g.net.mu.Unlock()
	if _, exists := g.net.streams[s.key]; exists {
		return nil, fmt.Errorf("stream %q to peer %s already exists in peer group %x", args.StreamName, remotePeerID, g.digest)
	}
	g.net.streams[s.key] = s
	return s, nil
}

func (g *inprocPeerGroup) Close() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	streams := g.streams
	g.streams = nil
	g.mu.Unlock()

	var err error
	for _, s := range streams {
		if cerr := s.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

// peerGroupStream is one end of a named bidirectional stream between two group members.
type peerGroupStream struct {
	net *network
	key streamKey
	in  chan []byte

	closeOnce sync.Once
	closed    chan struct{}
}

var _ creproxy.PeerGroupStream = (*peerGroupStream)(nil)

// SendMessage delivers to the stream the remote end opened back to us, dropping the message if it
// has not opened one yet or its buffer is full - the same outcome as sending on a rage stream to a
// peer that is not connected.
func (s *peerGroupStream) SendMessage(data []byte) {
	mirror := streamKey{digest: s.key.digest, name: s.key.name, owner: s.key.remote, remote: s.key.owner}

	s.net.mu.Lock()
	peer := s.net.streams[mirror]
	s.net.mu.Unlock()
	if peer == nil {
		return
	}

	select {
	case <-peer.closed:
	case peer.in <- slices.Clone(data):
	default:
	}
}

func (s *peerGroupStream) ReceiveMessages() <-chan []byte { return s.in }

func (s *peerGroupStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.net.mu.Lock()
		delete(s.net.streams, s.key)
		s.net.mu.Unlock()
	})
	return nil
}
