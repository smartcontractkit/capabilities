package ocr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/commontypes"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// receiveTimeout is how long a test waits for a message that should already be on its way. The
// transport is channels in one process, so anything slower than this is a bug rather than a slow
// machine.
const receiveTimeout = 5 * time.Second

var testDigest = ocr2types.ConfigDigest{1, 2, 3}

// testPeerIDs returns the derived peer IDs of the first count instances, in oracle order.
func testPeerIDs(t *testing.T, count int) []string {
	t.Helper()
	peerIDs := make([]string, count)
	for i := range count {
		peerID, err := DeterministicPeerID(i)
		require.NoError(t, err)
		peerIDs[i] = peerID.String()
	}
	return peerIDs
}

func TestInprocOCR2Endpoints(t *testing.T) {
	net := newNetwork()
	peerIDs := testPeerIDs(t, 3)

	endpoints := make([]commontypes.BinaryNetworkEndpoint, len(peerIDs))
	for i, peerID := range peerIDs {
		factory := ocr2Factory{net: net, peerID: peerID, bufferSize: 10}
		require.Equal(t, peerID, factory.PeerID())

		e, err := factory.NewEndpoint(testDigest, peerIDs, nil, 1, ocr2types.BinaryNetworkEndpointLimits{})
		require.NoError(t, err)
		require.NoError(t, e.Start())
		t.Cleanup(func() { assert.NoError(t, e.Close()) })
		endpoints[i] = e
	}

	t.Run("SendTo reaches only the addressed oracle", func(t *testing.T) {
		endpoints[2].SendTo([]byte("for oracle 0"), 0)

		msg := receive(t, endpoints[0].Receive())
		assert.Equal(t, []byte("for oracle 0"), msg.Msg)
		assert.Equal(t, commontypes.OracleID(2), msg.Sender)
		assertNothingReceived(t, endpoints[1].Receive())
	})

	t.Run("Broadcast reaches every other oracle but not the sender", func(t *testing.T) {
		endpoints[0].Broadcast([]byte("to everyone"))

		for _, i := range []int{1, 2} {
			msg := receive(t, endpoints[i].Receive())
			assert.Equal(t, []byte("to everyone"), msg.Msg)
			assert.Equal(t, commontypes.OracleID(0), msg.Sender)
		}
		assertNothingReceived(t, endpoints[0].Receive())
	})

	t.Run("payloads are copied, so a reused buffer cannot rewrite a sent message", func(t *testing.T) {
		payload := []byte("original")
		endpoints[1].SendTo(payload, 0)
		copy(payload, "OVERWROTE")

		assert.Equal(t, []byte("original"), receive(t, endpoints[0].Receive()).Msg)
	})

	t.Run("a closed endpoint receives nothing more", func(t *testing.T) {
		factory := ocr2Factory{net: net, peerID: peerIDs[0], bufferSize: 10}
		// The endpoint registered in the parent test still holds this peer's slot.
		_, err := factory.NewEndpoint(testDigest, peerIDs, nil, 1, ocr2types.BinaryNetworkEndpointLimits{})
		require.ErrorContains(t, err, "already has an OCR2 endpoint")

		other := ocr2Factory{net: net, peerID: peerIDs[1], bufferSize: 10}
		e, err := other.NewEndpoint(ocr2types.ConfigDigest{9}, peerIDs, nil, 1, ocr2types.BinaryNetworkEndpointLimits{})
		require.NoError(t, err)
		require.NoError(t, e.Close())

		// Nothing to deliver to under that digest now: this must drop rather than block or panic.
		endpoints[0].Broadcast([]byte("dropped"))
		assertNothingReceived(t, e.Receive())
	})

	t.Run("a peer outside the oracle set is refused", func(t *testing.T) {
		stranger, err := DeterministicPeerID(len(peerIDs) + 1)
		require.NoError(t, err)

		factory := ocr2Factory{net: net, peerID: stranger.String(), bufferSize: 10}
		_, err = factory.NewEndpoint(testDigest, peerIDs, nil, 1, ocr2types.BinaryNetworkEndpointLimits{})
		require.ErrorContains(t, err, "is not one of the oracles")
	})
}

