package don2don_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/x/don2don"
	don2dontypes "github.com/smartcontractkit/capabilities/libs/x/don2don/types"
	rage "github.com/smartcontractkit/capabilities/libs/x/rage"
	"github.com/smartcontractkit/capabilities/libs/x/rage/mocks"

	commonMocks "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
)

type testReceiver struct {
	ch chan *don2dontypes.MessageBody
}

func newReceiver() *testReceiver {
	return &testReceiver{
		ch: make(chan *don2dontypes.MessageBody, 100),
	}
}

func (r *testReceiver) Receive(_ context.Context, msg *don2dontypes.MessageBody) {
	r.ch <- msg
}

func TestDispatcher_CleanStartClose(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	ctx := t.Context()
	peer := mocks.NewPeer(t)
	recvCh := make(<-chan rage.Message)
	peer.On("Receive", mock.Anything).Return(recvCh)
	peer.On("ID", mock.Anything).Return(rage.PeerID{})
	wrapper := mocks.NewPeerWrapper(t)
	wrapper.On("GetPeer").Return(peer)
	signer := mocks.NewSigner(t)
	signer.EXPECT().Initialize().Return(nil)
	registry := commonMocks.NewCapabilitiesRegistry(t)

	dispatcher, err := don2don.NewDispatcher(newTestConfig(false), wrapper, nil, signer, registry, lggr)
	require.NoError(t, err)
	require.NoError(t, dispatcher.Start(ctx))
	require.NoError(t, dispatcher.Close())
}

func TestDispatcher_Receive(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	ctx := t.Context()
	privKey1, peerID1 := newKeyPair(t)
	_, peerID2 := newKeyPair(t)

	peer := mocks.NewPeer(t)
	recvCh := make(chan rage.Message)
	peer.On("Receive", mock.Anything).Return((<-chan rage.Message)(recvCh))
	peer.On("ID", mock.Anything).Return(peerID2)
	wrapper := mocks.NewPeerWrapper(t)
	wrapper.On("GetPeer").Return(peer)
	signer := mocks.NewSigner(t)
	signer.EXPECT().Initialize().Return(nil)
	signer.EXPECT().Sign(mock.Anything).Return(nil, errors.New("not implemented"))
	registry := commonMocks.NewCapabilitiesRegistry(t)

	dispatcher, err := don2don.NewDispatcher(newTestConfig(false), wrapper, nil, signer, registry, lggr)
	require.NoError(t, err)
	require.NoError(t, dispatcher.Start(ctx))

	rcv := newReceiver()
	err = dispatcher.SetReceiver(capID1, donID1, rcv)
	require.NoError(t, err)

	// supported capability
	recvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload1))
	// unknown capability
	recvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID2, donID1, []byte(payload1))
	// sender doesn't match
	invalid := encodeAndSign(t, privKey1, peerID1, peerID2, capID2, donID1, []byte(payload1))
	invalid.Sender = peerID2
	recvCh <- invalid
	// supported capability again
	recvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload2))

	m := <-rcv.ch
	require.Equal(t, payload1, string(m.Payload))
	m = <-rcv.ch
	require.Equal(t, payload2, string(m.Payload))

	dispatcher.RemoveReceiver(capID1, donID1)
	require.NoError(t, dispatcher.Close())
}

