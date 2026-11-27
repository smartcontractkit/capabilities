package actions

import (
	"context"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	eventspb "github.com/smartcontractkit/chainlink-protos/workflows/go/v2"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/txevents"
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

	t.Run("already succeeded emits the billed signature", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		rec := &recordingTxEventEmitter{}
		helper.solana.WithTxEvents(rec)

		sig := solana.Signature{1, 2, 3}
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(TransmissionInfo{State: TransmissionStateSucceeded, Signature: sig}, nil)
		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: 5000},
		}, nil)

		reportMetadata := createTestReportMetadata()
		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), helper.validWriteReportReq(t, reportMetadata))
		require.NoError(t, err)
		require.Len(t, rec.events, 1)
		require.Equal(t, sig.String(), rec.events[0].TxHash)
		require.Equal(t, eventspb.TxStatus_TX_STATUS_SUCCESS, rec.events[0].TxStatus)
		require.Equal(t, eventspb.TransmissionOutcome_TRANSMISSION_OUTCOME_SUCCEEDED, rec.events[0].TransmissionOutcome)
		require.Equal(t, reportMetadata.ExecutionID, rec.events[0].WorkflowExecutionId)
	})

	t.Run("failed chain transaction is reported as reverted", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		rec := &recordingTxEventEmitter{}
		helper.solana.WithTxEvents(rec)

		sig := solana.Signature{4, 5, 6}
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(TransmissionInfo{State: TransmissionStateSucceeded, Signature: sig}, nil)
		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: 5000, Err: "InstructionError"},
		}, nil)

		reportMetadata := createTestReportMetadata()
		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), helper.validWriteReportReq(t, reportMetadata))
		require.NoError(t, err)
		require.Len(t, rec.events, 1)
		require.Equal(t, sig.String(), rec.events[0].TxHash)
		require.Equal(t, eventspb.TxStatus_TX_STATUS_REVERTED, rec.events[0].TxStatus)
	})

	t.Run("fee lookup failure does not emit", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		rec := &recordingTxEventEmitter{}
		helper.solana.WithTxEvents(rec)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(TransmissionInfo{State: TransmissionStateSucceeded, Signature: solana.Signature{7}}, nil)
		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).
			Return((*soltypes.GetTransactionReply)(nil), errors.New("rpc down"))

		reportMetadata := createTestReportMetadata()
		result, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), helper.validWriteReportReq(t, reportMetadata))
		require.NoError(t, err)
		require.Empty(t, result.ResponseMetadata.Metering)
		require.Empty(t, rec.events)
	})

	t.Run("disabled emission records nothing", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		helper.solana.WithTxEvents(txevents.NopEmitter{})

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(TransmissionInfo{State: TransmissionStateSucceeded, Signature: solana.Signature{8}}, nil)
		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: 5000},
		}, nil)

		reportMetadata := createTestReportMetadata()
		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), helper.validWriteReportReq(t, reportMetadata))
		require.NoError(t, err)
	})
}
