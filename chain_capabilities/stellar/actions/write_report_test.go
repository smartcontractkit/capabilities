package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"google.golang.org/protobuf/proto"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	commontest "github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

// ─── constants ───────────────────────────────────────────────────────────────

const (
	testWRChainSelector = uint64(12345)

	// Valid C… StrKey Stellar contract address (56 chars).
	testForwarderAddress = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
	// Different valid C… address used as the receiver contract.
	testReceiverAddress = "CA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUWDA"
	// Valid G… StrKey Stellar account address (56 chars).
	testNodeAddress = "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7"

	testTxHash = "abc123txhash"
	testFee    = uint64(5_000) // stroops
	// Ledger close time in microseconds (1_700_000_000 unix seconds).
	testBlockTimestamp = uint64(1_700_000_000_000_000)
)

// ─── helper builders ─────────────────────────────────────────────────────────

type writeReportHelper struct {
	svc     *mocks.StellarService
	stellar *Stellar
}

// newWriteReportHelper builds a single-node Stellar under test backed by a fresh StellarService mock.
func newWriteReportHelper(t *testing.T) *writeReportHelper {
	t.Helper()
	lggr := logger.Test(t)
	mockSvc := mocks.NewStellarService(t)

	myPeerID := p2ptypes.PeerID{1}
	scheduler := ts.NewTransmissionScheduler(
		myPeerID, []p2ptypes.PeerID{myPeerID}, 100*time.Millisecond, 0, lggr)

	s := &Stellar{
		StellarService:           mockSvc,
		lggr:                     logger.Sugared(lggr),
		chainSelector:            testWRChainSelector,
		forwarderClient:          newForwarderClient(mockSvc, lggr, testForwarderAddress, 100),
		forwarderLookbackLedgers: 100,
		transmissionScheduler:    scheduler,
		messageBuilder:           monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		beholderProcessor:        nopBeholderProcessor{},
		handler:                  testConsensusHandler{handle: runVolatileHashableHandle},
	}
	require.NoError(t, s.initLimiters(limits.Factory{Logger: lggr}))
	return &writeReportHelper{svc: mockSvc, stellar: s}
}

func signingAccountResp() stellartypes.GetSigningAccountResponse {
	return stellartypes.GetSigningAccountResponse{AccountAddress: testNodeAddress}
}

func (h *writeReportHelper) expectSigningAccount(t *testing.T) {
	t.Helper()
	h.svc.EXPECT().GetSigningAccount(mock.Anything).
		Return(signingAccountResp(), nil).Once()
}

// newWRReportFixture generates a self-consistent (metadata, RequestMetadata, WriteReportRequest) triple.
func newWRReportFixture(t *testing.T) (ocrtypes.Metadata, capabilities.RequestMetadata, *stellarcap.WriteReportRequest) {
	t.Helper()
	rm := ocrtypes.Metadata{
		Version:          1,
		ExecutionID:      hex.EncodeToString(commontest.RandomBytes(32)),
		Timestamp:        1000,
		DONID:            10,
		DONConfigVersion: 2,
		WorkflowID:       hex.EncodeToString(commontest.RandomBytes(32)),
		WorkflowName:     hex.EncodeToString(commontest.RandomBytes(10)),
		WorkflowOwner:    hex.EncodeToString(commontest.RandomBytes(20)),
		ReportID:         hex.EncodeToString(commontest.RandomBytes(2)),
	}
	encoded, err := rm.Encode()
	require.NoError(t, err)

	reqMeta := capabilities.RequestMetadata{
		WorkflowID:               rm.WorkflowID,
		WorkflowOwner:            rm.WorkflowOwner,
		WorkflowName:             rm.WorkflowName,
		WorkflowDonID:            rm.DONID,
		WorkflowDonConfigVersion: rm.DONConfigVersion,
		WorkflowExecutionID:      rm.ExecutionID,
	}
	req := &stellarcap.WriteReportRequest{
		ContractId: testReceiverAddress,
		Report: &workflowpb.ReportResponse{
			RawReport:     encoded,
			ReportContext: make([]byte, 96),
			Sigs:          wrTestSigs(),
		},
	}
	return rm, reqMeta, req
}

func wrTestSigs() []*workflowpb.AttributedSignature {
	sig := make([]byte, ocrSignatureLen)
	sig[0] = 0xAB
	return []*workflowpb.AttributedSignature{{Signature: sig}, {Signature: sig}}
}

// ─── XDR helpers ─────────────────────────────────────────────────────────────

// buildTransmissionInfoXDR returns base64-encoded XDR for the TransmissionInfo struct
// returned by get_transmission_info: { state: u32, transmitter: Option<Address> }.
func buildTransmissionInfoXDR(t *testing.T, state TransmissionState) string {
	t.Helper()
	return marshalTransmissionInfoXDR(t, state, nil)
}

func marshalTransmissionInfoXDR(t *testing.T, state TransmissionState, transmitter *string) string {
	t.Helper()
	stateU32 := xdr.Uint32(state)
	stateVal := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &stateU32}
	stateSym := xdr.ScSymbol("state")
	stateKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &stateSym}

	var txrVal xdr.ScVal
	if transmitter == nil {
		txrVal = xdr.ScVal{Type: xdr.ScValTypeScvVoid}
	} else {
		accountID := xdr.MustAddress(*transmitter)
		txrVal = xdr.ScVal{
			Type: xdr.ScValTypeScvAddress,
			Address: &xdr.ScAddress{
				Type:      xdr.ScAddressTypeScAddressTypeAccount,
				AccountId: &accountID,
			},
		}
	}
	txrSym := xdr.ScSymbol("transmitter")
	txrKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &txrSym}

	scMap := xdr.ScMap{
		{Key: stateKey, Val: stateVal},
		{Key: txrKey, Val: txrVal},
	}
	mapPtr := &scMap
	sv := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr}
	b64, err := xdr.MarshalBase64(sv)
	require.NoError(t, err, "XDR encode transmission info struct")
	return b64
}

// Convenience wrappers for common transmission states.
func notAttemptedXDR(t *testing.T) string {
	return buildTransmissionInfoXDR(t, TransmissionStateNotAttempted)
}

func succeededXDR(t *testing.T) string {
	return buildTransmissionInfoXDR(t, TransmissionStateSucceeded)
}