func TestDispatcher_ReceiveForMethod(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	ctx := t.Context()
	privKey1, peerID1 := newKeyPair(t)
	_, peerID2 := newKeyPair(t)

	peer := mocks.NewPeer(t)
	recvCh := make(chan rage.Message)
	peer.On("Receive", mock.Anything).Return((<-chan rage.Message)(recvCh))
	peer.On("ID", mock.Anything).Return(peerID2)
	wrapper := mocks.NewPeerWrapper(t)
	wrapper.On("GetPeer").Return(peer)
	signer := mocks.NewSigner(t)
	signer.EXPECT().Initialize().Return(nil)
	signer.EXPECT().Sign(mock.Anything).Return(nil, errors.New("not implemented"))
	registry := commonMocks.NewCapabilitiesRegistry(t)

	dispatcher, err := don2don.NewDispatcher(newTestConfig(false), wrapper, nil, signer, registry, lggr)
	require.NoError(t, err)
	require.NoError(t, dispatcher.Start(ctx))

	methodA, methodB := "methodA", "methodB"
	rcvA, rcvB := newReceiver(), newReceiver()
	require.NoError(t, dispatcher.SetReceiverForMethod(capID1, donID1, methodA, rcvA))
	require.NoError(t, dispatcher.SetReceiverForMethod(capID1, donID1, methodB, rcvB))

	// supported capability / methodA
	recvCh <- encodeAndSignForMethod(t, privKey1, peerID1, peerID2, capID1, methodA, donID1, []byte(payload1))
	// unknown capability
	recvCh <- encodeAndSignForMethod(t, privKey1, peerID1, peerID2, capID2, methodA, donID1, []byte(payload1))
	// supported capability / methodB
	recvCh <- encodeAndSignForMethod(t, privKey1, peerID1, peerID2, capID1, methodB, donID1, []byte(payload2))

	m := <-rcvA.ch
	require.Equal(t, payload1, string(m.Payload))
	m = <-rcvB.ch
	require.Equal(t, payload2, string(m.Payload))

	dispatcher.RemoveReceiverForMethod(capID1, donID1, methodA)
	dispatcher.RemoveReceiverForMethod(capID1, donID1, methodB)
	require.NoError(t, dispatcher.Close())
}

func TestDispatcher_RespondWithError(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	ctx := t.Context()
	privKey1, peerID1 := newKeyPair(t)
	_, peerID2 := newKeyPair(t)

	peer := mocks.NewPeer(t)
	recvCh := make(chan rage.Message)
	peer.On("Receive", mock.Anything).Return((<-chan rage.Message)(recvCh))
	peer.On("ID", mock.Anything).Return(peerID2)
	sendCh := make(chan rage.PeerID)
	peer.On("Send", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		peerID := args.Get(0).(rage.PeerID)
		sendCh <- peerID
	}).Return(nil)
	wrapper := mocks.NewPeerWrapper(t)
	wrapper.On("GetPeer").Return(peer)
	signer := mocks.NewSigner(t)
	signer.EXPECT().Initialize().Return(nil)
	signer.EXPECT().Sign(mock.Anything).Return([]byte{1, 2, 3}, nil)
	registry := commonMocks.NewCapabilitiesRegistry(t)

	dispatcher, err := don2don.NewDispatcher(newTestConfig(false), wrapper, nil, signer, registry, lggr)
	require.NoError(t, err)
	require.NoError(t, dispatcher.Start(ctx))

	// unknown capability
	recvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload1))
	responseDestPeerID := <-sendCh
	require.Equal(t, peerID1, responseDestPeerID)

	require.NoError(t, dispatcher.Close())
}

func TestDispatcher_ReceiveFromBothPeers(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	ctx := t.Context()
	privKey1, peerID1 := newKeyPair(t)
	_, peerID2 := newKeyPair(t)

	peer := mocks.NewPeer(t)
	recvCh := make(chan rage.Message)
	peer.On("Receive", mock.Anything).Return((<-chan rage.Message)(recvCh))
	peer.On("ID", mock.Anything).Return(peerID2)
	wrapper := mocks.NewPeerWrapper(t)
	wrapper.On("GetPeer").Return(peer)
	signer := mocks.NewSigner(t)
	signer.EXPECT().Initialize().Return(nil)
	sharedPeer := mocks.NewSharedPeer(t)
	sharedPeerRecvCh := make(chan rage.Message)
	sharedPeer.On("Receive", mock.Anything).Return((<-chan rage.Message)(sharedPeerRecvCh))
	sharedPeer.On("ID", mock.Anything).Return(peerID2)
	registry := commonMocks.NewCapabilitiesRegistry(t)

	dispatcher, err := don2don.NewDispatcher(newTestConfig(false), wrapper, sharedPeer, signer, registry, lggr)
	require.NoError(t, err)
	require.NoError(t, dispatcher.Start(ctx))

	rcv := newReceiver()
	err = dispatcher.SetReceiver(capID1, donID1, rcv)
	require.NoError(t, err)

	recvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload1))
	sharedPeerRecvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload2))
	close(sharedPeerRecvCh) // make sure Dispatcher handles SharedPeer shutdown gracefully

	m := <-rcv.ch
	require.Equal(t, payload1, string(m.Payload))
	m = <-rcv.ch
	require.Equal(t, payload2, string(m.Payload))

	dispatcher.RemoveReceiver(capID1, donID1)
	require.NoError(t, dispatcher.Close())
}

