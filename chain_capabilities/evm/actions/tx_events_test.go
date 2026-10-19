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

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/txevents"
)

const testCapabilityID = "evm:ChainSelector:1@1.0.0"

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

func recordingEmitter(t *testing.T, service *EVM) *recordingTxEventEmitter {
	t.Helper()
	rec, ok := service.txEvents.Emitter.(*recordingTxEventEmitter)
	require.True(t, ok, "service must use the recording emitter")
	return rec
}

func recordedTxEvents(t *testing.T, service *EVM) []*txevents.ChainTransactionObserved {
	t.Helper()
	rec := recordingEmitter(t, service)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]*txevents.ChainTransactionObserved(nil), rec.events...)
}

func TestTxObservations(t *testing.T) {
	t.Parallel()
	replyHash := evmtypes.Hash(test.RandomBytes(32))
	ownHash := evmtypes.Hash(test.RandomBytes(32))

	t.Run("record merges by hash and only fills unset fields", func(t *testing.T) {
		var o txObservations
		o.record(txObservation{hash: replyHash, receiverStatus: txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, errorMessage: "invalid receiver"})
		o.record(txObservation{hash: replyHash, txStatus: txevents.TxStatus_TX_STATUS_SUCCESS, blockNumber: 7, errorMessage: "ignored"})
		o.record(txObservation{hash: replyHash, fee: big.NewInt(2000), transmitter: true})
		require.Len(t, o.list, 1)
		got := o.list[0]
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, got.txStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, got.receiverStatus)
		require.Equal(t, uint64(7), got.blockNumber)
		require.Equal(t, "invalid receiver", got.errorMessage)
		require.Equal(t, big.NewInt(2000), got.fee)
		require.True(t, got.transmitter)
		require.False(t, got.duplicate)
	})

	t.Run("recordOwnTx marks the reply transaction as sent by this node", func(t *testing.T) {
		var o txObservations
		o.record(txObservation{hash: replyHash, txStatus: txevents.TxStatus_TX_STATUS_SUCCESS})
		o.recordOwnTx(&evmtypes.TransactionResult{TxHash: replyHash, TxStatus: evmtypes.TxSuccess})
		require.Len(t, o.list, 1)
		require.True(t, o.list[0].transmitter)
		require.False(t, o.list[0].duplicate)
	})

	t.Run("recordOwnTx adds our reverted duplicate when the reply came from another transaction", func(t *testing.T) {
		var o txObservations
		o.record(txObservation{hash: replyHash, txStatus: txevents.TxStatus_TX_STATUS_SUCCESS})
		o.recordOwnTx(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxReverted})
		require.Len(t, o.list, 2)
		require.False(t, o.list[0].transmitter)
		dup := o.list[1]
		require.Equal(t, ownHash, dup.hash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, dup.txStatus)
		require.True(t, dup.transmitter)
		require.True(t, dup.duplicate)
	})

	t.Run("recordOwnTx without a reply transaction is not flagged duplicate", func(t *testing.T) {
		var o txObservations
		o.recordOwnTx(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxReverted})
		require.Len(t, o.list, 1)
		require.True(t, o.list[0].transmitter)
		require.False(t, o.list[0].duplicate)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, o.list[0].txStatus)
	})

	t.Run("recordOwnTx ignores nil, fatal and zero-hash results", func(t *testing.T) {
		var o txObservations
		o.recordOwnTx(nil)
		o.recordOwnTx(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxFatal})
		o.recordOwnTx(&evmtypes.TransactionResult{TxStatus: evmtypes.TxSuccess})
		require.Empty(t, o.list)
	})
}

func TestBuildTxEvent(t *testing.T) {
	t.Parallel()
	hash := evmtypes.Hash(test.RandomBytes(32))
	md := capabilities.RequestMetadata{
		WorkflowID:          "wf-id",
		WorkflowOwner:       "0xowner",
		OrgID:               "org-1",
		WorkflowExecutionID: "exec-1",
		WorkflowDonID:       7,
		ReferenceID:         "write-step",
	}
	now := time.UnixMilli(1_700_000_000_000)

	ev := buildTxEvent(&txObservation{
		hash:           hash,
		txStatus:       txevents.TxStatus_TX_STATUS_SUCCESS,
		receiverStatus: txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED,
		fee:            big.NewInt(2000),
		blockNumber:    99,
		errorMessage:   "receiver contract execution failure",
		transmitter:    true,
		duplicate:      false,
	}, md, 1, testCapabilityID, now)

	require.Equal(t, "exec-1", ev.WorkflowExecutionId)
	require.Equal(t, "wf-id", ev.WorkflowId)
	require.Equal(t, "0xowner", ev.WorkflowOwner)
	require.Equal(t, "write-step", ev.ReferenceId)
	require.Equal(t, uint32(7), ev.WorkflowDonId)
	require.Equal(t, "org-1", ev.OrgId)
	require.Equal(t, uint64(1), ev.ChainSelector)
	require.Equal(t, formatTxHash(hash), ev.TxHash)
	require.Len(t, ev.TxHash, 66)
	require.Equal(t, "0x", ev.TxHash[:2])
	require.Equal(t, testCapabilityID, ev.CapabilityId)
	require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
	require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, ev.ReceiverStatus)
	require.Equal(t, "2000", ev.TransactionFee)
	require.Equal(t, uint64(99), ev.BlockNumber)
	require.Equal(t, "receiver contract execution failure", ev.ErrorMessage)
	require.Equal(t, now.UnixMilli(), ev.ObservedAtMs)
	require.True(t, ev.Transmitter)
	require.False(t, ev.Duplicate)

	noFee := buildTxEvent(&txObservation{hash: hash}, md, 1, testCapabilityID, now)
	require.Empty(t, noFee.TransactionFee)
	require.Equal(t, txevents.TxStatus_TX_STATUS_UNSPECIFIED, noFee.TxStatus)
}