func invalidReceiverXDR(t *testing.T) string {
	return buildTransmissionInfoXDR(t, TransmissionStateInvalidReceiver)
}

func failedXDR(t *testing.T) string {
	return buildTransmissionInfoXDR(t, TransmissionStateFailed)
}

func transmissionResp(xdrResult string) stellartypes.SimulateTransactionResponse {
	return stellartypes.SimulateTransactionResponse{Success: true, ReturnValueXDR: xdrResult, LedgerSequence: 100}
}

func successSubmitResp() *stellartypes.SubmitTransactionResponse {
	fee := testFee
	ts := testBlockTimestamp
	return &stellartypes.SubmitTransactionResponse{
		TxStatus:       stellartypes.TxSuccess,
		TxHash:         testTxHash,
		TransactionFee: &fee,
		BlockTimestamp: &ts,
	}
}

func reportProcessedEventsForFixture(t *testing.T, rm ocrtypes.Metadata, receiver string, success bool) stellartypes.GetEventsResponse {
	t.Helper()
	execID, err := hex.DecodeString(rm.ExecutionID)
	require.NoError(t, err)
	require.Len(t, execID, 32)
	reportID, err := hex.DecodeString(rm.ReportID)
	require.NoError(t, err)
	require.Len(t, reportID, 2)

	eventName := reportProcessedTopicPrefix
	receiverVal, err := contractAddressToScVal(receiver)
	require.NoError(t, err)

	return stellartypes.GetEventsResponse{
		Events: []stellartypes.EventInfo{{
			Ledger:          100,
			TransactionHash: testTxHash,
			Topics: []stellartypes.ScVal{
				{Type: stellartypes.ScValTypeSymbol, Symbol: &eventName},
				receiverVal,
				{Type: stellartypes.ScValTypeBytes, Bytes: execID},
				{Type: stellartypes.ScValTypeBytes, Bytes: reportID},
			},
			Value: stellartypes.ScVal{Type: stellartypes.ScValTypeBool, Bool: &success},
		}},
	}
}

func (h *writeReportHelper) expectGetTransaction(t *testing.T) {
	t.Helper()
	h.svc.EXPECT().GetTransaction(mock.Anything, stellartypes.GetTransactionRequest{TxHash: testTxHash}).
		Return(stellartypes.GetTransactionResponse{
			FeeStroops:      testFee,
			LedgerSequence:  100,
			LedgerCloseTime: int64(testBlockTimestamp / 1_000_000),
		}, nil).Once()
}

func (h *writeReportHelper) expectObservedTxHashLookup(t *testing.T, rm ocrtypes.Metadata, receiver string, eventSuccess bool) {
	t.Helper()
	h.svc.EXPECT().GetLatestLedger(mock.Anything).
		Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
	h.svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
		Return(reportProcessedEventsForFixture(t, rm, receiver, eventSuccess), nil).Once()
	h.expectGetTransaction(t)
}

func (h *writeReportHelper) expectPostSubmitSuccessTxLookup(t *testing.T, rm ocrtypes.Metadata, receiver string) {
	t.Helper()
	h.expectObservedTxHashLookup(t, rm, receiver, true)
}

func (h *writeReportHelper) expectPostSubmitFailedTxLookup(t *testing.T, rm ocrtypes.Metadata, receiver string) {
	t.Helper()
	h.expectObservedTxHashLookup(t, rm, receiver, false)
}

// expectEventTxHashLookupUnavailable makes GetSuccessfulTransmissionHash fail so poll-timeout
// paths can fall back to the local TXM submit response.
func (h *writeReportHelper) expectEventTxHashLookupUnavailable(t *testing.T) {
	t.Helper()
	h.svc.EXPECT().GetLatestLedger(mock.Anything).
		Return(stellartypes.GetLatestLedgerResponse{}, errors.New("events unavailable")).
		Maybe()
}

func requireReplyBlockTimestamp(t *testing.T, reply *stellarcap.WriteReportReply, expected uint64) {
	t.Helper()
	require.NotNil(t, reply.BlockTimestamp)
	require.Equal(t, expected, *reply.BlockTimestamp)
}

// ─── metering assertion ───────────────────────────────────────────────────────

func validateWRMetering(t *testing.T, meta capabilities.ResponseMetadata, chainSelector uint64, expectedStroops uint64) {
	t.Helper()
	require.Len(t, meta.Metering, 1)
	m := meta.Metering[0]
	require.Equal(t, fmt.Sprintf(metering.WriteReportSpendUnitFormat, chainSelector), m.SpendUnit)
	require.Equal(t, fmt.Sprintf("%d", expectedStroops), m.SpendValue)
	require.Empty(t, m.Peer2PeerID)
}

// ─── Validation tests ─────────────────────────────────────────────────────────

