package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

type stubForwarderClient struct {
	events []ReportProcessedEvent
	err    error
}

func (s *stubForwarderClient) InvokeOnReport(context.Context, string, *sdk.ReportResponse) (*stellartypes.SubmitTransactionResponse, error) {
	panic("not implemented")
}

func (s *stubForwarderClient) GetTransmissionInfo(context.Context, TransmissionID) (TransmissionInfo, error) {
	panic("not implemented")
}

func (s *stubForwarderClient) GetReportProcessedEvents(context.Context, TransmissionID) ([]ReportProcessedEvent, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.events, nil
}

func (s *stubForwarderClient) ForwarderAddress() string {
	return testForwarderAddress
}

func testTransmissionID() TransmissionID {
	var workflowExecutionID [32]byte
	var reportID [2]byte
	workflowExecutionID[0] = 0xAB
	reportID[0] = 0x01
	return TransmissionID{
		Receiver:            testReceiverAddress,
		WorkflowExecutionID: workflowExecutionID,
		ReportID:            reportID,
	}
}

func TestEventDetails_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "hash=abc ledger=10 result=success", eventDetails{txHash: "abc", ledger: 10, isSuccess: true}.String())
	require.Equal(t, "hash=def ledger=20 result=failed", eventDetails{txHash: "def", ledger: 20, isSuccess: false}.String())
}

func TestEventDetailsList_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "[]", eventDetailsList(nil).String())
	require.Equal(t, "[hash=a ledger=1 result=success, hash=b ledger=2 result=failed]",
		eventDetailsList{
			{txHash: "a", ledger: 1, isSuccess: true},
			{txHash: "b", ledger: 2, isSuccess: false},
		}.String(),
	)
}

func TestBuildEventDetails(t *testing.T) {
	t.Parallel()

	details := buildEventDetails([]ReportProcessedEvent{
		{TxHash: "tx1", Ledger: 10, Success: true},
		{TxHash: "tx2", Ledger: 11, Success: false},
	})
	require.Len(t, details, 2)
	require.Equal(t, "tx1", details[0].txHash)
	require.True(t, details[0].isSuccess)
	require.Equal(t, uint32(11), details[1].ledger)
	require.False(t, details[1].isSuccess)
}

func TestTxHashRetriever_GetSuccessfulTransmissionHash(t *testing.T) {
	t.Parallel()
	transmissionID := testTransmissionID()
	lggr := logger.Sugared(logger.Test(t))

	t.Run("returns successful hash", func(t *testing.T) {
		t.Parallel()
		client := &stubForwarderClient{events: []ReportProcessedEvent{
			{TxHash: "failed", Ledger: 1, Success: false},
			{TxHash: testTxHash, Ledger: 2, Success: true},
		}}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		hash, err := retriever.GetSuccessfulTransmissionHash(t.Context())
		require.NoError(t, err)
		require.Equal(t, testTxHash, hash)
	})

	t.Run("returns error when all events failed", func(t *testing.T) {
		t.Parallel()
		client := &stubForwarderClient{events: []ReportProcessedEvent{
			{TxHash: "a", Ledger: 1, Success: false},
			{TxHash: "b", Ledger: 2, Success: false},
		}}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		_, err := retriever.GetSuccessfulTransmissionHash(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no successful transmission found")
		require.Contains(t, err.Error(), "Found 2 transactions (all failed)")
	})

	t.Run("returns error when event fetch fails", func(t *testing.T) {
		t.Parallel()
		client := &stubForwarderClient{err: errors.New("rpc down")}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()

		_, err := retriever.GetSuccessfulTransmissionHash(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), failedToRetrieveTxHashErrorMsg)
	})
}

func TestTxHashRetriever_GetFailedTransmissionHash(t *testing.T) {
	t.Parallel()
	transmissionID := testTransmissionID()
	lggr := logger.Sugared(logger.Test(t))

	t.Run("returns earliest failed hash by ledger", func(t *testing.T) {
		t.Parallel()
		client := &stubForwarderClient{events: []ReportProcessedEvent{
			{TxHash: "later", Ledger: 200, Success: false},
			{TxHash: testTxHash, Ledger: 100, Success: false},
		}}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		hash, err := retriever.GetFailedTransmissionHash(t.Context())
		require.NoError(t, err)
		require.Equal(t, testTxHash, hash)
	})

	t.Run("returns unexpected successful transmission error", func(t *testing.T) {
		t.Parallel()
		client := &stubForwarderClient{events: []ReportProcessedEvent{
			{TxHash: testTxHash, Ledger: 100, Success: true},
		}}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		_, err := retriever.GetFailedTransmissionHash(t.Context())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUnexpectedSuccessfulTransmission)
	})

	t.Run("GetFailedTransmissionHashWithCount returns count", func(t *testing.T) {
		t.Parallel()
		client := &stubForwarderClient{events: []ReportProcessedEvent{
			{TxHash: "a", Ledger: 1, Success: false},
			{TxHash: "b", Ledger: 2, Success: false},
		}}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		hash, count, err := retriever.GetFailedTransmissionHashWithCount(t.Context())
		require.NoError(t, err)
		require.Equal(t, "a", hash)
		require.Equal(t, 2, count)
	})
}
