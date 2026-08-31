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
	events                      []ReportProcessedEvent
	eventsErr                   error
	eventsFn                    func(call int, searchRange EventSearchRange) ([]ReportProcessedEvent, error)
	eventRange                  EventSearchRange
	eventRangeFn                func(call int) (EventSearchRange, error)
	eventRangeErr               error
	eventEndLedgerFn            func(call int) (uint32, error)
	eventEndLedgerErr           error
	eventRangeCalls             int
	eventEndLedgerCalls         int
	eventRanges                 []EventSearchRange
	eventCalls                  int
	transmissionInfoFn          func(call int) (TransmissionInfo, error)
	invokeOnReportResp          *stellartypes.SubmitTransactionResponse
	invokeOnReportErr           error
	transmissionCalls           int
	simulateReportResp          stellartypes.SimulateTransactionResponse
	simulateReportSet           bool
	simulateReportErr           error
	simulateReportValidationErr error
	simulateReportCalls         int
}

func (s *stubForwarderClient) ResolveSigningAccount(context.Context) (string, error) {
	return testNodeAddress, nil
}

func (s *stubForwarderClient) InvokeOnReport(context.Context, string, string, *sdk.ReportResponse, TransmissionID, uint64) (*stellartypes.SubmitTransactionResponse, error) {
	if s.invokeOnReportErr != nil {
		return nil, s.invokeOnReportErr
	}
	if s.invokeOnReportResp != nil {
		return s.invokeOnReportResp, nil
	}
	panic("stubForwarderClient.InvokeOnReport not configured")
}

// SimulateReport defaults to a successful simulation unless configured otherwise.
func (s *stubForwarderClient) SimulateReport(context.Context, string, string, *sdk.ReportResponse) (stellartypes.SimulateTransactionResponse, error) {
	s.simulateReportCalls++
	if s.simulateReportErr != nil {
		return stellartypes.SimulateTransactionResponse{}, s.simulateReportErr
	}
	if s.simulateReportSet {
		return s.simulateReportResp, nil
	}
	return stellartypes.SimulateTransactionResponse{Success: true}, nil
}

// ValidateReportSimulation can be forced to fail by setting simulateReportValidationErr.
func (s *stubForwarderClient) ValidateReportSimulation(stellartypes.SimulateTransactionResponse, TransmissionID) error {
	return s.simulateReportValidationErr
}

func (s *stubForwarderClient) GetTransmissionInfo(context.Context, TransmissionID) (TransmissionInfo, error) {
	s.transmissionCalls++
	if s.transmissionInfoFn != nil {
		return s.transmissionInfoFn(s.transmissionCalls)
	}
	panic("stubForwarderClient.GetTransmissionInfo not configured")
}

func (s *stubForwarderClient) GetReportProcessedEventSearchRange(context.Context) (EventSearchRange, error) {
	s.eventRangeCalls++
	if s.eventRangeFn != nil {
		return s.eventRangeFn(s.eventRangeCalls)
	}
	if s.eventRangeErr != nil {
		return EventSearchRange{}, s.eventRangeErr
	}
	if s.eventRange != (EventSearchRange{}) {
		return s.eventRange, nil
	}
	return EventSearchRange{StartLedger: 1, EndLedger: 200}, nil
}

func (s *stubForwarderClient) GetReportProcessedEventSearchEndLedger(context.Context) (uint32, error) {
	s.eventEndLedgerCalls++
	if s.eventEndLedgerFn != nil {
		return s.eventEndLedgerFn(s.eventEndLedgerCalls)
	}
	if s.eventEndLedgerErr != nil {
		return 0, s.eventEndLedgerErr
	}
	if s.eventRange != (EventSearchRange{}) {
		return s.eventRange.EndLedger, nil
	}
	return 200, nil
}

func (s *stubForwarderClient) GetReportProcessedEvents(_ context.Context, _ TransmissionID, searchRange EventSearchRange) ([]ReportProcessedEvent, error) {
	s.eventCalls++
	s.eventRanges = append(s.eventRanges, searchRange)
	if s.eventsFn != nil {
		return s.eventsFn(s.eventCalls, searchRange)
	}
	if s.eventsErr != nil {
		return nil, s.eventsErr
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
	require.Equal(t, "[]", eventDetailsList{}.String())
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
		client := &stubForwarderClient{eventsErr: errors.New("rpc down")}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()

		_, err := retriever.GetSuccessfulTransmissionHash(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), failedToRetrieveTxHashErrorMsg)
	})

	t.Run("retries without moving the start ledger", func(t *testing.T) {
		t.Parallel()
		searchRange := EventSearchRange{StartLedger: 100, EndLedger: 200}
		client := &stubForwarderClient{
			eventRange: searchRange,
			eventsFn: func(call int, gotRange EventSearchRange) ([]ReportProcessedEvent, error) {
				require.Equal(t, searchRange, gotRange)
				if call == 1 {
					return nil, nil
				}
				return []ReportProcessedEvent{{TxHash: testTxHash, Ledger: 108, Success: true}}, nil
			},
		}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		hash, err := retriever.GetSuccessfulTransmissionHash(t.Context())
		require.NoError(t, err)
		require.Equal(t, testTxHash, hash)
		require.Equal(t, 1, client.eventRangeCalls)
		require.Equal(t, 1, client.eventEndLedgerCalls)
		require.Len(t, client.eventRanges, 2)
		require.Equal(t, searchRange, client.eventRanges[0])
		require.Equal(t, searchRange, client.eventRanges[1])
	})

	t.Run("expands end ledger on miss without moving start ledger", func(t *testing.T) {
		t.Parallel()
		client := &stubForwarderClient{
			eventRangeFn: func(call int) (EventSearchRange, error) {
				require.Equal(t, 1, call)
				return EventSearchRange{StartLedger: 100, EndLedger: 300}, nil
			},
			eventEndLedgerFn: func(call int) (uint32, error) {
				require.Equal(t, 1, call)
				return 304, nil
			},
			eventsFn: func(call int, gotRange EventSearchRange) ([]ReportProcessedEvent, error) {
				if call == 1 {
					require.Equal(t, EventSearchRange{StartLedger: 100, EndLedger: 300}, gotRange)
					return nil, nil
				}
				require.Equal(t, EventSearchRange{StartLedger: 100, EndLedger: 304}, gotRange)
				return []ReportProcessedEvent{{TxHash: testTxHash, Ledger: 302, Success: true}}, nil
			},
		}
		retriever := NewTxHashRetriever(client, lggr, transmissionID)

		hash, err := retriever.GetSuccessfulTransmissionHash(t.Context())
		require.NoError(t, err)
		require.Equal(t, testTxHash, hash)
		require.Equal(t, 1, client.eventRangeCalls)
		require.Equal(t, 1, client.eventEndLedgerCalls)
		require.Len(t, client.eventRanges, 2)
		require.Equal(t, EventSearchRange{StartLedger: 100, EndLedger: 300}, client.eventRanges[0])
		require.Equal(t, EventSearchRange{StartLedger: 100, EndLedger: 304}, client.eventRanges[1])
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
