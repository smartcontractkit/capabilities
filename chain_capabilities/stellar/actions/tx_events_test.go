package actions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	eventspb "github.com/smartcontractkit/chainlink-protos/workflows/go/v2"
)

type recordingTxEventEmitter struct {
	events []*eventspb.ChainTransactionObserved
}

func (r *recordingTxEventEmitter) Emit(_ context.Context, ev *eventspb.ChainTransactionObserved) error {
	r.events = append(r.events, ev)
	return nil
}

func TestWriteReport_EmitsChargedTransaction(t *testing.T) {
	t.Parallel()

	t.Run("early success emits the canonical transaction", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rec := &recordingTxEventEmitter{}
		h.stellar.WithTxEvents(rec)
		rm, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()
		h.expectObservedTxHashLookup(t, rm, req.ContractId, true)

		_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Len(t, rec.events, 1)
		require.Equal(t, testTxHash, rec.events[0].TxHash)
		require.Equal(t, eventspb.TxStatus_TX_STATUS_SUCCESS, rec.events[0].TxStatus)
		require.Equal(t, eventspb.TransmissionOutcome_TRANSMISSION_OUTCOME_SUCCEEDED, rec.events[0].TransmissionOutcome)
		require.Equal(t, reqMeta.WorkflowExecutionID, rec.events[0].WorkflowExecutionId)
		require.Equal(t, testWRChainSelector, rec.events[0].ChainSelector)
	})

	t.Run("own submit emits the transaction whose fee is billed", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rec := &recordingTxEventEmitter{}
		h.stellar.WithTxEvents(rec)
		_, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t, reqMeta, req)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitRespWithFee(7_777), nil).Once()
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()
		h.expectGetTransactionWithFee(t, 9_999)

		_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Len(t, rec.events, 1)
		require.Equal(t, testTxHash, rec.events[0].TxHash)
		require.Equal(t, eventspb.TxStatus_TX_STATUS_SUCCESS, rec.events[0].TxStatus)
		require.Equal(t, eventspb.TransmissionOutcome_TRANSMISSION_OUTCOME_SUCCEEDED, rec.events[0].TransmissionOutcome)
	})

	t.Run("duplicate submit emits the canonical transaction that was charged", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rec := &recordingTxEventEmitter{}
		h.stellar.WithTxEvents(rec)
		rm, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t, reqMeta, req)

		fee := uint64(0)
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(&stellartypes.SubmitTransactionResponse{
				TxStatus:       stellartypes.TxFailed,
				TxHash:         "mytx",
				Error:          "Already processed",
				TransactionFee: &fee,
			}, nil).Once()
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()
		h.expectPostSubmitSuccessTxLookup(t, rm, req.ContractId)

		_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Len(t, rec.events, 1)
		// The canonical transaction is the one billed, not the local duplicate submit.
		require.Equal(t, "abc123txhash", rec.events[0].TxHash)
		require.Equal(t, eventspb.TxStatus_TX_STATUS_SUCCESS, rec.events[0].TxStatus)
		require.Equal(t, eventspb.TransmissionOutcome_TRANSMISSION_OUTCOME_SUCCEEDED, rec.events[0].TransmissionOutcome)
	})
}
