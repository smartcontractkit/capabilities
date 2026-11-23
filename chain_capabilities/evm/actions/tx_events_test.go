package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	commonmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	eventspb "github.com/smartcontractkit/chainlink-protos/workflows/go/v2"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/txevents"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
)

// recordingTxEventEmitter captures emitted events so tests can assert on them.
type recordingTxEventEmitter struct {
	mu     sync.Mutex
	events []*eventspb.ChainTransactionObserved
	err    error
}

func (r *recordingTxEventEmitter) Emit(_ context.Context, ev *eventspb.ChainTransactionObserved) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return r.err
}

func (r *recordingTxEventEmitter) recorded() []*eventspb.ChainTransactionObserved {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*eventspb.ChainTransactionObserved(nil), r.events...)
}

func recordingEmitter(t *testing.T, service *EVM) *recordingTxEventEmitter {
	t.Helper()
	rec, ok := service.txEvents.(*recordingTxEventEmitter)
	require.True(t, ok, "service must use the recording emitter")
	return rec
}

func formatTxHash(h evmtypes.Hash) string {
	return "0x" + hex.EncodeToString(h[:])
}

func recordedTxEvents(t *testing.T, service *EVM) []*eventspb.ChainTransactionObserved {
	t.Helper()
	return recordingEmitter(t, service).recorded()
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

	assertEvent := func(t *testing.T, ev *eventspb.ChainTransactionObserved, fixture writeReportTestFixture, txHash evmtypes.Hash, status eventspb.TxStatus) {
		t.Helper()
		require.Equal(t, fixture.metadata.WorkflowExecutionID, ev.WorkflowExecutionId)
		require.Equal(t, fixture.metadata.WorkflowID, ev.WorkflowId)
		require.Equal(t, uint64(1), ev.ChainSelector)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, status, ev.TxStatus)
		require.NotZero(t, ev.ObservedAtMs)
	}

	t.Run("prior successful transmission is the billed transaction", func(t *testing.T) {
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
		assertEvent(t, events[0], fixture, txHash, eventspb.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("prior transmission with an invalid receiver is reported when it is billed", func(t *testing.T) {
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
		assertEvent(t, events[0], fixture, txHash, eventspb.TxStatus_TX_STATUS_SUCCESS)
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("own transmission is reported once, for the billed hash", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{State: contracts.TransmissionStateNotAttempted}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		emittedBeforeBilling := -1
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, fixture.receiver, fixture.request.Report, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{TxHash: txHash, TxStatus: evmtypes.TxSuccess, TxIdempotencyKey: "key"}, nil).Once()
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Run(func(mock.Arguments) { emittedBeforeBilling = len(recordedTxEvents(t, service)) }).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded, GasLimit: big.NewInt(EnoughReceiverGas)}, nil).Once()
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		expectReceipt(t, evmServiceMock, txHash)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
		require.Equal(t, 0, emittedBeforeBilling, "the event waits until the billed transaction is known")

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		assertEvent(t, events[0], fixture, txHash, eventspb.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("duplicate transmission reports only the successful transaction that is billed", func(t *testing.T) {
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
		require.Len(t, events, 1)
		assertEvent(t, events[0], fixture, landedHash, eventspb.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("receipt lookup failure does not bill and does not emit", func(t *testing.T) {
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
		require.Empty(t, recordedTxEvents(t, service))
	})

	t.Run("request failing before billing does not emit", func(t *testing.T) {
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
		require.Empty(t, recordedTxEvents(t, service))
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