func TestWriteReport_Validation(t *testing.T) {
	t.Parallel()

	t.Run("nil request", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, err := h.stellar.WriteReport(t.Context(), capabilities.RequestMetadata{}, nil)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "nil WriteReportRequest")
	})

	t.Run("nil report", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, err := h.stellar.WriteReport(t.Context(), capabilities.RequestMetadata{},
			&stellarcap.WriteReportRequest{})
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "nil SignedReport")
	})

	t.Run("empty contract_id", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, err := h.stellar.WriteReport(t.Context(), capabilities.RequestMetadata{},
			&stellarcap.WriteReportRequest{Report: &workflowpb.ReportResponse{Sigs: wrTestSigs()}})
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "contractId is required")
	})

	t.Run("invalid contract_id (G… account, not C… contract)", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, err := h.stellar.WriteReport(t.Context(), capabilities.RequestMetadata{},
			&stellarcap.WriteReportRequest{
				ContractId: testNodeAddress, // G… key — not a contract
				Report:     &workflowpb.ReportResponse{Sigs: wrTestSigs()},
			})
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "invalid receiver contract address")
	})

	t.Run("no signatures", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		req.Report.Sigs = nil

		_, err := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "signed report must contain at least one signature")
	})

	t.Run("invalid signature length", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		req.Report.Sigs = []*workflowpb.AttributedSignature{{Signature: make([]byte, 32)}}

		_, err := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "signature 0 has invalid length")
		require.Contains(t, err.Error(), "want 65")
	})

	t.Run("report metadata cannot be decoded", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, _ := newWRReportFixture(t)
		req := &stellarcap.WriteReportRequest{
			ContractId: testReceiverAddress,
			Report:     &workflowpb.ReportResponse{RawReport: []byte("garbage"), Sigs: wrTestSigs()},
		}

		_, err := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "failed to decode report metadata")
	})

	t.Run("WorkflowExecutionID mismatch", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		reqMeta.WorkflowExecutionID = hex.EncodeToString(commontest.RandomBytes(32))

		_, err := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "workflowExecutionID does not match")
	})

	t.Run("WorkflowOwner mismatch", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		reqMeta.WorkflowOwner = hex.EncodeToString(commontest.RandomBytes(20))

		_, err := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "workflowOwner does not match")
	})

	t.Run("WorkflowName mismatch", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		reqMeta.WorkflowName = "totally-different-name"

		_, err := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "workflowName does not match")
	})

	t.Run("WorkflowID mismatch", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		reqMeta.WorkflowID = hex.EncodeToString(commontest.RandomBytes(32))

		_, err := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "workflowID does not match")
	})

	t.Run("report size exceeds limit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rm, reqMeta, _ := newWRReportFixture(t)
		encoded, err2 := rm.Encode()
		require.NoError(t, err2)

		// Pre-submit poll: NotAttempted so code reaches the size check.
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()

		req := &stellarcap.WriteReportRequest{
			ContractId: testReceiverAddress,
			Report: &workflowpb.ReportResponse{
				RawReport:     append(encoded, make([]byte, 20_000)...),
				ReportContext: make([]byte, 96),
				Sigs:          wrTestSigs(),
			},
		}

		_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "report size exceeds limit")
	})
}

// ─── Early-return (observed-state before submit) tests ───────────────────────

func TestWriteReport_EarlyReturn(t *testing.T) {
	t.Parallel()

	t.Run("already succeeded - returns success with no submit and no metering", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rm, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()
		h.expectObservedTxHashLookup(t, rm, req.ContractId, true)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		rcSuccess := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		require.Equal(t, &rcSuccess, result.Response.ReceiverContractExecutionStatus)
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testFee, *result.Response.TransactionFee)
		requireReplyBlockTimestamp(t, result.Response, testBlockTimestamp)
		// No billing metering: this node observed, not submitted.
		require.Empty(t, result.ResponseMetadata.Metering)
		h.svc.AssertNotCalled(t, "SubmitTransaction", mock.Anything, mock.Anything)
	})

	t.Run("already failed - InvalidReceiver - terminal error message, no submit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rm, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(invalidReceiverXDR(t)), nil).Once()
		h.expectObservedTxHashLookup(t, rm, req.ContractId, false)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		rcReverted := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
		require.Equal(t, &rcReverted, result.Response.ReceiverContractExecutionStatus)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "not a Wasm contract or missing on_report")
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		requireReplyBlockTimestamp(t, result.Response, testBlockTimestamp)
		require.Empty(t, result.ResponseMetadata.Metering)
		h.svc.AssertNotCalled(t, "SubmitTransaction", mock.Anything, mock.Anything)
	})

	t.Run("already failed - receiver revert - error message, no submit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rm, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(failedXDR(t)), nil).Once()
		h.expectObservedTxHashLookup(t, rm, req.ContractId, false)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "receiver contract execution failed")
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		requireReplyBlockTimestamp(t, result.Response, testBlockTimestamp)
		require.Empty(t, result.ResponseMetadata.Metering)
		h.svc.AssertNotCalled(t, "SubmitTransaction", mock.Anything, mock.Anything)
	})
}

// ─── Happy-path and submit tests ─────────────────────────────────────────────

func TestWriteReport_HappyPath(t *testing.T) {
	t.Parallel()

	t.Run("fresh submit succeeds - reply contains hash, fee and metering", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rm, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		// Call 1: pre-submit get_transmission_info → NotAttempted
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		// TXM submit → success with fee
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		// Call 3: post-submit get_transmission_info → Succeeded
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()
		h.expectPostSubmitSuccessTxLookup(t, rm, req.ContractId)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		rcSuccess := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		require.Equal(t, &rcSuccess, result.Response.ReceiverContractExecutionStatus)
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testFee, *result.Response.TransactionFee)
		requireReplyBlockTimestamp(t, result.Response, testBlockTimestamp)
		// Billing metering is populated because this node submitted.
		validateWRMetering(t, result.ResponseMetadata, testWRChainSelector, testFee)
	})
}

// ─── Submit-path error and edge-case tests ────────────────────────────────────

