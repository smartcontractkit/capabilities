package actions

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

func TestForwarderClient_ForwarderAddress(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)
	svc := mocks.NewStellarService(t)
	client := newForwarderClient(svc, lggr, testForwarderAddress, 100)
	require.Equal(t, testForwarderAddress, client.ForwarderAddress())
}

func TestForwarderClient_InvokeOnReport(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)

	t.Run("signing account error", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetSigningAccount(mock.Anything).
			Return(stellartypes.GetSigningAccountResponse{}, errors.New("no account")).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.InvokeOnReport(t.Context(), testReceiverAddress, &workflowpb.ReportResponse{Sigs: wrTestSigs()})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to resolve signing account")
	})

	t.Run("empty signing account", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetSigningAccount(mock.Anything).
			Return(stellartypes.GetSigningAccountResponse{}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.InvokeOnReport(t.Context(), testReceiverAddress, &workflowpb.ReportResponse{Sigs: wrTestSigs()})
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty signing account")
	})

	t.Run("submit error", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetSigningAccount(mock.Anything).
			Return(signingAccountResp(), nil).Once()
		svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(nil, errors.New("txm unavailable")).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)
		_, _, req := newWRReportFixture(t)

		_, err := client.InvokeOnReport(t.Context(), testReceiverAddress, req.Report)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to submit forwarder report transaction")
	})
}

func TestForwarderClient_GetReportProcessedEvents(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)
	_, reqMeta, req := newWRReportFixture(t)
	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		success := true
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
		svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
			Return(stellartypes.GetEventsResponse{
				Events: []stellartypes.EventInfo{{
					TransactionHash: testTxHash,
					Ledger:          150,
					Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
				}},
			}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		events, err := client.GetReportProcessedEvents(t.Context(), transmissionID)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, testTxHash, events[0].TxHash)
		require.True(t, events[0].Success)
	})

	t.Run("start ledger clamps to 1 when history is short", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		success := false
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{Sequence: 50}, nil).Once()
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == 1
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{{
				TransactionHash: testTxHash,
				Ledger:          10,
				Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
			}},
		}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		events, err := client.GetReportProcessedEvents(t.Context(), transmissionID)
		require.NoError(t, err)
		require.Len(t, events, 1)
	})

	t.Run("empty tx hash in event", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		success := true
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
		svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
			Return(stellartypes.GetEventsResponse{
				Events: []stellartypes.EventInfo{{
					TransactionHash: "",
					Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
				}},
			}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.GetReportProcessedEvents(t.Context(), transmissionID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty tx hash")
	})

	t.Run("non-bool event value", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
		svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
			Return(stellartypes.GetEventsResponse{
				Events: []stellartypes.EventInfo{{
					TransactionHash: testTxHash,
					Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeU32},
				}},
			}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.GetReportProcessedEvents(t.Context(), transmissionID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a bool")
	})

	t.Run("latest ledger error", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{}, errors.New("ledger unavailable")).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.GetReportProcessedEvents(t.Context(), transmissionID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ledger unavailable")
	})
}

func TestTransmissionID_Helpers(t *testing.T) {
	t.Parallel()
	_, reqMeta, req := newWRReportFixture(t)
	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)

	require.NotEmpty(t, transmissionID.ReportIDHex())
	require.NotEmpty(t, transmissionID.WorkflowExecutionIDHex())
	require.Contains(t, transmissionID.InvalidReceiverMessage(), "not a Wasm contract")
	attrs := transmissionID.LogAttrs()
	require.Len(t, attrs, 6)

	key, err := transmissionID.ScheduleKey()
	require.NoError(t, err)
	require.NotEqual(t, [32]byte{}, key)

	invalid := transmissionID
	invalid.Receiver = "not-a-contract"
	_, err = invalid.ScheduleKey()
	require.Error(t, err)
}