func TestInprocOCR31Endpoints(t *testing.T) {
	net := newNetwork()
	peerIDs := testPeerIDs(t, 2)

	endpoints := make([]ocr2types.BinaryNetworkEndpoint2, len(peerIDs))
	for i, peerID := range peerIDs {
		factory := ocr31Factory{net: net, peerID: peerID, bufferSize: 10}
		e, err := factory.NewEndpoint(testDigest, peerIDs, nil,
			ocr2types.BinaryNetworkEndpoint2Config{}, ocr2types.BinaryNetworkEndpoint2Config{})
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, e.Close()) })
		endpoints[i] = e
	}

	t.Run("a plain message keeps its payload and priority", func(t *testing.T) {
		endpoints[0].SendTo(ocr2types.OutboundBinaryMessagePlain{
			Payload:  []byte("plain"),
			Priority: ocr2types.BinaryMessagePriorityLow,
		}, 1)

		msg := receive(t, endpoints[1].Receive())
		assert.Equal(t, commontypes.OracleID(0), msg.Sender)
		plain, ok := msg.InboundBinaryMessage.(ocr2types.InboundBinaryMessagePlain)
		require.True(t, ok, "expected a plain message, got %T", msg.InboundBinaryMessage)
		assert.Equal(t, []byte("plain"), plain.Payload)
		assert.Equal(t, ocr2types.BinaryMessagePriorityLow, plain.Priority)
	})

	t.Run("a request is answered through its handle, at the request's priority", func(t *testing.T) {
		endpoints[0].SendTo(ocr2types.OutboundBinaryMessageRequest{
			Payload:  []byte("question"),
			Priority: ocr2types.BinaryMessagePriorityDefault,
		}, 1)

		inbound := receive(t, endpoints[1].Receive())
		request, ok := inbound.InboundBinaryMessage.(ocr2types.InboundBinaryMessageRequest)
		require.True(t, ok, "expected a request, got %T", inbound.InboundBinaryMessage)
		assert.Equal(t, []byte("question"), request.Payload)
		require.NotNil(t, request.RequestHandle)

		endpoints[1].SendTo(request.RequestHandle.MakeResponse([]byte("answer")), inbound.Sender)

		reply := receive(t, endpoints[0].Receive())
		assert.Equal(t, commontypes.OracleID(1), reply.Sender)
		response, ok := reply.InboundBinaryMessage.(ocr2types.InboundBinaryMessageResponse)
		require.True(t, ok, "expected a response, got %T", reply.InboundBinaryMessage)
		assert.Equal(t, []byte("answer"), response.Payload)
		// A ragep2p backend drops a response whose priority differs from its request's, so the
		// handle has to preserve it.
		assert.Equal(t, ocr2types.BinaryMessagePriorityDefault, response.Priority)
	})

	t.Run("the incoming buffer override sizes the mailbox", func(t *testing.T) {
		size := 1
		factory := ocr31Factory{net: net, peerID: peerIDs[0], bufferSize: 10}
		e, err := factory.NewEndpoint(ocr2types.ConfigDigest{7}, peerIDs, nil,
			ocr2types.BinaryNetworkEndpoint2Config{OverrideIncomingMessageBufferSize: &size},
			ocr2types.BinaryNetworkEndpoint2Config{})
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, e.Close()) })

		assert.Equal(t, size, cap(e.(*binaryNetworkEndpoint2).in))
	})
}

func TestInprocPeerGroupStreams(t *testing.T) {
	net := newNetwork()
	peerIDs := testPeerIDs(t, 2)
	var digest [32]byte
	digest[0] = 42

	groups := make([]creproxy.PeerGroup, len(peerIDs))
	for i, peerID := range peerIDs {
		g, err := inprocPeerGroupFactory{net: net, peerID: peerID}.NewPeerGroup(digest, peerIDs, nil)
		require.NoError(t, err)
		groups[i] = g
	}

	args := creproxy.StreamArgs{StreamName: "stream", IncomingBufferSize: 10, OutgoingBufferSize: 10}
	streamFrom0, err := groups[0].NewStream(peerIDs[1], args)
	require.NoError(t, err)
	streamFrom1, err := groups[1].NewStream(peerIDs[0], args)
	require.NoError(t, err)

	t.Run("messages flow both ways between the two ends", func(t *testing.T) {
		streamFrom0.SendMessage([]byte("ping"))
		assert.Equal(t, []byte("ping"), receive(t, streamFrom1.ReceiveMessages()))

		streamFrom1.SendMessage([]byte("pong"))
		assert.Equal(t, []byte("pong"), receive(t, streamFrom0.ReceiveMessages()))
	})

	t.Run("a differently named stream is a different stream", func(t *testing.T) {
		other, err := groups[0].NewStream(peerIDs[1], creproxy.StreamArgs{StreamName: "other", IncomingBufferSize: 10})
		require.NoError(t, err)

		// Nothing is listening on "other" at the remote end, so this is dropped rather than
		// delivered to the stream that happens to connect the same two peers.
		other.SendMessage([]byte("nowhere"))
		assertNothingReceived(t, streamFrom1.ReceiveMessages())
	})

	t.Run("a non-member cannot be streamed to", func(t *testing.T) {
		stranger, err := DeterministicPeerID(len(peerIDs) + 1)
		require.NoError(t, err)

		_, err = groups[0].NewStream(stranger.String(), args)
		require.ErrorContains(t, err, "is not a member of peer group")
	})

	t.Run("closing a group closes its streams and stops delivery", func(t *testing.T) {
		require.NoError(t, groups[1].Close())

		streamFrom0.SendMessage([]byte("after close"))
		assertNothingReceived(t, streamFrom1.ReceiveMessages())

		// Closing again is a no-op, as is closing a stream the group already closed.
		require.NoError(t, groups[1].Close())
		require.NoError(t, streamFrom1.Close())

		_, err := groups[1].NewStream(peerIDs[0], args)
		require.ErrorContains(t, err, "is closed")

		require.NoError(t, groups[0].Close())
	})
}

// receive returns the next value on ch, failing the test if none arrives.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(receiveTimeout):
		var zero T
		t.Fatalf("timed out waiting for a message")
		return zero
	}
}

// assertNothingReceived fails if anything is waiting on ch. Nothing sleeps here: delivery is a
// channel send inside the SendTo call that preceded this, so a message that was going to arrive
// has already arrived.
func assertNothingReceived[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("expected no message, got %v", v)
	default:
	}
}
