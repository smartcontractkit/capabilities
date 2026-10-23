package actions

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	commonmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/txevents"
)

// recordingTxEventEmitter captures emitted events so tests can assert on them.
type recordingTxEventEmitter struct {
	mu     sync.Mutex
	events []*txevents.ChainTransactionObserved
	err    error
}

func (r *recordingTxEventEmitter) Emit(_ context.Context, ev *txevents.ChainTransactionObserved) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return r.err
}

func (r *recordingTxEventEmitter) recorded() []*txevents.ChainTransactionObserved {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*txevents.ChainTransactionObserved(nil), r.events...)
}

func recordingEmitter(t *testing.T, service *EVM) *recordingTxEventEmitter {
	t.Helper()
	rec, ok := service.txEvents.(*recordingTxEventEmitter)
	require.True(t, ok, "service must use the recording emitter")
	return rec
}

func recordedTxEvents(t *testing.T, service *EVM) []*txevents.ChainTransactionObserved {
	t.Helper()
	return recordingEmitter(t, service).recorded()
}

type txEventEmitterFunc func(ctx context.Context, ev *txevents.ChainTransactionObserved) error

func (f txEventEmitterFunc) Emit(ctx context.Context, ev *txevents.ChainTransactionObserved) error {
	return f(ctx, ev)
}

func testRequestMetadata() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		WorkflowID:          "wf-id",
		WorkflowOwner:       "0xowner",
		WorkflowExecutionID: "exec-1",
	}
}

func newTestReporter(t *testing.T, emitter txevents.Emitter) *txEventReporter {
	t.Helper()
	svc := &EVM{chainSelector: 1, txEvents: emitter}
	r := svc.newTxEventReporter(logger.Sugared(logger.Test(t)), testRequestMetadata())
	require.NotNil(t, r)
	return r
}

func TestTxEventReporter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	hash := evmtypes.Hash(test.RandomBytes(32))
	other := evmtypes.Hash(test.RandomBytes(32))

	t.Run("disabled emission yields a nil reporter whose methods are no-ops", func(t *testing.T) {
		lggr := logger.Sugared(logger.Test(t))
		require.Nil(t, (&EVM{}).newTxEventReporter(lggr, testRequestMetadata()))
		r := (&EVM{txEvents: txevents.NopEmitter{}}).newTxEventReporter(lggr, testRequestMetadata())
		require.Nil(t, r)
		r.sent(ctx, &evmtypes.TransactionResult{TxHash: hash, TxStatus: evmtypes.TxSuccess})
		r.landed(ctx, hash)
		r.observed(ctx, hash, txevents.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("emits one event per transaction, the first time it is observed", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec)
		r.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }

		r.landed(ctx, hash)
		r.observed(ctx, hash, txevents.TxStatus_TX_STATUS_REVERTED) // same transaction again: ignored
		r.sent(ctx, &evmtypes.TransactionResult{TxHash: other, TxStatus: evmtypes.TxReverted})
		r.landed(ctx, other)

		events := rec.recorded()
		require.Len(t, events, 2)
		want := &txevents.ChainTransactionObserved{
			WorkflowExecutionId: "exec-1",
			WorkflowId:          "wf-id",
			ChainSelector:       1,
			TxHash:              formatTxHash(hash),
			TxStatus:            txevents.TxStatus_TX_STATUS_SUCCESS,
			ObservedAtMs:        1_700_000_000_000,
		}
		require.True(t, proto.Equal(want, events[0]), "got %v", events[0])
		require.Equal(t, formatTxHash(other), events[1].TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, events[1].TxStatus)
	})

	t.Run("sent ignores nil, fatal and zero-hash results", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec)
		r.sent(ctx, nil)
		r.sent(ctx, &evmtypes.TransactionResult{TxHash: hash, TxStatus: evmtypes.TxFatal})
		r.sent(ctx, &evmtypes.TransactionResult{TxStatus: evmtypes.TxSuccess})
		r.landed(ctx, evmtypes.Hash{})
		require.Empty(t, rec.recorded())
	})

	t.Run("survives emitter errors", func(t *testing.T) {
		rec := &recordingTxEventEmitter{err: errors.New("chip down")}
		r := newTestReporter(t, rec)
		r.landed(ctx, hash)
		require.Len(t, rec.recorded(), 1)

		unavailable := newTestReporter(t, txEventEmitterFunc(func(context.Context, *txevents.ChainTransactionObserved) error {
			return txevents.ErrEmitterUnavailable
		}))
		unavailable.landed(ctx, hash)
	})

	t.Run("emits even after the request context is done", func(t *testing.T) {
		var gotCtx context.Context
		r := newTestReporter(t, txEventEmitterFunc(func(ctx context.Context, _ *txevents.ChainTransactionObserved) error {
			gotCtx = ctx
			return nil
		}))
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		r.landed(cancelled, hash)
		require.NotNil(t, gotCtx)
		require.NoError(t, gotCtx.Err())
	})
}