func TestWriteReport_Submit(t *testing.T) {
	t.Parallel()

	t.Run("SubmitTransaction RPC fails - error propagated", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(nil, errors.New("TXM: context deadline exceeded")).Once()

		_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "failed to submit forwarder report transaction")
	})

	t.Run("post-submit poll times out - falls back to TXM reply", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		// Post-submit poll always returns NotAttempted → times out → TXM fallback.
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil)
		h.expectEventTxHashLookupUnavailable(t)

		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(400*time.Millisecond))
		defer cancel()

		result, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
		require.Nil(t, capErr)
		// Reply comes from the TXM submit response.
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testFee, *result.Response.TransactionFee)
		requireReplyBlockTimestamp(t, result.Response, testBlockTimestamp)
		validateWRMetering(t, result.ResponseMetadata, testWRChainSelector, testFee)
	})

	t.Run("post-submit shows InvalidReceiver - reply with error message and canonical tx hash", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rm, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(invalidReceiverXDR(t)), nil).Once()
		h.expectPostSubmitFailedTxLookup(t, rm, req.ContractId)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		// hash and fee come from the canonical failed ReportProcessed event, not local TXM data.
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testFee, *result.Response.TransactionFee)
		requireReplyBlockTimestamp(t, result.Response, testBlockTimestamp)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "not a Wasm contract or missing on_report")
		// This node spent gas → billing metering is populated.
		validateWRMetering(t, result.ResponseMetadata, testWRChainSelector, testFee)
	})

	t.Run("post-submit shows Failed - receiver revert - error message and canonical tx hash", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		rm, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(failedXDR(t)), nil).Once()
		h.expectPostSubmitFailedTxLookup(t, rm, req.ContractId)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "receiver contract execution failed")
		validateWRMetering(t, result.ResponseMetadata, testWRChainSelector, testFee)
	})

	t.Run("own TxFailed - on-chain error string appears in ErrorMessage", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		fee := uint64(0)
		failedResp := &stellartypes.SubmitTransactionResponse{
			TxStatus:       stellartypes.TxFailed,
			TxHash:         testTxHash,
			Error:          "transaction result: InvokeHostFunctionTrapped",
			TransactionFee: &fee,
		}

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(failedResp, nil).Once()
		// Post-submit poll stays NotAttempted → context deadline triggers TXM fallback.
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil)
		h.expectEventTxHashLookupUnavailable(t)

		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(400*time.Millisecond))
		defer cancel()

		result, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
		require.Nil(t, capErr)
		// replyFromOwnTransaction maps TxFailed to REVERTED.
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "on-chain transaction failed")
		require.Contains(t, *result.Response.ErrorMessage, "InvokeHostFunctionTrapped")
	})

	t.Run("submit superseded by prior success - post-submit succeeds", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		processor := h.withRecordingProcessor()
		rm, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		fee := uint64(0)
		// Our node's submission "fails" (another node already succeeded), but
		// the post-submit poll shows Succeeded.
		myResp := &stellartypes.SubmitTransactionResponse{
			TxStatus:       stellartypes.TxFailed,
			TxHash:         "mytx",
			Error:          "Already processed",
			TransactionFee: &fee,
		}

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(myResp, nil).Once()
		// Post-submit: another node already succeeded.
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()
		h.expectPostSubmitSuccessTxLookup(t, rm, req.ContractId)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		// Reply must use the successful on-chain tx from events, not this node's reverted submit.
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		require.NotEqual(t, "mytx", *result.Response.TxHash)
		requireDuplicateTxTelemetry(t, processor.messages, "mytx", testTxHash)
	})

	t.Run("submit superseded by prior invalid receiver - post-submit returns canonical failed hash", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		processor := h.withRecordingProcessor()
		rm, reqMeta, req := newWRReportFixture(t)
		h.expectSigningAccount(t)

		myResp := &stellartypes.SubmitTransactionResponse{
			TxStatus: stellartypes.TxFailed,
			TxHash:   "mytx",
			Error:    "Already processed",
		}

		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(myResp, nil).Once()
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(transmissionResp(invalidReceiverXDR(t)), nil).Once()
		h.expectPostSubmitFailedTxLookup(t, rm, req.ContractId)

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		require.NotNil(t, result.Response.TxHash)
		require.Equal(t, testTxHash, *result.Response.TxHash)
		require.NotEqual(t, "mytx", *result.Response.TxHash)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "not a Wasm contract or missing on_report")
		requireDuplicateTxTelemetry(t, processor.messages, "mytx", testTxHash)
	})
}

// ─── Transmission ID tests ────────────────────────────────────────────────────

func TestGetTransmissionID_Determinism(t *testing.T) {
	t.Parallel()

	_, _, req := newWRReportFixture(t)
	execID := hex.EncodeToString(commontest.RandomBytes(32))

	id1, err1 := getTransmissionID(execID, req)
	id2, err2 := getTransmissionID(execID, req)
	require.NoError(t, err1)
	require.NoError(t, err2)
	require.Equal(t, id1, id2, "transmission ID must be deterministic for identical inputs")
}

func TestGetTransmissionID_DifferentInputsDifferentIDs(t *testing.T) {
	t.Parallel()

	_, _, req1 := newWRReportFixture(t)
	_, _, req2 := newWRReportFixture(t)
	execID := hex.EncodeToString(commontest.RandomBytes(32))

	id1, err1 := getTransmissionID(execID, req1)
	id2, err2 := getTransmissionID(execID, req2)
	require.NoError(t, err1)
	require.NoError(t, err2)
	key1, err := id1.ScheduleKey()
	require.NoError(t, err)
	key2, err := id2.ScheduleKey()
	require.NoError(t, err)
	require.NotEqual(t, key1, key2, "different receivers must produce different schedule keys")
}

func TestGetTransmissionID_InvalidReceiver(t *testing.T) {
	t.Parallel()

	// Build a fixture so RawReport and execID are valid; only ContractId is wrong.
	rm, reqMeta, req := newWRReportFixture(t)
	_ = rm
	req.ContractId = testNodeAddress // G… StrKey — not a contract address

	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)
	_, err = transmissionID.ScheduleKey()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid receiver contract address")
}

func TestWriteReport_NoSigningAccount(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	_, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
	h.svc.EXPECT().GetSigningAccount(mock.Anything).
		Return(stellartypes.GetSigningAccountResponse{}, errors.New("keystore has no accounts")).Once()

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.Contains(t, capErr.Error(), "failed to resolve signing account")
}

func TestWriteReport_UnsupportedReportMetadataVersion(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	rm, reqMeta, req := newWRReportFixture(t)
	rm.Version = 2
	encoded, err := rm.Encode()
	require.NoError(t, err)
	req.Report.RawReport = encoded

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.Contains(t, capErr.Error(), "unsupported report metadata version")
}

func TestGetTransmissionInfo(t *testing.T) {
	t.Parallel()

	newWR := func(t *testing.T) (*writeReportHelper, CREForwarderClient, TransmissionID) {
		t.Helper()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
		require.NoError(t, err)
		return h, h.stellar.forwarderClient, transmissionID
	}

	t.Run("empty result treated as not attempted", func(t *testing.T) {
		t.Parallel()
		h, fc, transmissionID := newWR(t)
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(stellartypes.SimulateTransactionResponse{}, nil).Once()

		info, err := fc.GetTransmissionInfo(t.Context(), transmissionID)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateNotAttempted, info.State)
	})

	t.Run("forwarder simulation error is propagated", func(t *testing.T) {
		t.Parallel()
		h, fc, transmissionID := newWR(t)
		h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
			Return(stellartypes.SimulateTransactionResponse{Error: "contract trap"}, nil).Once()

		_, err := fc.GetTransmissionInfo(t.Context(), transmissionID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "forwarder simulation failed")
	})
}

