package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

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
		StellarService:        mockSvc,
		lggr:                  logger.Sugared(lggr),
		chainSelector:         testWRChainSelector,
		forwarderAddress:      testForwarderAddress,
		nodeAddress:           testNodeAddress,
		transmissionScheduler: scheduler,
		messageBuilder:        monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		beholderProcessor:     nopBeholderProcessor{},
		handler:               testConsensusHandler{handle: runVolatileHashableHandle},
	}
	require.NoError(t, s.initLimiters(limits.Factory{Logger: lggr}))
	return &writeReportHelper{svc: mockSvc, stellar: s}
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
	sig := make([]byte, 32)
	sig[0] = 0xAB
	return []*workflowpb.AttributedSignature{{Signature: sig}, {Signature: sig}}
}

// ─── XDR helpers ─────────────────────────────────────────────────────────────

// buildTransmissionInfoXDR returns a base64-encoded XDR ScVal{scvU32=state}.
// This is what the Stellar forwarder returns from get_transmission_info.
// We use the stellar SDK's MarshalBase64 so the encoding is verifiable by SafeUnmarshalBase64.
func buildTransmissionInfoXDR(t *testing.T, state TransmissionState) string {
	t.Helper()
	v := xdr.Uint32(state)
	sv := xdr.ScVal{
		Type: xdr.ScValTypeScvU32,
		U32:  &v,
	}
	b64, err := xdr.MarshalBase64(sv)
	require.NoError(t, err, "XDR encode transmission state")
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

func transmissionResp(xdrResult string) stellartypes.ReadContractResponse {
	return stellartypes.ReadContractResponse{Result: xdrResult, LedgerSequence: 100}
}

func simulationSuccessResp() stellartypes.ReadContractResponse {
	return stellartypes.ReadContractResponse{} // empty result = no error = simulation passed
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
		require.Contains(t, err.Error(), "contracId is required")
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
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
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
		_, reqMeta, req := newWRReportFixture(t)

		// Only get_transmission_info is called; no simulation or submit.
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		rcSuccess := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		require.Equal(t, &rcSuccess, result.Response.ReceiverContractExecutionStatus)
		// No hash or fee: forwarder doesn't store tx hash, and this node didn't spend gas.
		require.Nil(t, result.Response.TxHash)
		require.Nil(t, result.Response.TransactionFee)
		require.Nil(t, result.Response.BlockTimestamp)
		// No billing metering: this node observed, not submitted.
		require.Empty(t, result.ResponseMetadata.Metering)
		h.svc.AssertNotCalled(t, "SubmitTransaction", mock.Anything, mock.Anything)
	})

	t.Run("already failed - InvalidReceiver - terminal error message, no submit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(invalidReceiverXDR(t)), nil).Once()

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		rcReverted := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
		require.Equal(t, &rcReverted, result.Response.ReceiverContractExecutionStatus)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "not a Wasm contract or missing on_report")
		require.Nil(t, result.Response.BlockTimestamp)
		require.Empty(t, result.ResponseMetadata.Metering)
		h.svc.AssertNotCalled(t, "SubmitTransaction", mock.Anything, mock.Anything)
	})

	t.Run("already failed - receiver revert - error message, no submit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(failedXDR(t)), nil).Once()

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "receiver contract execution failed")
		require.Nil(t, result.Response.BlockTimestamp)
		require.Empty(t, result.ResponseMetadata.Metering)
		h.svc.AssertNotCalled(t, "SubmitTransaction", mock.Anything, mock.Anything)
	})
}

// ─── Simulation (pre-submit forwarder check) tests ───────────────────────────

func TestWriteReport_Simulation(t *testing.T) {
	t.Parallel()

	t.Run("forwarder simulation revert - user error, no submit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)

		// Call 1: get_transmission_info → NotAttempted
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		// Call 2: simulation (report) → forwarder revert
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{Error: "Error(WasmVm): wrong DON config"}, nil).Once()

		_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "forwarder simulation failed")
		require.Contains(t, capErr.Error(), "wrong DON config")
		h.svc.AssertNotCalled(t, "SubmitTransaction", mock.Anything, mock.Anything)
	})

	t.Run("simulation RPC error - propagated as error", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)

		// Call 1: get_transmission_info → NotAttempted
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		// Call 2: simulation RPC failure
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{}, errors.New("connection refused")).Once()

		_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "failed to simulate forwarder report call")
	})
}

