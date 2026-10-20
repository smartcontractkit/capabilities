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

func (r *recordingTxEventEmitter) recorded() []*txevents.ChainTransactionObserved {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*txevents.ChainTransactionObserved(nil), r.events...)
}

func recordingEmitter(t *testing.T, service *EVM) *recordingTxEventEmitter {
	t.Helper()
	rec, ok := service.txEvents.Emitter.(*recordingTxEventEmitter)
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
		OrgID:               "org-1",
		WorkflowExecutionID: "exec-1",
		WorkflowDonID:       7,
		ReferenceID:         "write-step",
	}
}

func newTestReporter(t *testing.T, emitter txevents.Emitter, transmitterOnly bool) *txEventReporter {
	t.Helper()
	svc := &EVM{chainSelector: 1, txEvents: TxEventsOptions{Emitter: emitter, TransmitterOnly: transmitterOnly, CapabilityID: testCapabilityID}}
	r := svc.newTxEventReporter(logger.Sugared(logger.Test(t)), testRequestMetadata())
	require.NotNil(t, r)
	return r
}

func TestTxEventReporter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ownHash := evmtypes.Hash(test.RandomBytes(32))
	landedHash := evmtypes.Hash(test.RandomBytes(32))

	t.Run("disabled emission yields a nil reporter whose methods are no-ops", func(t *testing.T) {
		lggr := logger.Sugared(logger.Test(t))
		require.Nil(t, (&EVM{}).newTxEventReporter(lggr, testRequestMetadata()))
		r := (&EVM{txEvents: TxEventsOptions{Emitter: txevents.NopEmitter{}}}).newTxEventReporter(lggr, testRequestMetadata())
		require.Nil(t, r)
		r.ownTransaction(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxSuccess})
		r.record(txObservation{hash: ownHash})
		r.resolved(ctx, txObservation{hash: ownHash})
		r.duplicateOf(ctx, landedHash)
		r.flush(ctx)
	})

	t.Run("record emits nothing and flush emits one basic event per transaction, merged, in first-seen order", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec, false)
		r.record(txObservation{hash: landedHash, txStatus: txevents.TxStatus_TX_STATUS_SUCCESS, blockNumber: 100})
		r.record(txObservation{hash: landedHash, receiverStatus: txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, errorMessage: "invalid receiver", blockNumber: 101})
		r.record(txObservation{hash: ownHash})
		require.Empty(t, rec.recorded())

		r.flush(ctx)
		r.flush(ctx)
		events := rec.recorded()
		require.Len(t, events, 2)
		first := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_BASIC, first.Detail)
		require.Equal(t, formatTxHash(landedHash), first.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, first.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, first.ReceiverStatus)
		require.Equal(t, "invalid receiver", first.ErrorMessage)
		require.Equal(t, uint64(100), first.BlockNumber, "the first recorded value wins")
		require.Empty(t, first.TransactionFee)
		require.Zero(t, first.GasUsed)
		require.False(t, first.Transmitter)
		require.Equal(t, formatTxHash(ownHash), events[1].TxHash)
	})

	t.Run("resolved emits receipt detail once, completed from the record, and suppresses the basic event", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec, false)
		r.record(txObservation{hash: landedHash, receiverStatus: txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, errorMessage: "invalid receiver"})
		r.resolved(ctx, txObservation{hash: landedHash, txStatus: txevents.TxStatus_TX_STATUS_SUCCESS, gasUsed: 1000, effectiveGasPrice: big.NewInt(2), fee: big.NewInt(2000), blockNumber: 1234})
		r.resolved(ctx, txObservation{hash: landedHash})
		r.flush(ctx)

		events := rec.recorded()
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, ev.Detail)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, ev.ReceiverStatus)
		require.Equal(t, "invalid receiver", ev.ErrorMessage)
		require.Equal(t, uint64(1000), ev.GasUsed)
		require.Equal(t, "2", ev.EffectiveGasPrice)
		require.Equal(t, "2000", ev.TransactionFee)
		require.Equal(t, uint64(1234), ev.BlockNumber)
		require.False(t, ev.Transmitter)
	})

	t.Run("ownTransaction records the transaction manager status and flags the transmitter", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec, false)
		r.ownTransaction(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxReverted, TxIdempotencyKey: "key"})
		require.Empty(t, rec.recorded())
		r.flush(ctx)

		events := rec.recorded()
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_BASIC, ev.Detail)
		require.Equal(t, formatTxHash(ownHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, ev.TxStatus)
		require.True(t, ev.Transmitter)
		require.False(t, ev.Duplicate)
	})

	t.Run("ownTransaction ignores nil, fatal and zero-hash results", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec, false)
		r.ownTransaction(nil)
		r.ownTransaction(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxFatal})
		r.ownTransaction(&evmtypes.TransactionResult{TxStatus: evmtypes.TxSuccess})
		r.duplicateOf(ctx, landedHash)
		r.flush(ctx)
		require.Empty(t, rec.recorded())
		require.Nil(t, r.ownTx)
	})

	t.Run("duplicateOf emits our transaction as a basic duplicate only when another one landed", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec, false)
		r.ownTransaction(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxReverted})
		r.duplicateOf(ctx, ownHash) // ours is the transaction that landed
		require.Empty(t, rec.recorded())

		r.duplicateOf(ctx, landedHash)
		r.resolved(ctx, txObservation{hash: ownHash, gasUsed: 21000})
		r.flush(ctx)
		events := rec.recorded()
		require.Len(t, events, 1)
		dup := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_BASIC, dup.Detail)
		require.Equal(t, formatTxHash(ownHash), dup.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, dup.TxStatus)
		require.True(t, dup.Transmitter)
		require.True(t, dup.Duplicate)
		require.Empty(t, dup.TransactionFee)
	})

	t.Run("transmitter-only mode emits only our own transaction", func(t *testing.T) {
		rec := &recordingTxEventEmitter{}
		r := newTestReporter(t, rec, true)
		r.record(txObservation{hash: landedHash})
		r.resolved(ctx, txObservation{hash: landedHash})
		r.flush(ctx)
		require.Empty(t, rec.recorded())

		r.ownTransaction(&evmtypes.TransactionResult{TxHash: ownHash, TxStatus: evmtypes.TxSuccess})
		r.flush(ctx)
		events := rec.recorded()
		require.Len(t, events, 1)
		require.Equal(t, formatTxHash(ownHash), events[0].TxHash)
		require.True(t, events[0].Transmitter)
	})

	t.Run("skips zero hashes and survives emitter errors", func(t *testing.T) {
		rec := &recordingTxEventEmitter{err: errors.New("chip unreachable")}
		r := newTestReporter(t, rec, false)
		r.record(txObservation{})
		r.resolved(ctx, txObservation{})
		r.flush(ctx)
		require.Empty(t, rec.recorded())
		r.resolved(ctx, txObservation{hash: landedHash})
		require.Len(t, rec.recorded(), 1)
	})

	t.Run("emits even after the request context is done", func(t *testing.T) {
		var gotCtx context.Context
		r := newTestReporter(t, txEventEmitterFunc(func(ctx context.Context, _ *txevents.ChainTransactionObserved) error {
			gotCtx = ctx
			return nil
		}), false)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		r.record(txObservation{hash: landedHash})
		r.flush(cancelled)
		require.NotNil(t, gotCtx)
		require.NoError(t, gotCtx.Err())
	})
}