func newPollTransmissionInfoHarness(t *testing.T, deltaStage time.Duration) (
	*writeReport,
	*mocks.StellarService,
	TransmissionID,
	*stellarcap.WriteReportRequest,
) {
	t.Helper()
	lggr := logger.Test(t)
	mockSvc := mocks.NewStellarService(t)
	myPeerID := p2ptypes.PeerID{1}
	scheduler := ts.NewTransmissionScheduler(
		myPeerID,
		[]p2ptypes.PeerID{{1}, {2}, {3}},
		deltaStage,
		0,
		lggr,
	)
	wr := &writeReport{
		service:               mockSvc,
		forwarderClient:       newForwarderClient(mockSvc, lggr, testForwarderAddress, 100),
		lggr:                  logger.Sugared(lggr),
		transmissionScheduler: scheduler,
	}
	_, reqMeta, req := newWRReportFixture(t)
	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)
	return wr, mockSvc, transmissionID, req
}

func expectTransmissionInfoPoll(mockSvc *mocks.StellarService, xdrResult string, err error) {
	mockSvc.EXPECT().
		SimulateTransaction(mock.Anything, mock.MatchedBy(func(req stellartypes.SimulateTransactionRequest) bool {
			return req.Function == forwarderGetTransmissionInfoFunction
		})).
		Return(transmissionResp(xdrResult), err).
		Once()
}

func expectTransmissionInfoPollMaybe(mockSvc *mocks.StellarService, xdrResult string, err error) {
	mockSvc.EXPECT().
		SimulateTransaction(mock.Anything, mock.MatchedBy(func(req stellartypes.SimulateTransactionRequest) bool {
			return req.Function == forwarderGetTransmissionInfoFunction
		})).
		Return(transmissionResp(xdrResult), err).
		Maybe()
}

func TestPollTransmissionInfo_QueuePositionScenarios(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("terminal states return immediately without waiting for delta stage", func(t *testing.T) {
		cases := []struct {
			name  string
			state TransmissionState
			xdr   func(t *testing.T) string
		}{
			{"succeeded", TransmissionStateSucceeded, succeededXDR},
			{"invalid receiver", TransmissionStateInvalidReceiver, invalidReceiverXDR},
			{"failed", TransmissionStateFailed, failedXDR},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				for _, queuePosition := range []int{1, 2, 3} {
					t.Run("queue position "+strconv.Itoa(queuePosition), func(t *testing.T) {
						t.Parallel()
						wr, mockSvc, transmissionID, req := newPollTransmissionInfoHarness(t, 5*time.Second)
						expectTransmissionInfoPoll(mockSvc, tc.xdr(t), nil)

						start := time.Now()
						info, err := wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, queuePosition)
						require.NoError(t, err)
						require.Equal(t, tc.state, info.State)
						require.Less(t, time.Since(start), 500*time.Millisecond)
					})
				}
			})
		}
	})

	t.Run("not attempted waits until delta stage then returns", func(t *testing.T) {
		t.Parallel()
		const queuePosition = 2
		wr, mockSvc, transmissionID, req := newPollTransmissionInfoHarness(t, 150*time.Millisecond)
		expectTransmissionInfoPollMaybe(mockSvc, notAttemptedXDR(t), nil)

		start := time.Now()
		info, err := wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, queuePosition)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateNotAttempted, info.State)
		require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	})

	t.Run("position zero uses quick retry", func(t *testing.T) {
		t.Parallel()
		wr, mockSvc, transmissionID, req := newPollTransmissionInfoHarness(t, 5*time.Second)
		expectTransmissionInfoPoll(mockSvc, succeededXDR(t), nil)

		info, err := wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, 0)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateSucceeded, info.State)
	})
}

func TestPollTransmissionInfo_RaceConditions(t *testing.T) {
	t.Parallel()

	t.Run("timer boundary read catches late success", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		wr, mockSvc, transmissionID, req := newPollTransmissionInfoHarness(t, 150*time.Millisecond)
		var chainStateUpdated atomic.Bool
		go func() {
			time.Sleep(120 * time.Millisecond)
			chainStateUpdated.Store(true)
		}()

		mockSvc.EXPECT().
			SimulateTransaction(mock.Anything, mock.MatchedBy(func(req stellartypes.SimulateTransactionRequest) bool {
				return req.Function == forwarderGetTransmissionInfoFunction
			})).
			RunAndReturn(func(context.Context, stellartypes.SimulateTransactionRequest) (stellartypes.SimulateTransactionResponse, error) {
				if chainStateUpdated.Load() {
					return transmissionResp(succeededXDR(t)), nil
				}
				return transmissionResp(notAttemptedXDR(t)), nil
			}).
			Maybe()

		info, err := wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, 1)
		require.NoError(t, err)
		require.True(t, chainStateUpdated.Load())
		require.Equal(t, TransmissionStateSucceeded, info.State)
	})

	t.Run("all rpc errors including boundary read return error", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		wr, mockSvc, transmissionID, req := newPollTransmissionInfoHarness(t, 50*time.Millisecond)
		var rpcCalls atomic.Int64
		mockSvc.EXPECT().
			SimulateTransaction(mock.Anything, mock.MatchedBy(func(req stellartypes.SimulateTransactionRequest) bool {
				return req.Function == forwarderGetTransmissionInfoFunction
			})).
			RunAndReturn(func(context.Context, stellartypes.SimulateTransactionRequest) (stellartypes.SimulateTransactionResponse, error) {
				rpcCalls.Add(1)
				return stellartypes.SimulateTransactionResponse{}, errors.New("rpc unavailable")
			}).
			Maybe()

		_, err := wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, 2)
		require.Greater(t, rpcCalls.Load(), int64(0))
		require.Error(t, err)
		require.Contains(t, err.Error(), "all GetTransmissionInfo polls failed during delta stage window")
	})

	t.Run("context cancel returns timeout error", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		wr, mockSvc, transmissionID, req := newPollTransmissionInfoHarness(t, 5*time.Second)
		expectTransmissionInfoPollMaybe(mockSvc, notAttemptedXDR(t), nil)

		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		_, err := wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, 2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "timed out waiting for transmission info")
	})
}

func TestInvalidTransmissionStateError(t *testing.T) {
	t.Parallel()
	err := invalidTransmissionStateError(TransmissionState(99))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected transmission state: 99")
}