// ─── Happy-path and submit tests ─────────────────────────────────────────────

func TestWriteReport_HappyPath(t *testing.T) {
	t.Parallel()

	t.Run("fresh submit succeeds - reply contains hash, fee and metering", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)

		// Call 1: pre-submit get_transmission_info → NotAttempted
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		// Call 2: forwarder simulation → success
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(simulationSuccessResp(), nil).Once()
		// TXM submit → success with fee
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		// Call 3: post-submit get_transmission_info → Succeeded
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()

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

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(simulationSuccessResp(), nil).Once()
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

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(simulationSuccessResp(), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		// Post-submit poll always returns NotAttempted → times out → fallback.
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil)

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

	t.Run("post-submit shows InvalidReceiver - reply with error message and hash from submit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(simulationSuccessResp(), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(invalidReceiverXDR(t)), nil).Once()

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_REVERTED, result.Response.TxStatus)
		// hash and fee come from submit response via populateReplyFromSubmit.
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

	t.Run("post-submit shows Failed - receiver revert - error message and hash from submit", func(t *testing.T) {
		t.Parallel()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(simulationSuccessResp(), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(successSubmitResp(), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(failedXDR(t)), nil).Once()

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

		fee := uint64(0)
		failedResp := &stellartypes.SubmitTransactionResponse{
			TxStatus:       stellartypes.TxFailed,
			TxHash:         testTxHash,
			Error:          "transaction result: InvokeHostFunctionTrapped",
			TransactionFee: &fee,
		}

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(simulationSuccessResp(), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(failedResp, nil).Once()
		// Post-submit poll stays NotAttempted → context deadline triggers fallback.
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil)

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
		_, reqMeta, req := newWRReportFixture(t)

		fee := uint64(0)
		// Our node's submission "fails" (another node already succeeded), but
		// the post-submit poll shows Succeeded.
		myResp := &stellartypes.SubmitTransactionResponse{
			TxStatus:       stellartypes.TxFailed,
			TxHash:         "mytx",
			Error:          "Already processed",
			TransactionFee: &fee,
		}

		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(simulationSuccessResp(), nil).Once()
		h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
			Return(myResp, nil).Once()
		// Post-submit: another node already succeeded.
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(transmissionResp(succeededXDR(t)), nil).Once()

		result, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, stellarcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
	})
}

// ─── parseTransmissionInfo unit tests ────────────────────────────────────────

func TestParseTransmissionInfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		state         TransmissionState
		wantSuccess   bool
		wantInvalidRx bool
	}{
		{"NotAttempted", TransmissionStateNotAttempted, false, false},
		{"Succeeded", TransmissionStateSucceeded, true, false},
		{"InvalidReceiver", TransmissionStateInvalidReceiver, false, true},
		{"Failed", TransmissionStateFailed, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			xdrResult := buildTransmissionInfoXDR(t, tc.state)
			info, err := parseTransmissionInfo(xdrResult, 42)
			require.NoError(t, err)
			require.Equal(t, tc.state, info.State)
			require.Equal(t, tc.wantSuccess, info.Success)
			require.Equal(t, tc.wantInvalidRx, info.InvalidReceiver)
			require.Equal(t, uint32(42), info.LedgerSequence)
		})
	}

	t.Run("invalid base64 returns error", func(t *testing.T) {
		t.Parallel()
		_, err := parseTransmissionInfo("not-valid-xdr!!!", 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode transmission info result XDR")
	})
}

// ─── Transmission ID tests ────────────────────────────────────────────────────

func TestGetTransmissionID_Determinism(t *testing.T) {
	t.Parallel()

	_, _, req := newWRReportFixture(t)
	execID := hex.EncodeToString(commontest.RandomBytes(32))

	id1, _, _, err1 := getTransmissionID(execID, req)
	id2, _, _, err2 := getTransmissionID(execID, req)
	require.NoError(t, err1)
	require.NoError(t, err2)
	require.Equal(t, id1, id2, "transmission ID must be deterministic for identical inputs")
}

func TestGetTransmissionID_DifferentInputsDifferentIDs(t *testing.T) {
	t.Parallel()

	_, _, req1 := newWRReportFixture(t)
	_, _, req2 := newWRReportFixture(t)
	execID := hex.EncodeToString(commontest.RandomBytes(32))

	id1, _, _, err1 := getTransmissionID(execID, req1)
	id2, _, _, err2 := getTransmissionID(execID, req2)
	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NotEqual(t, id1, id2, "different receivers must produce different transmission IDs")
}

