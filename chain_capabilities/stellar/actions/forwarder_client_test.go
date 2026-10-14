package actions

import (
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
)

func TestForwarderClient_ForwarderAddress(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)
	svc := mocks.NewStellarService(t)
	client := newForwarderClient(svc, lggr, testForwarderAddress, 100)
	require.Equal(t, testForwarderAddress, client.ForwarderAddress())
}

func TestForwarderClient_DefaultForwarderLookbackLedgers(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)

	svc := mocks.NewStellarService(t)
	svc.EXPECT().GetLatestLedger(mock.Anything).
		Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()

	client := newForwarderClient(svc, lggr, testForwarderAddress, 0)
	searchRange, err := client.GetReportProcessedEventSearchRange(t.Context())
	require.NoError(t, err)
	require.Equal(t, EventSearchRange{StartLedger: 100, EndLedger: 200}, searchRange)
}

func TestForwarderClient_ResolveSigningAccount(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)

	t.Run("signing account error", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetSigningAccount(mock.Anything).
			Return(stellartypes.GetSigningAccountResponse{}, errors.New("no account")).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.ResolveSigningAccount(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no account")
	})

	t.Run("empty signing account", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetSigningAccount(mock.Anything).
			Return(stellartypes.GetSigningAccountResponse{}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.ResolveSigningAccount(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty signing account")
	})

	t.Run("invalid signing account type", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetSigningAccount(mock.Anything).
			Return(stellartypes.GetSigningAccountResponse{AccountAddress: testReceiverAddress}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.ResolveSigningAccount(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid signing account")
	})
}

func TestForwarderClient_InvokeOnReport(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)

	t.Run("submit error", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		transmissionID := testTransmissionID()
		const maxResourceFee = uint64(100_000)
		svc.EXPECT().SubmitTransaction(mock.Anything, mock.MatchedBy(func(req stellartypes.SubmitTransactionRequest) bool {
			if len(req.Args) == 0 || req.Args[0].Address == nil || req.Args[0].Address.AccountID == nil {
				return false
			}
			transmitter, err := strkey.Encode(strkey.VersionByteAccountID, req.Args[0].Address.AccountID)
			return err == nil &&
				transmitter == testNodeAddress &&
				req.FromAddress == transmitter &&
				req.IdempotencyKey == transmissionID.idempotencyKey() &&
				req.MaxResourceFee == maxResourceFee
		})).
			Return(nil, errors.New("txm unavailable")).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)
		_, _, req := newWRReportFixture(t)

		_, err := client.InvokeOnReport(t.Context(), testNodeAddress, testReceiverAddress, req.Report, transmissionID, maxResourceFee)
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
	searchRange := EventSearchRange{StartLedger: 100, EndLedger: 200}

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		success := true
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == searchRange.StartLedger &&
				req.EndLedger == searchRange.EndLedger &&
				req.Pagination != nil &&
				req.Pagination.Limit == reportProcessedEventPageLimit
		})).
			Return(stellartypes.GetEventsResponse{
				Events: []stellartypes.EventInfo{{
					TransactionHash: testTxHash,
					Ledger:          150,
					Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
				}},
			}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		events, err := client.GetReportProcessedEvents(t.Context(), transmissionID, searchRange)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, testTxHash, events[0].TxHash)
		require.True(t, events[0].Success)
	})

	t.Run("search range clamps to ledger 1 when history is short", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{Sequence: 50}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		searchRange, err := client.GetReportProcessedEventSearchRange(t.Context())
		require.NoError(t, err)
		require.Equal(t, EventSearchRange{StartLedger: 1, EndLedger: 50}, searchRange)
	})

	t.Run("drains paginated results", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		failed := false
		success := true
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == searchRange.StartLedger &&
				req.EndLedger == searchRange.EndLedger &&
				req.Pagination != nil && req.Pagination.Cursor == ""
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{{
				TransactionHash: "failed",
				Ledger:          150,
				Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &failed},
			}},
			Cursor: "next",
		}, nil).Once()
		// Soroban getEvents rejects a ledger range combined with a cursor, so the
		// follow-up page must carry the cursor only (no StartLedger/EndLedger).
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == 0 &&
				req.EndLedger == 0 &&
				req.Pagination != nil &&
				req.Pagination.Cursor == "next"
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{{
				TransactionHash: testTxHash,
				Ledger:          151,
				Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
			}},
		}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		events, err := client.GetReportProcessedEvents(t.Context(), transmissionID, searchRange)
		require.NoError(t, err)
		require.Len(t, events, 2)
		require.False(t, events[0].Success)
		require.Equal(t, testTxHash, events[1].TxHash)
		require.True(t, events[1].Success)
	})

	// Regression: some Soroban RPCs hand back a trailing cursor that, when
	// followed, walks past EndLedger and keeps returning out-of-range events with
	// a fresh cursor (never an empty one within range). The drain must stop at the
	// first event beyond EndLedger instead of running to the page cap.
	t.Run("stops at events beyond end ledger when cursor overruns range", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		success := true
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == searchRange.StartLedger &&
				req.EndLedger == searchRange.EndLedger &&
				req.Pagination != nil && req.Pagination.Cursor == ""
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{{
				TransactionHash: testTxHash,
				Ledger:          150, // within [100, 200]
				Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
			}},
			Cursor: "next", // trailing cursor despite being the last in-range page
		}, nil).Once()
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == 0 && req.EndLedger == 0 &&
				req.Pagination != nil && req.Pagination.Cursor == "next"
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{{
				TransactionHash: "out-of-range",
				Ledger:          9999, // beyond EndLedger
				Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
			}},
			Cursor: "still-more", // never empty; must not be followed
		}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		events, err := client.GetReportProcessedEvents(t.Context(), transmissionID, searchRange)
		require.NoError(t, err)
		require.Len(t, events, 1) // only the in-range event is kept
		require.Equal(t, testTxHash, events[0].TxHash)
	})

	// Regression matching observed quickstart behavior: page 1 returns in-range
	// events with a trailing cursor; page 2 (cursor) returns the boundary event at
	// exactly EndLedger with a fresh cursor; page 3 (cursor) returns an empty page
	// that repeats the same non-empty cursor forever. The drain must stop at the
	// empty page rather than run to the page cap.
	t.Run("stops on empty cursor page that repeats a non-empty cursor", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		success := true
		boolVal := stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success}
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == searchRange.StartLedger && req.EndLedger == searchRange.EndLedger &&
				req.Pagination != nil && req.Pagination.Cursor == ""
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{{TransactionHash: "a", Ledger: 150, Value: boolVal}},
			Cursor: "c1",
		}, nil).Once()
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == 0 && req.EndLedger == 0 && req.Pagination != nil && req.Pagination.Cursor == "c1"
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{{TransactionHash: testTxHash, Ledger: searchRange.EndLedger, Value: boolVal}}, // boundary, in-range
			Cursor: "c2",
		}, nil).Once()
		svc.EXPECT().GetEvents(mock.Anything, mock.MatchedBy(func(req stellartypes.GetEventsRequest) bool {
			return req.StartLedger == 0 && req.EndLedger == 0 && req.Pagination != nil && req.Pagination.Cursor == "c2"
		})).Return(stellartypes.GetEventsResponse{
			Events: []stellartypes.EventInfo{}, // empty page, repeating cursor
			Cursor: "c2",
		}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		events, err := client.GetReportProcessedEvents(t.Context(), transmissionID, searchRange)
		require.NoError(t, err)
		require.Len(t, events, 2)
		require.Equal(t, "a", events[0].TxHash)
		require.Equal(t, testTxHash, events[1].TxHash)
	})

	t.Run("index behind requested range returns retryable error", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
			Return(stellartypes.GetEventsResponse{LatestLedger: 150}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.GetReportProcessedEvents(t.Context(), transmissionID, searchRange)
		require.Error(t, err)
		require.Contains(t, err.Error(), "event index has not reached requested range")
	})

	t.Run("empty tx hash in event", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		success := true
		svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
			Return(stellartypes.GetEventsResponse{
				Events: []stellartypes.EventInfo{{
					TransactionHash: "",
					Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
				}},
			}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.GetReportProcessedEvents(t.Context(), transmissionID, searchRange)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty tx hash")
	})

	t.Run("non-bool event value", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
			Return(stellartypes.GetEventsResponse{
				Events: []stellartypes.EventInfo{{
					TransactionHash: testTxHash,
					Value:           stellartypes.ScVal{Type: stellartypes.ScValTypeU32},
				}},
			}, nil).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.GetReportProcessedEvents(t.Context(), transmissionID, searchRange)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a bool")
	})

	t.Run("latest ledger error", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewStellarService(t)
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{}, errors.New("ledger unavailable")).Once()
		client := newForwarderClient(svc, lggr, testForwarderAddress, 100)

		_, err := client.GetReportProcessedEventSearchRange(t.Context())
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