func TestBuildTxEvent(t *testing.T) {
	t.Parallel()
	hash := evmtypes.Hash(test.RandomBytes(32))
	md := testRequestMetadata()
	now := time.UnixMilli(1_700_000_000_000)

	ev := buildTxEvent(txevents.TxDetail_TX_DETAIL_RECEIPT, txObservation{
		hash:              hash,
		txStatus:          txevents.TxStatus_TX_STATUS_SUCCESS,
		receiverStatus:    txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED,
		gasUsed:           1000,
		effectiveGasPrice: big.NewInt(2),
		fee:               big.NewInt(2000),
		blockNumber:       99,
		errorMessage:      "receiver contract execution failure",
		duplicate:         false,
	}, true, md, 1, testCapabilityID, now)

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
	require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, ev.Detail)
	require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
	require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, ev.ReceiverStatus)
	require.Equal(t, uint64(1000), ev.GasUsed)
	require.Equal(t, "2", ev.EffectiveGasPrice)
	require.Equal(t, "2000", ev.TransactionFee)
	require.Equal(t, uint64(99), ev.BlockNumber)
	require.Equal(t, "receiver contract execution failure", ev.ErrorMessage)
	require.Equal(t, now.UnixMilli(), ev.ObservedAtMs)
	require.True(t, ev.Transmitter)
	require.False(t, ev.Duplicate)

	bare := buildTxEvent(txevents.TxDetail_TX_DETAIL_BASIC, txObservation{hash: hash}, false, md, 1, testCapabilityID, now)
	require.Equal(t, txevents.TxDetail_TX_DETAIL_BASIC, bare.Detail)
	require.Empty(t, bare.TransactionFee)
	require.Empty(t, bare.EffectiveGasPrice)
	require.Zero(t, bare.GasUsed)
	require.Equal(t, txevents.TxStatus_TX_STATUS_UNSPECIFIED, bare.TxStatus)
	require.False(t, bare.Transmitter)
}