func TestDispatcher_SendToSharedPeer(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	ctx := t.Context()
	_, peerID1 := newKeyPair(t)
	_, peerID2 := newKeyPair(t)

	peer := mocks.NewPeer(t)
	recvCh := make(chan rage.Message)
	peer.On("Receive", mock.Anything).Return((<-chan rage.Message)(recvCh))
	peer.On("ID", mock.Anything).Return(peerID2)
	wrapper := mocks.NewPeerWrapper(t)
	wrapper.On("GetPeer").Return(peer)
	signer := mocks.NewSigner(t)
	signer.EXPECT().Initialize().Return(nil)
	signer.EXPECT().Sign(mock.Anything).Return([]byte("signed payload"), nil)
	sharedPeer := mocks.NewSharedPeer(t)
	sharedPeerRecvCh := make(chan rage.Message)
	sharedPeer.On("Receive", mock.Anything).Return((<-chan rage.Message)(sharedPeerRecvCh))
	sharedPeer.On("ID", mock.Anything).Return(peerID2)
	sharedPeer.On("Send", mock.Anything, mock.Anything).Return(nil)
	registry := commonMocks.NewCapabilitiesRegistry(t)

	dispatcher, err := don2don.NewDispatcher(newTestConfig(true), wrapper, sharedPeer, signer, registry, lggr)
	require.NoError(t, err)
	require.NoError(t, dispatcher.Start(ctx))

	require.NoError(t, dispatcher.Send(peerID1, &don2dontypes.MessageBody{}))
	// mocks expect Sign() and Send()

	require.NoError(t, dispatcher.Close())
}

// panicOnFirstReceiver panics on the first Receive call and records all
// subsequent messages so tests can assert the goroutine survived.
type panicOnFirstReceiver struct {
	mu       sync.Mutex
	calls    int
	received chan *don2dontypes.MessageBody
}

func newPanicOnFirstReceiver() *panicOnFirstReceiver {
	return &panicOnFirstReceiver{received: make(chan *don2dontypes.MessageBody, 10)}
}

func (r *panicOnFirstReceiver) Receive(_ context.Context, msg *don2dontypes.MessageBody) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		panic("deliberate panic from receiver")
	}
	r.received <- msg
}

// TestDispatcher_ReceiverPanicDoesNotKillLoop verifies that a panic inside a
// receiver's Receive() method is caught by the recover() wrapper in the
// dispatcher goroutine and does not prevent subsequent messages from being
// delivered to the same receiver.
func TestDispatcher_ReceiverPanicDoesNotKillLoop(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	ctx := t.Context()
	privKey1, peerID1 := newKeyPair(t)
	_, peerID2 := newKeyPair(t)

	peer := mocks.NewPeer(t)
	recvCh := make(chan rage.Message)
	peer.On("Receive", mock.Anything).Return((<-chan rage.Message)(recvCh))
	peer.On("ID", mock.Anything).Return(peerID2)
	wrapper := mocks.NewPeerWrapper(t)
	wrapper.On("GetPeer").Return(peer)
	signer := mocks.NewSigner(t)
	signer.EXPECT().Initialize().Return(nil)
	registry := commonMocks.NewCapabilitiesRegistry(t)

	dispatcher, err := don2don.NewDispatcher(newTestConfig(false), wrapper, nil, signer, registry, lggr)
	require.NoError(t, err)
	require.NoError(t, dispatcher.Start(ctx))

	rcv := newPanicOnFirstReceiver()
	err = dispatcher.SetReceiver(capID1, donID1, rcv)
	require.NoError(t, err)

	// First message triggers the panic; the goroutine must survive.
	recvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload1))
	// Second message must still be delivered.
	recvCh <- encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload2))

	m := <-rcv.received
	require.Equal(t, payload2, string(m.Payload))

	dispatcher.RemoveReceiver(capID1, donID1)
	require.NoError(t, dispatcher.Close())
}

func newTestConfig(sendToSharedPeer bool) don2don.DispatcherConfig {
	return don2don.DispatcherConfig{
		SupportedVersion:   1,
		ReceiverBufferSize: 10000,
		RateLimit: don2don.DispatcherRateLimit{
			GlobalRPS:      800.0,
			GlobalBurst:    100,
			PerSenderRPS:   10.0,
			PerSenderBurst: 50,
		},
		SendToSharedPeer: sendToSharedPeer,
	}
}