func TestReplyFromOwnTransaction(t *testing.T) {
	t.Parallel()
	wr := &writeReport{lggr: logger.Sugared(logger.Test(t))}

	t.Run("nil response is fatal", func(t *testing.T) {
		t.Parallel()
		reply := wr.replyFromOwnTransaction(nil)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_FATAL, reply.TxStatus)
	})

	t.Run("tx fatal maps to fatal status", func(t *testing.T) {
		t.Parallel()
		reply := wr.replyFromOwnTransaction(&stellartypes.SubmitTransactionResponse{
			TxStatus: stellartypes.TxFatal,
			TxHash:   testTxHash,
		})
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_FATAL, reply.TxStatus)
		require.NotNil(t, reply.TxHash)
	})
}

func TestWriteReport_TxFatalSubmitFallback(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	_, reqMeta, req := newWRReportFixture(t)
	h.expectSigningAccount(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
	h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
		Return(&stellartypes.SubmitTransactionResponse{
			TxStatus: stellartypes.TxFatal,
			TxHash:   testTxHash,
		}, nil).Once()
	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil)
	h.expectEventTxHashLookupUnavailable(t)

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(400*time.Millisecond))
	defer cancel()

	result, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
	require.Nil(t, capErr)
	require.Equal(t, stellarcap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
	require.NotNil(t, result.Response.TxHash)
}

func TestReplyBuilders(t *testing.T) {
	t.Parallel()
	_, reqMeta, req := newWRReportFixture(t)
	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)

	t.Run("buildSuccessReply", func(t *testing.T) {
		t.Parallel()
		mockSvc := mocks.NewStellarService(t)
		wr := &writeReport{service: mockSvc, lggr: logger.Sugared(logger.Test(t))}
		mockSvc.EXPECT().GetTransaction(mock.Anything, stellartypes.GetTransactionRequest{TxHash: testTxHash}).
			Return(stellartypes.GetTransactionResponse{
				FeeStroops:      testFee,
				LedgerSequence:  100,
				LedgerCloseTime: int64(testBlockTimestamp / 1_000_000),
			}, nil).Once()

		reply, err := wr.buildSuccessReply(t.Context(), req, monitoring.TelemetryContext{}, testTxHash)
		require.NoError(t, err)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, reply.TxStatus)
		require.Equal(t, testTxHash, *reply.TxHash)
	})

	t.Run("buildRevertReplyFromTx invalid receiver", func(t *testing.T) {
		t.Parallel()
		mockSvc := mocks.NewStellarService(t)
		wr := &writeReport{service: mockSvc, lggr: logger.Sugared(logger.Test(t))}
		mockSvc.EXPECT().GetTransaction(mock.Anything, stellartypes.GetTransactionRequest{TxHash: testTxHash}).
			Return(stellartypes.GetTransactionResponse{FeeStroops: testFee}, nil).Once()

		reply, err := wr.buildRevertReplyFromTx(t.Context(), req, monitoring.TelemetryContext{}, testTxHash, TransmissionInfo{State: TransmissionStateInvalidReceiver}, transmissionID)
		require.NoError(t, err)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, reply.TxStatus)
		require.Contains(t, *reply.ErrorMessage, "not a Wasm contract")
	})

	t.Run("revertReplyBuildError", func(t *testing.T) {
		t.Parallel()
		buildErr := revertReplyBuildError(
			TransmissionInfo{State: TransmissionStateFailed},
			transmissionID,
			errors.New("rpc down"),
		)
		require.Error(t, buildErr)
		require.Contains(t, buildErr.Error(), unknownIssueExecutingReceiverContractMessage)
	})

	t.Run("meteringFromReply nil cases", func(t *testing.T) {
		t.Parallel()
		wr := &writeReport{lggr: logger.Sugared(logger.Test(t))}
		require.Empty(t, wr.meteringFromReply(nil).Metering)
		require.Empty(t, wr.meteringFromReply(&stellarcap.WriteReportReply{}).Metering)
	})
}

func TestPopulateReplyFromSubmit(t *testing.T) {
	t.Parallel()
	reply := &stellarcap.WriteReportReply{}
	populateReplyFromSubmit(reply, nil)
	require.Nil(t, reply.TxHash)

	fee := testFee
	blockTs := testBlockTimestamp
	populateReplyFromSubmit(reply, &stellartypes.SubmitTransactionResponse{
		TxHash:         testTxHash,
		TransactionFee: &fee,
		BlockTimestamp: &blockTs,
	})
	require.Equal(t, testTxHash, *reply.TxHash)
	require.Equal(t, testFee, *reply.TransactionFee)
}

func TestWriteReport_ObservedRevertReplyBuildError(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	rm, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(failedXDR(t)), nil).Once()
	h.svc.EXPECT().GetLatestLedger(mock.Anything).
		Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
	h.svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
		Return(reportProcessedEventsForFixture(t, rm, req.ContractId, false), nil).Once()
	h.svc.EXPECT().GetTransaction(mock.Anything, stellartypes.GetTransactionRequest{TxHash: testTxHash}).
		Return(stellartypes.GetTransactionResponse{}, errors.New("rpc down")).Maybe()

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
	require.NotNil(t, capErr)
	require.Contains(t, capErr.Error(), unknownIssueExecutingReceiverContractMessage)
}

func TestWriteReport_PostSubmitPollRecoversFromEvents(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	rm, reqMeta, req := newWRReportFixture(t)
	h.expectSigningAccount(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
	h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
		Return(successSubmitResp(), nil).Once()
	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil)
	h.expectPostSubmitSuccessTxLookup(t, rm, req.ContractId)

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(400*time.Millisecond))
	defer cancel()

	result, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
	require.Nil(t, capErr)
	require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
	require.Equal(t, testTxHash, *result.Response.TxHash)
}

func (h *writeReportHelper) withRecordingProcessor() *recordingWriteReportProcessor {
	processor := &recordingWriteReportProcessor{}
	h.stellar.beholderProcessor = processor
	return processor
}

func newQueuedWriteReportHelper(t *testing.T) *writeReportHelper {
	t.Helper()
	lggr := logger.Test(t)
	mockSvc := mocks.NewStellarService(t)
	scheduler := ts.NewTransmissionScheduler(
		p2ptypes.PeerID{2},
		[]p2ptypes.PeerID{{1}, {2}, {3}},
		5*time.Second,
		0,
		lggr,
	)
	s := &Stellar{
		StellarService:           mockSvc,
		lggr:                     logger.Sugared(lggr),
		chainSelector:            testWRChainSelector,
		forwarderClient:          newForwarderClient(mockSvc, lggr, testForwarderAddress, 100),
		forwarderLookbackLedgers: 100,
		transmissionScheduler:    scheduler,
		messageBuilder:           monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		beholderProcessor:        nopBeholderProcessor{},
		handler:                  testConsensusHandler{handle: runVolatileHashableHandle},
	}
	require.NoError(t, s.initLimiters(limits.Factory{Logger: lggr}))
	return &writeReportHelper{svc: mockSvc, stellar: s}
}