func TestGetTransmissionID_InvalidReceiver(t *testing.T) {
	t.Parallel()

	// Build a fixture so RawReport and execID are valid; only ContractId is wrong.
	rm, reqMeta, req := newWRReportFixture(t)
	_ = rm
	req.ContractId = testNodeAddress // G… StrKey — not a contract address

	_, _, _, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid receiver contract address")
}

func TestWriteReport_EmptyNodeAddress(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	h.stellar.nodeAddress = ""
	_, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil).Once()

	_, capErr := h.stellar.WriteReport(t.Context(), reqMeta, req)
	require.NotNil(t, capErr)
	require.Contains(t, capErr.Error(), "node address is not configured")
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

	newWR := func(t *testing.T) (*writeReportHelper, *writeReport, [32]byte, [2]byte) {
		t.Helper()
		h := newWriteReportHelper(t)
		_, reqMeta, req := newWRReportFixture(t)
		_, workflowExecutionID, reportID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
		require.NoError(t, err)
		wr := &writeReport{
			service:          h.svc,
			lggr:             h.stellar.lggr,
			forwarderAddress: testForwarderAddress,
		}
		return h, wr, workflowExecutionID, reportID
	}

	t.Run("missing entry treated as not attempted", func(t *testing.T) {
		t.Parallel()
		h, wr, workflowExecutionID, reportID := newWR(t)
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{Error: "entry missing"}, nil).Once()

		info, err := wr.getTransmissionInfo(t.Context(), testReceiverAddress, workflowExecutionID, reportID)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateNotAttempted, info.State)
	})

	t.Run("not found treated as not attempted", func(t *testing.T) {
		t.Parallel()
		h, wr, workflowExecutionID, reportID := newWR(t)
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{Error: "transmission not found"}, nil).Once()

		info, err := wr.getTransmissionInfo(t.Context(), testReceiverAddress, workflowExecutionID, reportID)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateNotAttempted, info.State)
	})

	t.Run("empty result treated as not attempted", func(t *testing.T) {
		t.Parallel()
		h, wr, workflowExecutionID, reportID := newWR(t)
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{}, nil).Once()

		info, err := wr.getTransmissionInfo(t.Context(), testReceiverAddress, workflowExecutionID, reportID)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateNotAttempted, info.State)
	})

	t.Run("non-missing forwarder error is propagated", func(t *testing.T) {
		t.Parallel()
		h, wr, workflowExecutionID, reportID := newWR(t)
		h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{Error: "contract trap"}, nil).Once()

		_, err := wr.getTransmissionInfo(t.Context(), testReceiverAddress, workflowExecutionID, reportID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "forwarder read failed")
	})
}

func TestPollTransmissionInfo_WithQueuePosition(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	lggr := logger.Test(t)
	myPeerID := p2ptypes.PeerID{2}
	scheduler := ts.NewTransmissionScheduler(
		myPeerID,
		[]p2ptypes.PeerID{{1}, myPeerID, {3}},
		50*time.Millisecond,
		0,
		lggr,
	)
	h.stellar.transmissionScheduler = scheduler

	wr := &writeReport{
		service:               h.svc,
		lggr:                  h.stellar.lggr,
		forwarderAddress:      testForwarderAddress,
		transmissionScheduler: scheduler,
	}

	_, reqMeta, req := newWRReportFixture(t)
	_, workflowExecutionID, reportID, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
	require.NoError(t, err)

	h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
		Return(transmissionResp(succeededXDR(t)), nil)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	info, err := wr.pollTransmissionInfo(ctx, req.ContractId, workflowExecutionID, reportID, 1)
	require.NoError(t, err)
	require.Equal(t, TransmissionStateSucceeded, info.State)
}