func TestWriteReport_EmitsChainTransactionObserved(t *testing.T) {
	t.Parallel()

	t.Run("prior successful transmission emits one receipt-detailed event", func(t *testing.T) {
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
		require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, ev.Detail)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_SUCCESS, ev.ReceiverStatus)
		require.Equal(t, "2000", ev.TransactionFee)
		require.Equal(t, uint64(1000), ev.GasUsed)
		require.Equal(t, "2", ev.EffectiveGasPrice)
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

	t.Run("prior invalid receiver carries the revert reason, gas and fee", func(t *testing.T) {
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
		require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, ev.Detail)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, ev.ReceiverStatus)
		require.Equal(t, fixture.transmissionID.InvalidReceiverMessage(), ev.ErrorMessage)
		require.Equal(t, uint64(55), ev.BlockNumber)
		require.Equal(t, "2000", ev.TransactionFee)
		require.Equal(t, uint64(1000), ev.GasUsed)
		require.False(t, ev.Transmitter)
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("own transmission emits one receipt-detailed event flagged transmitter", func(t *testing.T) {
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
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(77)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.NoError(t, err)
		require.Zero(t, emittedBeforeReceipt, "nothing is emitted until the receipt is resolved")

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, ev.Detail)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.True(t, ev.Transmitter)
		require.False(t, ev.Duplicate)
		require.Equal(t, "2000", ev.TransactionFee)
		require.Equal(t, uint64(1000), ev.GasUsed)
		require.Equal(t, uint64(77), ev.BlockNumber)
	})

	t.Run("own transmission whose receiver reverted carries the reason and the gas spent", func(t *testing.T) {
		ctx := t.Context()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		fixture := newWriteReportTestFixture(t)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{State: contracts.TransmissionStateNotAttempted}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, fixture.receiver, fixture.request.Report, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{TxHash: txHash, TxStatus: evmtypes.TxSuccess}, nil).Once()
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{State: contracts.TransmissionStateFailed, GasLimit: big.NewInt(EnoughReceiverGas)}, nil).Once()
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(66)}
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
		require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, ev.Detail)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED, ev.ReceiverStatus)
		require.Equal(t, UnknownIssueExecutingReceiverContractMessage, ev.ErrorMessage)
		require.Equal(t, "2000", ev.TransactionFee)
		require.Equal(t, uint64(1000), ev.GasUsed)
		require.Equal(t, uint64(66), ev.BlockNumber)
		require.True(t, ev.Transmitter)
		require.False(t, ev.Duplicate)
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

	t.Run("duplicate transmission emits our reverted duplicate with basic detail and the landed one with its receipt", func(t *testing.T) {
		events, landedHash, ownHash := runDuplicate(t, false)
		require.Len(t, events, 2)

		dup := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_BASIC, dup.Detail)
		require.Equal(t, formatTxHash(ownHash), dup.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, dup.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_UNSPECIFIED, dup.ReceiverStatus)
		require.Empty(t, dup.TransactionFee)
		require.Zero(t, dup.GasUsed)
		require.Zero(t, dup.BlockNumber)
		require.True(t, dup.Transmitter)
		require.True(t, dup.Duplicate)

		landed := events[1]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, landed.Detail)
		require.Equal(t, formatTxHash(landedHash), landed.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, landed.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_SUCCESS, landed.ReceiverStatus)
		require.Equal(t, "2000", landed.TransactionFee)
		require.Equal(t, uint64(1000), landed.GasUsed)
		require.Equal(t, uint64(88), landed.BlockNumber)
		require.False(t, landed.Transmitter)
		require.False(t, landed.Duplicate)
		require.NotEqual(t, txevents.IdempotencyKey(dup), txevents.IdempotencyKey(landed))
	})

	t.Run("transmitter-only mode emits only the transaction this node sent", func(t *testing.T) {
		events, _, ownHash := runDuplicate(t, true)
		require.Len(t, events, 1)
		require.Equal(t, formatTxHash(ownHash), events[0].TxHash)
		require.True(t, events[0].Transmitter)
		require.True(t, events[0].Duplicate)
	})

	t.Run("receipt lookup failure emits the basic event known from the log", func(t *testing.T) {
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
		ev := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_BASIC, ev.Detail)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.Equal(t, txevents.ReceiverStatus_RECEIVER_STATUS_SUCCESS, ev.ReceiverStatus)
		require.Equal(t, uint64(100), ev.BlockNumber, "block number comes from the ReportProcessed log")
		require.Empty(t, ev.TransactionFee)
		require.Zero(t, ev.GasUsed)
		require.Equal(t, fixture.metadata.WorkflowExecutionID, ev.WorkflowExecutionId)
		require.Equal(t, fixture.metadata.WorkflowID, ev.WorkflowId)
	})

	t.Run("request failing after the transaction was sent emits the basic event for it", func(t *testing.T) {
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
		ev := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_BASIC, ev.Detail)
		require.Equal(t, formatTxHash(txHash), ev.TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, ev.TxStatus)
		require.True(t, ev.Transmitter)
		require.False(t, ev.Duplicate)
		require.Empty(t, ev.TransactionFee)
	})

	t.Run("fee calculation failure still emits the receipt detail with the gas spent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, logger.Test(t))
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{Success: true, State: contracts.TransmissionStateSucceeded}, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))
		expectSuccessfulTransmissionEvent(t, mockForwarderClient, txHash)
		receipt := evmtypes.Receipt{Status: 1, TxHash: txHash, GasUsed: 1000, EffectiveGasPrice: big.NewInt(2), BlockNumber: big.NewInt(1234)}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(nil, errors.New("fee oracle down"))

		fixture := newWriteReportTestFixture(t)
		_, err := service.WriteReport(ctx, fixture.metadata, fixture.request)
		require.Error(t, err)

		events := recordedTxEvents(t, service)
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, txevents.TxDetail_TX_DETAIL_RECEIPT, ev.Detail)
		require.Equal(t, uint64(1000), ev.GasUsed)
		require.Equal(t, "2", ev.EffectiveGasPrice)
		require.Empty(t, ev.TransactionFee)
		require.Equal(t, uint64(1234), ev.BlockNumber)
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