func TestWriteReport_EmitsChainTransactionObserved(t *testing.T) {
	t.Parallel()

	expectReceipt := func(t *testing.T, evmServiceMock *commonmocks.EVMService, txHash evmtypes.Hash) {
		t.Helper()
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(1234)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)
	}

	assertEvent := func(t *testing.T, ev *txevents.ChainTransactionObserved, fixture writeReportTestFixture, txHash evmtypes.Hash, status txevents.TxStatus) {
		t.Helper()
		require.Equal(t, fixture.metadata.WorkflowExecutionID, ev.WorkflowExecutionId)
		require.Equal(t, fixture.metadata.WorkflowID, ev.WorkflowId)
		require.Equal(t, uint64(1), ev.ChainSelector)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, status, ev.TxStatus)
		require.NotZero(t, ev.ObservedAtMs)
	}

	t.Run("prior successful transmission is reported", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		expectReceipt(t, evmServiceMock, txHash)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		assertEvent(t, events[0], fixture, txHash, txevents.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("prior transmission with an invalid receiver is reported as a mined transaction", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{InvalidReceiver: true, State: contracts.TransmissionStateInvalidReceiver, GasLimit: big.NewInt(EnoughReceiverGas)}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectFailedTransmissionEvent(t, mockForwarderClient, txHash)
		expectReceipt(t, evmServiceMock, txHash)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		assertEvent(t, events[0], fixture, txHash, txevents.TxStatus_TX_STATUS_SUCCESS)
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("own transmission is reported as soon as the transaction manager returns, before the receipt", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{State: contracts.TransmissionStateNotAttempted}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		emittedBeforeReceipt := -1
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, fixture.receiver, fixture.request.Report, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{TxHash: txHash, TxStatus: evmtypes.TxSuccess, TxIdempotencyKey: "key"}, nil).Once()
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Run(func(mock.Arguments) { emittedBeforeReceipt = len(recordedTxEvents(t, service)) }).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded, GasLimit: big.NewInt(EnoughReceiverGas)}, nil).Once()
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		expectReceipt(t, evmServiceMock, txHash)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
		require.Equal(t, 1, emittedBeforeReceipt, "the event goes out when the transaction manager returns the mined transaction")

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1, "seeing our own hash again in the forwarder log does not emit twice")
		assertEvent(t, events[0], fixture, txHash, txevents.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("duplicate transmission reports our reverted transaction and the one that landed", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{State: contracts.TransmissionStateNotAttempted}, nil).Once()

		ownHash := evmtypes.Hash(test.RandomBytes(32))
		landedHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, fixture.receiver, fixture.request.Report, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxReverted, TxIdempotencyKey: "dup"}, nil).Once()
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded, GasLimit: big.NewInt(EnoughReceiverGas)}, nil).Once()
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, landedHash)
		expectReceipt(t, evmServiceMock, landedHash)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 2)
		assertEvent(t, events[0], fixture, ownHash, txevents.TxStatus_TX_STATUS_REVERTED)
		assertEvent(t, events[1], fixture, landedHash, txevents.TxStatus_TX_STATUS_SUCCESS)
		require.NotEqual(t, txevents.Key(events[0]), txevents.Key(events[1]))
	})

	t.Run("receipt lookup failure does not lose the event", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(nil, errors.New("rpc down"))

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.Error(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		assertEvent(t, events[0], fixture, txHash, txevents.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("request failing after the transaction was sent still reports it", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		_, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{State: contracts.TransmissionStateNotAttempted}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, fixture.receiver, fixture.request.Report, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{TxHash: txHash, TxStatus: evmtypes.TxSuccess}, nil).Once()
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{}, errors.New("rpc down"))

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.Error(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		assertEvent(t, events[0], fixture, txHash, txevents.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("emitter failure does not fail the write", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		recordingEmitter(t, service).err = errors.New("chip unreachable")
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		expectReceipt(t, evmServiceMock, txHash)

		txResult, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
		require.Equal(t, txHash[:], txResult.Response.TxHash)
		require.Len(t, recordedTxEvents(t, service), 1)
	})

	t.Run("disabled emission leaves the write untouched", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		service.txEvents = txevents.NopEmitter{}
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		expectReceipt(t, evmServiceMock, txHash)

		txResult, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
		require.Equal(t, txHash[:], txResult.Response.TxHash)
	})
}