func TestParseFieldsIntoTransmissionInfo(t *testing.T) {
	t.Parallel()

	t.Run("vec with state and transmitter", func(t *testing.T) {
		t.Parallel()
		state := xdr.Uint32(TransmissionStateSucceeded)
		stateVal := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &state}
		accountID := xdr.MustAddress(testNodeAddress)
		txrVal := xdr.ScVal{
			Type: xdr.ScValTypeScvAddress,
			Address: &xdr.ScAddress{
				Type:      xdr.ScAddressTypeScAddressTypeAccount,
				AccountId: &accountID,
			},
		}
		vec := xdr.ScVec{stateVal, txrVal}
		vecPtr := &vec
		sv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &vecPtr}

		info := TransmissionInfo{}
		parseFieldsIntoTransmissionInfo(&info, sv)
		require.Equal(t, TransmissionStateSucceeded, info.State)
		require.Equal(t, testNodeAddress, info.Transmitter)
	})

	t.Run("map with state transmitter and flags", func(t *testing.T) {
		t.Parallel()
		state := xdr.Uint32(TransmissionStateInvalidReceiver)
		stateVal := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &state}
		stateKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: func() *xdr.ScSymbol { s := xdr.ScSymbol("state"); return &s }()}
		invalid := true
		invalidVal := xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &invalid}
		invalidKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: func() *xdr.ScSymbol { s := xdr.ScSymbol("invalid_receiver"); return &s }()}
		scMap := xdr.ScMap{
			{Key: stateKey, Val: stateVal},
			{Key: invalidKey, Val: invalidVal},
		}
		mapPtr := &scMap
		sv := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr}

		info := TransmissionInfo{}
		parseFieldsIntoTransmissionInfo(&info, sv)
		require.Equal(t, TransmissionStateInvalidReceiver, info.State)
		require.True(t, info.InvalidReceiver)
	})
}

func TestXDRExtractHelpers(t *testing.T) {
	t.Parallel()

	t.Run("extractStringish symbol and string", func(t *testing.T) {
		t.Parallel()
		sym := xdr.ScSymbol("state")
		symVal := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
		out, ok := extractStringish(symVal)
		require.True(t, ok)
		require.Equal(t, "state", out)

		str := xdr.ScString("transmitter")
		strVal := xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
		out, ok = extractStringish(strVal)
		require.True(t, ok)
		require.Equal(t, "transmitter", out)
	})

	t.Run("extractAddressString account and contract", func(t *testing.T) {
		t.Parallel()
		accountID := xdr.MustAddress(testNodeAddress)
		accountVal := xdr.ScVal{
			Type: xdr.ScValTypeScvAddress,
			Address: &xdr.ScAddress{
				Type:      xdr.ScAddressTypeScAddressTypeAccount,
				AccountId: &accountID,
			},
		}
		out, ok := extractAddressString(accountVal)
		require.True(t, ok)
		require.Equal(t, testNodeAddress, out)

		contractBytes, err := strkey.Decode(strkey.VersionByteContract, testForwarderAddress)
		require.NoError(t, err)
		var contractID xdr.ContractId
		copy(contractID[:], contractBytes)
		contractVal := xdr.ScVal{
			Type: xdr.ScValTypeScvAddress,
			Address: &xdr.ScAddress{
				Type:       xdr.ScAddressTypeScAddressTypeContract,
				ContractId: &contractID,
			},
		}
		out, ok = extractAddressString(contractVal)
		require.True(t, ok)
		require.Equal(t, testForwarderAddress, out)
	})

	t.Run("extractBool", func(t *testing.T) {
		t.Parallel()
		b := true
		val := xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b}
		require.NotNil(t, extractBool(val))
		require.True(t, *extractBool(val))
	})
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

func TestBuildForwarderReportArgs_InvalidTransmitter(t *testing.T) {
	t.Parallel()
	_, _, req := newWRReportFixture(t)
	_, err := buildForwarderReportArgs("not-a-valid-address", testReceiverAddress, req.Report)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transmitter")
}

func TestWriteReport_TxFatalSubmitFallback(t *testing.T) {
	t.Parallel()
	h := newWriteReportHelper(t)
	_, reqMeta, req := newWRReportFixture(t)

	h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil).Once()
	h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
		Return(simulationSuccessResp(), nil).Once()
	h.svc.EXPECT().SubmitTransaction(mock.Anything, mock.Anything).
		Return(&stellartypes.SubmitTransactionResponse{
			TxStatus: stellartypes.TxFatal,
			TxHash:   testTxHash,
		}, nil).Once()
	h.svc.EXPECT().ReadContract(mock.Anything, mock.Anything).
		Return(transmissionResp(notAttemptedXDR(t)), nil)

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(400*time.Millisecond))
	defer cancel()

	result, capErr := h.stellar.WriteReport(ctx, reqMeta, req)
	require.Nil(t, capErr)
	require.Equal(t, stellarcap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
	require.NotNil(t, result.Response.TxHash)
}
