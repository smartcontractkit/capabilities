package actions

import (
	"context"
	"testing"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/txevents"
)

type recordingTxEventEmitter struct {
	events []*txevents.ChainTransactionObserved
}

func (r *recordingTxEventEmitter) Emit(_ context.Context, ev *txevents.ChainTransactionObserved) error {
	r.events = append(r.events, ev)
	return nil
}

func TestWriteReport_EmitsChargedTransaction(t *testing.T) {
	t.Parallel()

	t.Run("already on chain emits the successful transaction", func(t *testing.T) {
		t.Parallel()
		h := newTestHelper(t)
		rec := &recordingTxEventEmitter{}
		h.aptos.WithTxEvents(rec)
		rm, reqMeta, req := newReportFixture(t)
		transmitter := aptos_sdk.AccountAddress{0xCC}

		h.forwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything, mock.Anything).
			Return(TransmissionInfo{Success: true, Transmitter: transmitter}, nil)
		h.mockSearchTx(t, transmitter, buildFakeTransaction(t, "0xalready", true, 100, testBlockTimestampMicro, rm))

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Len(t, rec.events, 1)
		require.Equal(t, "0xalready", rec.events[0].TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, rec.events[0].TxStatus)
		require.Equal(t, reqMeta.WorkflowExecutionID, rec.events[0].WorkflowExecutionId)
		require.Equal(t, testChainSelector, rec.events[0].ChainSelector)
	})

	t.Run("own successful submit emits that transaction", func(t *testing.T) {
		t.Parallel()
		h := newTestHelper(t)
		rec := &recordingTxEventEmitter{}
		h.aptos.WithTxEvents(rec)
		_, reqMeta, req := newReportFixture(t)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xabc"}, nil)
		h.mockTransmission(TransmissionInfo{Success: true, Transmitter: testTransmitter})
		h.mockTransactionByHash("0xabc", testGasUsed, testGasUnitPrice)

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Len(t, rec.events, 1)
		require.Equal(t, "0xabc", rec.events[0].TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, rec.events[0].TxStatus)
	})

	t.Run("duplicate submit emits the successful transaction, not the reverted one", func(t *testing.T) {
		t.Parallel()
		h := newTestHelper(t)
		rec := &recordingTxEventEmitter{}
		h.aptos.WithTxEvents(rec)
		rm, reqMeta, req := newReportFixture(t)
		transmitter := aptos_sdk.AccountAddress{0xEE}

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxReverted, TxHash: "0xreverted"}, nil)
		h.mockTransmission(TransmissionInfo{Success: true, Transmitter: transmitter})
		h.mockTransactionByHash("0xreverted", testGasUsed, testGasUnitPrice)
		h.mockSearchTx(t, transmitter, buildFakeTransaction(t, "0xreal", true, 100, testBlockTimestampMicro, rm))

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Len(t, rec.events, 1)
		require.Equal(t, "0xreal", rec.events[0].TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_SUCCESS, rec.events[0].TxStatus)
	})

	t.Run("charged failed transaction is emitted and an uncharged one is not", func(t *testing.T) {
		t.Parallel()
		h := newTestHelper(t)
		rec := &recordingTxEventEmitter{}
		h.aptos.WithTxEvents(rec)
		_, reqMeta, req := newReportFixture(t)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockTransactionByHashFailed("0xmine", testGasUsed, testGasUnitPrice, "Move abort in 0x1::coin: EINSUFFICIENT_BALANCE(0x10006)")

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Len(t, rec.events, 1)
		require.Equal(t, "0xmine", rec.events[0].TxHash)
		require.Equal(t, txevents.TxStatus_TX_STATUS_REVERTED, rec.events[0].TxStatus)
	})

	t.Run("disabled emission records nothing", func(t *testing.T) {
		t.Parallel()
		h := newTestHelper(t)
		rec := &recordingTxEventEmitter{}
		h.aptos.WithTxEvents(txevents.NopEmitter{})
		_, reqMeta, req := newReportFixture(t)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xabc"}, nil)
		h.mockTransmission(TransmissionInfo{Success: true, Transmitter: testTransmitter})
		h.mockTransactionByHash("0xabc", testGasUsed, testGasUnitPrice)

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Empty(t, rec.events)
	})
}