func hasTelemetryMessage[T proto.Message](messages []proto.Message) bool {
	for _, msg := range messages {
		if _, ok := msg.(T); ok {
			return true
		}
	}
	return false
}

func requireDuplicateTxTelemetry(t *testing.T, messages []proto.Message, duplicateHash, canonicalHash string) {
	t.Helper()
	for _, msg := range messages {
		dup, ok := msg.(*monitoring.WriteReportDuplicateTx)
		if !ok {
			continue
		}
		require.Equal(t, duplicateHash, dup.GetDuplicateTxHash())
		require.Equal(t, canonicalHash, dup.GetCanonicalTxHash())
		return
	}
	t.Fatalf("missing WriteReportDuplicateTx telemetry duplicate=%s canonical=%s", duplicateHash, canonicalHash)
}

type recordingWriteReportProcessor struct {
	messages []proto.Message
}

func (r *recordingWriteReportProcessor) Process(_ context.Context, msg proto.Message, _ ...any) error {
	r.messages = append(r.messages, msg)
	return nil
}

func TestWriteReport_EmitsTxInfoRetrievalErrorTelemetry(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	processor := h.withRecordingProcessor()
	rm, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(failedXDR(t)), nil).Once()
	h.svc.EXPECT().GetLatestLedger(mock.Anything).
		Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
	h.svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
		Return(reportProcessedEventsForFixture(t, rm, req.ContractId, false), nil).Once()
	h.svc.EXPECT().GetTransaction(mock.Anything, stellartypes.GetTransactionRequest{TxHash: testTxHash}).
		Return(stellartypes.GetTransactionResponse{}, errors.New("rpc down")).Maybe()

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.True(t, hasTelemetryMessage[*monitoring.WriteReportTxInfoRetrievalError](processor.messages))
}

func TestWriteReport_EmitsInvalidTransmissionStateOnUnexpectedSuccess(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	processor := h.withRecordingProcessor()
	rm, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(failedXDR(t)), nil).Once()
	h.svc.EXPECT().GetLatestLedger(mock.Anything).
		Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
	h.svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
		Return(reportProcessedEventsForFixture(t, rm, req.ContractId, true), nil).Once()

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.True(t, hasTelemetryMessage[*monitoring.WriteReportInvalidTransmissionState](processor.messages))
}

func TestWriteReport_EmitsSuccessfulEarlyReturnTelemetry(t *testing.T) {
	t.Parallel()
	h := newQueuedWriteReportHelper(t)
	processor := h.withRecordingProcessor()
	rm, reqMeta, req := newWRReportFixture(t)

	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)
	scheduleKey, err := transmissionID.ScheduleKey()
	require.NoError(t, err)
	queuePosition := h.stellar.transmissionScheduler.GetQueuePosition(hex.EncodeToString(scheduleKey[:]))
	if queuePosition <= 0 {
		t.Skip("fixture resolves to queue position 0 in this 3-node schedule")
	}

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(succeededXDR(t)), nil).Once()
	h.expectObservedTxHashLookup(t, rm, req.ContractId, true)

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.Nil(t, capErr)
	require.True(t, hasTelemetryMessage[*monitoring.WriteReportSuccessfulEarlyReturn](processor.messages))
}

func TestWriteReport_EmitsLifecycleTelemetry(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	processor := &recordingWriteReportProcessor{}
	h.stellar.beholderProcessor = processor

	rm, reqMeta, req := newWRReportFixture(t)
	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(succeededXDR(t)), nil).Once()
	h.expectObservedTxHashLookup(t, rm, req.ContractId, true)

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.Nil(t, capErr)

	var sawInitiated, sawSuccess bool
	for _, msg := range processor.messages {
		switch msg.(type) {
		case *monitoring.WriteReportInitiated:
			sawInitiated = true
		case *monitoring.WriteReportSuccess:
			sawSuccess = true
		case *monitoring.WriteReportSuccessfulEarlyReturn:
			t.Fatalf("unexpected early return telemetry on direct success path")
		}
	}
	require.True(t, sawInitiated)
	require.True(t, sawSuccess)
}

func TestIsUserErrorWriteReport(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	require.True(t, h.stellar.isUserErrorWriteReport(errors.New(capcommon.UserError+" invalid receiver")))
	require.False(t, h.stellar.isUserErrorWriteReport(errors.New("system failure")))
}

func TestWriteReport_EmitsInvalidTransmissionStatePreSubmitWithStubForwarder(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	processor := h.withRecordingProcessor()
	_, reqMeta, req := newWRReportFixture(t)

	h.stellar.forwarderClient = &stubForwarderClient{
		transmissionInfoFn: func(int) (TransmissionInfo, error) {
			return TransmissionInfo{State: TransmissionState(99)}, nil
		},
	}

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.True(t, hasTelemetryMessage[*monitoring.WriteReportInvalidTransmissionState](processor.messages))
}

func TestWriteReport_EmitsInvalidTransmissionStateAfterSubmitWithStubForwarder(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	processor := h.withRecordingProcessor()
	_, reqMeta, req := newWRReportFixture(t)

	call := 0
	h.stellar.forwarderClient = &stubForwarderClient{
		transmissionInfoFn: func(int) (TransmissionInfo, error) {
			call++
			if call == 1 {
				return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
			}
			return TransmissionInfo{State: TransmissionState(99)}, nil
		},
		invokeOnReportResp: successSubmitResp(),
	}

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.True(t, hasTelemetryMessage[*monitoring.WriteReportInvalidTransmissionState](processor.messages))
}