func TestWriteReport_EmitsChainTransactionObserved(t *testing.T) {
	t.Parallel()

	t.Run("prior successful transmission emits one success event not sent by this node", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(1234)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		fixture := newWriteReportTestFixture(t)
		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_SUCCESS, ev.ReceiverStatus)
		require.Equal(t, "2000", ev.TransactionFee)
		require.Equal(t, uint64(1234), ev.BlockNumber)
		require.Empty(t, ev.ErrorMessage)
		require.False(t, ev.Transmitter)
		require.False(t, ev.Duplicate)
		require.Equal(t, fixture.metadata.WorkflowExecutionID, ev.WorkflowExecutionId)
		require.Equal(t, fixture.metadata.WorkflowID, ev.WorkflowId)
		require.Equal(t, fixture.metadata.WorkflowOwner, ev.WorkflowOwner)
		require.Equal(t, fixture.metadata.ReferenceID, ev.ReferenceId)
		require.Equal(t, fixture.metadata.WorkflowDonID, ev.WorkflowDonId)
		require.Equal(t, uint64(1), ev.ChainSelector)
		require.Equal(t, testCapabilityID, ev.CapabilityId)
		require.NotZero(t, ev.ObservedAtMs)
	})

	t.Run("prior invalid receiver emits a receiver-reverted event with the revert reason", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{InvalidReceiver: true, State: contracts.TransmissionStateInvalidReceiver, GasLimit: big.NewInt(EnoughReceiverGas)}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectFailedTransmissionEvent(t, mockForwarderClient, txHash)
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(55)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, ev.ReceiverStatus)
		require.Equal(t, fixture.transmissionID.InvalidReceiverMessage(), ev.ErrorMessage)
		require.Equal(t, uint64(55), ev.BlockNumber)
		require.False(t, ev.Transmitter)
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("new transmission sent by this node is flagged transmitter", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{State: contracts.TransmissionStateNotAttempted}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, fixture.receiver, fixture.request.Report, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{TxHash: txHash, TxStatus: evmtypes.TxSuccess, TxIdempotencyKey: "key"}, nil).Once()
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded, GasLimit: big.NewInt(EnoughReceiverGas)}, nil).Once()
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(77)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		require.Equal(t, formatTxHash(txHash), events[0].TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, events[0].TxStatus)
		require.True(t, events[0].Transmitter)
		require.False(t, events[0].Duplicate)
	})

	runDuplicate := func(t *testing.T, transmitterOnly bool) ([]*txevents.ChainTransactionObserved, evmtypes.Hash, evmtypes.Hash) {
		t.Helper()
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		service.txEvents.TransmitterOnly = transmitterOnly
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
		receipt := evmtypes.Receipt{Status: 1, TxHash: landedHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(88)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: landedHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
		return recordedTxEvents(t, service), landedHash, ownHash
	}

	t.Run("duplicate transmission emits the landed transaction and our reverted duplicate", func(t *testing.T) {
		events, landedHash, ownHash := runDuplicate(t, false)
		require.Len(t, events, 2)

		landed := events[0]
		require.Equal(t, formatTxHash(landedHash), landed.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, landed.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_SUCCESS, landed.ReceiverStatus)
		require.Equal(t, "2000", landed.TransactionFee)
		require.False(t, landed.Transmitter)
		require.False(t, landed.Duplicate)

		dup := events[1]
		require.Equal(t, formatTxHash(ownHash), dup.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, dup.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_UNSPECIFIED, dup.ReceiverStatus)
		require.Empty(t, dup.TransactionFee)
		require.Zero(t, dup.BlockNumber)
		require.True(t, dup.Transmitter)
		require.True(t, dup.Duplicate)
		require.NotEqual(t, txevents.IdempotencyKey(landed), txevents.IdempotencyKey(dup))
	})

	t.Run("transmitter-only mode emits only transactions this node sent", func(t *testing.T) {
		events, _, ownHash := runDuplicate(t, true)
		require.Len(t, events, 1)
		require.Equal(t, formatTxHash(ownHash), events[0].TxHash)
		require.True(t, events[0].Transmitter)
		require.True(t, events[0].Duplicate)
	})

	t.Run("receipt lookup failure still emits the transaction hash", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(nil, errors.New("rpc down"))

		fixture := newWriteReportTestFixture(t)
		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.Error(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		require.Equal(t, formatTxHash(txHash), events[0].TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_UNSPECIFIED, events[0].TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_SUCCESS, events[0].ReceiverStatus)
		require.Empty(t, events[0].TransactionFee)
	})

	t.Run("emitter failure does not fail the write", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		recordingEmitter(t, service).err = errors.New("chip unreachable")
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		fixture := newWriteReportTestFixture(t)
		txResult, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
		require.Equal(t, txHash[:], txResult.Response.TxHash)
		require.Len(t, recordedTxEvents(t, service), 1)
	})

	t.Run("disabled emission leaves the write untouched", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		service.txEvents.Emitter = txevents.NopEmitter{}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		fixture := newWriteReportTestFixture(t)
		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
	})
}