func TestPollTransmissionInfo_EmitsInvalidTransmissionStateWithStubForwarder(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)
	processor := &recordingWriteReportProcessor{}
	scheduler := ts.NewTransmissionScheduler(
		p2ptypes.PeerID{2},
		[]p2ptypes.PeerID{{1}, {2}, {3}},
		150*time.Millisecond,
		0,
		lggr,
	)
	call := 0
	stub := &stubForwarderClient{
		transmissionInfoFn: func(int) (TransmissionInfo, error) {
			call++
			if call == 1 {
				return TransmissionInfo{State: TransmissionState(99)}, nil
			}
			return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
		},
	}
	wr := &writeReport{
		forwarderClient:       stub,
		lggr:                  logger.Sugared(lggr),
		transmissionScheduler: scheduler,
		messageBuilder:        monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		beholderProcessor:     processor,
	}
	_, reqMeta, req := newWRReportFixture(t)
	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(500*time.Millisecond))
	defer cancel()

	_, err = wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, 2)
	require.NoError(t, err)
	require.True(t, hasTelemetryMessage[*monitoring.WriteReportInvalidTransmissionState](processor.messages))
}

func TestPollTransmissionInfo_EmitsInvalidTransmissionStateOnlyOnce(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)
	processor := &recordingWriteReportProcessor{}
	scheduler := ts.NewTransmissionScheduler(
		p2ptypes.PeerID{2},
		[]p2ptypes.PeerID{{1}, {2}, {3}},
		150*time.Millisecond,
		0,
		lggr,
	)
	// Always return unexpected state — ensures multiple poll iterations hit the default branch.
	stub := &stubForwarderClient{
		transmissionInfoFn: func(int) (TransmissionInfo, error) {
			return TransmissionInfo{State: TransmissionState(99)}, nil
		},
	}
	wr := &writeReport{
		forwarderClient:       stub,
		lggr:                  logger.Sugared(lggr),
		transmissionScheduler: scheduler,
		messageBuilder:        monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		beholderProcessor:     processor,
	}
	_, reqMeta, req := newWRReportFixture(t)
	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(500*time.Millisecond))
	defer cancel()

	_, err = wr.pollTransmissionInfo(ctx, req, monitoring.TelemetryContext{}, transmissionID, 2)
	require.NoError(t, err)

	var invalidStateCount int
	for _, msg := range processor.messages {
		if _, ok := msg.(*monitoring.WriteReportInvalidTransmissionState); ok {
			invalidStateCount++
		}
	}
	require.Equal(t, 1, invalidStateCount, "InvalidTransmissionState should fire exactly once even across multiple poll iterations with a persistent unexpected state")
}

func TestWriteReport_EmitsInvalidTransmissionStateOnPostSubmitUnexpectedSuccess(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	processor := h.withRecordingProcessor()
	rm, reqMeta, req := newWRReportFixture(t)
	h.expectSigningAccount(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
	h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
		Return(successSubmitResp(), nil).Once()
	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(failedXDR(t)), nil).Once()
	h.svc.EXPECT().GetLatestLedger(mock.Anything).
		Return(stellartypes.GetLatestLedgerResponse{Sequence: 200}, nil).Once()
	h.svc.EXPECT().GetEvents(mock.Anything, mock.Anything).
		Return(reportProcessedEventsForFixture(t, rm, req.ContractId, true), nil).Once()

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.True(t, hasTelemetryMessage[*monitoring.WriteReportInvalidTransmissionState](processor.messages))
}

func TestMonitoringEnabled(t *testing.T) {
	t.Parallel()

	wr := &writeReport{
		messageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		beholderProcessor: nopBeholderProcessor{},
	}
	require.True(t, wr.monitoringEnabled())

	disabled := &writeReport{messageBuilder: monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, "")}
	require.False(t, disabled.monitoringEnabled())
}

func TestEmitInvalidTransmissionState_MonitoringDisabled(t *testing.T) {
	t.Parallel()

	_, reqMeta, req := newWRReportFixture(t)
	transmissionID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)

	wr := &writeReport{
		lggr:           logger.Sugared(logger.Test(t)),
		messageBuilder: monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	}
	require.NotPanics(t, func() {
		wr.emitInvalidTransmissionState(
			t.Context(),
			req,
			monitoring.TelemetryContext{},
			TransmissionInfo{State: TransmissionState(99)},
			transmissionID,
			"summary",
			"cause",
		)
	})
}

func TestWriteReport_PreSubmitSucceeded_EventsUnavailable(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	_, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(succeededXDR(t)), nil).Once()
	h.expectEventTxHashLookupUnavailable(t)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
	require.NotNil(t, capErr)
	require.Contains(t, capErr.Error(), failedToRetrieveTxHashErrorMsg)
}

func TestWriteReport_PreSubmitInvalidReceiver_EventsUnavailable(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	_, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(invalidReceiverXDR(t)), nil).Once()
	h.expectEventTxHashLookupUnavailable(t)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
	require.NotNil(t, capErr)
	require.Contains(t, capErr.Error(), failedToRetrieveTxHashErrorMsg)
}

func TestWriteReport_PostSubmitFailed_EventsUnavailable(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	processor := h.withRecordingProcessor()
	_, reqMeta, req := newWRReportFixture(t)
	h.expectSigningAccount(t)

	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
	h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
		Return(successSubmitResp(), nil).Once()
	h.svc.EXPECT().SimulateTransaction(mock.Anything, mock.Anything).
		Return(transmissionResp(failedXDR(t)), nil).Once()
	h.expectEventTxHashLookupUnavailable(t)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
	require.NotNil(t, capErr)
	require.Contains(t, capErr.Error(), failedToRetrieveTxHashErrorMsg)
	require.False(t, hasTelemetryMessage[*monitoring.WriteReportInvalidTransmissionState](processor.messages))
}

func TestReplyFromTransaction_SkipsTelemetryWhenMonitoringDisabled(t *testing.T) {
	t.Parallel()
	_, _, req := newWRReportFixture(t)
	mockSvc := mocks.NewStellarService(t)
	mockSvc.EXPECT().GetTransaction(mock.Anything, stellartypes.GetTransactionRequest{TxHash: testTxHash}).
		Return(stellartypes.GetTransactionResponse{}, errors.New("rpc down")).Maybe()

	wr := &writeReport{
		service:        mockSvc,
		lggr:           logger.Sugared(logger.Test(t)),
		messageBuilder: monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	}
	_, err := wr.replyFromTransaction(
		t.Context(),
		req,
		monitoring.TelemetryContext{},
		testTxHash,
		stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS,
		nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get transaction")
}
