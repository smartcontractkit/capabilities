package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
)

// --- helpers ---

var (
	testForwarderAddr = aptos_sdk.AccountAddress{0xAA}
	testReceiver      = aptos_sdk.AccountAddress{0xBB}
)

type testHelper struct {
	forwarderClient *CREForwarderClient_mock
	aptos           *Aptos
}

func newTestHelper(t *testing.T) *testHelper {
	t.Helper()
	lggr := logger.Test(t)
	mockClient := NewCREForwarderClient_mock(t)
	myPeerID := p2ptypes.PeerID{1}

	a := &Aptos{
		AptosService:     &types.UnimplementedAptosService{},
		forwarderClient:  mockClient,
		forwarderAddress: testForwarderAddr,
		lggr:             logger.Sugared(lggr),
		p2pConfig:        map[string]string{},
		transmissionScheduler: NewTransmissionScheduler(
			myPeerID, []p2ptypes.PeerID{myPeerID}, map[string]string{},
			1*time.Second, 0, lggr,
		),
	}
	require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
	return &testHelper{forwarderClient: mockClient, aptos: a}
}

// newMultiNodeTestHelper builds a 2-node DON where our node is at queue position > 0
// for the given transmissionID string. Returns the helper and the prior node's transmitter address.
func newMultiNodeTestHelper(t *testing.T, transmissionIDStr string) (*testHelper, aptos_sdk.AccountAddress) {
	t.Helper()
	lggr := logger.Test(t)
	mockClient := NewCREForwarderClient_mock(t)

	peerA, peerB := p2ptypes.PeerID{1}, p2ptypes.PeerID{2}
	myPeerID, otherPeerID := peerB, peerA
	node0Addr := aptos_sdk.AccountAddress{0xCC}
	myAddr := aptos_sdk.AccountAddress{0xDD}

	buildCfg := func() map[string]string {
		return map[string]string{
			fmt.Sprintf("%x", otherPeerID[:]): node0Addr.StringLong(),
			fmt.Sprintf("%x", myPeerID[:]):    myAddr.StringLong(),
		}
	}

	p2pCfg := buildCfg()
	scheduler := NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, p2pCfg, 15*time.Second, 0, lggr)
	if scheduler.GetQueuePosition(transmissionIDStr) == 0 {
		myPeerID, otherPeerID = otherPeerID, myPeerID
		p2pCfg = buildCfg()
		scheduler = NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, p2pCfg, 15*time.Second, 0, lggr)
	}
	require.Greater(t, scheduler.GetQueuePosition(transmissionIDStr), 0)

	a := &Aptos{
		AptosService:          &types.UnimplementedAptosService{},
		forwarderClient:       mockClient,
		forwarderAddress:      testForwarderAddr,
		lggr:                  logger.Sugared(lggr),
		p2pConfig:             p2pCfg,
		transmissionScheduler: scheduler,
	}
	require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
	return &testHelper{forwarderClient: mockClient, aptos: a}, node0Addr
}

func newReportFixture(t *testing.T) (ocrtypes.Metadata, capabilities.RequestMetadata, *aptoscap.WriteReportRequest) {
	t.Helper()
	rm := ocrtypes.Metadata{
		Version: 1, ExecutionID: hex.EncodeToString(randomBytes(32)),
		Timestamp: 1000, DONID: 10, DONConfigVersion: 2,
		WorkflowID: hex.EncodeToString(randomBytes(32)), WorkflowName: hex.EncodeToString(randomBytes(10)),
		WorkflowOwner: hex.EncodeToString(randomBytes(20)), ReportID: hex.EncodeToString(randomBytes(2)),
	}
	encoded, err := rm.Encode()
	require.NoError(t, err)
	reqMeta := capabilities.RequestMetadata{
		WorkflowID: rm.WorkflowID, WorkflowOwner: rm.WorkflowOwner, WorkflowName: rm.WorkflowName,
		WorkflowDonID: rm.DONID, WorkflowDonConfigVersion: rm.DONConfigVersion, WorkflowExecutionID: rm.ExecutionID,
	}
	req := &aptoscap.WriteReportRequest{
		Receiver: testReceiver[:],
		Report:   &workflowpb.ReportResponse{RawReport: encoded, Sigs: generateRandomSignatures()},
	}
	return rm, reqMeta, req
}

func generateRandomSignatures() []*workflowpb.AttributedSignature {
	sig := [32]byte{1, 2, 3}
	return []*workflowpb.AttributedSignature{{Signature: sig[:]}, {Signature: sig[:]}}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// buildFakeTransaction constructs an aptostypes.Transaction whose Data field is JSON
// that scanTransactions can unmarshal into an aptos_api.UserTransaction.
func buildFakeTransaction(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro uint64, reportMetadata ocrtypes.Metadata) *aptostypes.Transaction {
	t.Helper()
	encodedReport, err := reportMetadata.Encode()
	require.NoError(t, err)

	rawReportHex := "0x" + hex.EncodeToString(append(make([]byte, 96), encodedReport...))
	functionName := fmt.Sprintf("%s::forwarder::report", testForwarderAddr.String())

	txJSON := fmt.Sprintf(`{
		"hash": %q, "success": %t, "version": "1",
		"accumulator_root_hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
		"state_change_hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
		"event_root_hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
		"gas_used": "100", "vm_status": "Executed successfully", "changes": [], "events": [],
		"sender": "0x0000000000000000000000000000000000000000000000000000000000000001",
		"sequence_number": "%d", "max_gas_amount": "100000", "gas_unit_price": "100",
		"expiration_timestamp_secs": "99999999999",
		"payload": {"type": "entry_function_payload", "function": %q, "type_arguments": [], "arguments": ["0x01", %q, "0x01"]},
		"timestamp": "%d"
	}`, txHash, success, seqNum, functionName, rawReportHex, timestampMicro)

	return &aptostypes.Transaction{Data: []byte(txJSON)}
}

func newTestTxHashRetriever(t *testing.T, mockClient *CREForwarderClient_mock, targetReportMetadata ocrtypes.Metadata, requestStartTime time.Time) TxHashRetriever {
	t.Helper()
	rawExecID, _ := hex.DecodeString(targetReportMetadata.ExecutionID)
	reportIDBytes, _ := hex.DecodeString(targetReportMetadata.ReportID)
	tid := TransmissionID{
		Receiver: testReceiver, WorkflowExecutionID: [32]byte(rawExecID), ReportID: [2]byte(reportIDBytes),
	}
	return NewTxHashRetriever(mockClient, logger.Test(t), tid, testForwarderAddr.String(), requestStartTime)
}

func computeTransmissionIDStr(t *testing.T, rm ocrtypes.Metadata) string {
	t.Helper()
	rawExecID, _ := hex.DecodeString(rm.ExecutionID)
	reportIDBytes, _ := hex.DecodeString(rm.ReportID)
	return TransmissionID{
		Receiver: testReceiver, WorkflowExecutionID: [32]byte(rawExecID), ReportID: [2]byte(reportIDBytes),
	}.GetDebugID()
}

// mockNoTransmission sets GetTransmissionInfo to return {Success: false} once.
func (h *testHelper) mockNoTransmission() {
	h.forwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
		Return(TransmissionInfo{Success: false}, nil).Once()
}

// mockPostSubmitPoll sets the second GetTransmissionInfo call (post-submission polling).
func (h *testHelper) mockPostSubmitPoll(info TransmissionInfo) {
	h.forwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
		Return(info, nil)
}

// mockInvokeOnReport sets InvokeOnReport to return the given reply.
func (h *testHelper) mockInvokeOnReport(reply *aptostypes.SubmitTransactionReply, err error) {
	h.forwarderClient.On("InvokeOnReport", mock.Anything, testReceiver[:], mock.Anything, mock.Anything).
		Return(reply, err)
}

// --- Tests ---

func TestWriteReport_Validation(t *testing.T) {
	t.Parallel()

	t.Run("WorkflowID mismatch", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)
		reqMeta.WorkflowID = hex.EncodeToString(randomBytes(32))

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "workflowID mismatch")
	})

	t.Run("Gas config exceeds limit", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)
		req.GasConfig = &aptoscap.GasConfig{MaxGasAmount: 2_000_000}

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "provided gas config exceeds limit")
	})

	t.Run("Report size exceeds limit", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, _ := newReportFixture(t)
		encoded, err := rm.Encode()
		require.NoError(t, err)

		req := &aptoscap.WriteReportRequest{
			Receiver: testReceiver[:],
			Report:   &workflowpb.ReportResponse{RawReport: append(encoded, make([]byte, 200)...), Sigs: generateRandomSignatures()},
		}
		h.mockNoTransmission()

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "report size exceeds limit")
	})
}

func TestWriteReport_Execute(t *testing.T) {
	t.Parallel()

	t.Run("Happy path - submit succeeds", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xabc"}, nil)
		h.mockPostSubmitPoll(TransmissionInfo{Success: true, Transmitter: testReceiver})

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xabc", *result.Response.TxHash)
	})

	t.Run("Already transmitted - returns without submitting", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, req := newReportFixture(t)
		transmitter := aptos_sdk.AccountAddress{0xCC}

		h.forwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(TransmissionInfo{Success: true, Transmitter: transmitter}, nil)
		h.forwarderClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{buildFakeTransaction(t, "0xalready", true, 100, uint64(time.Now().UnixMicro()), rm)}, nil)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xalready", *result.Response.TxHash)
		h.forwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("InvokeOnReport fails", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockNoTransmission()
		h.mockInvokeOnReport(nil, errors.New("rpc connection refused"))

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "failed to invoke forwarder report")
	})

	t.Run("Submit reverts but transmission succeeded - resolves real hash", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, req := newReportFixture(t)
		transmitter := aptos_sdk.AccountAddress{0xEE}

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxReverted, TxHash: "0xreverted"}, nil)
		h.mockPostSubmitPoll(TransmissionInfo{Success: true, Transmitter: transmitter})
		h.forwarderClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{buildFakeTransaction(t, "0xreal", true, 100, uint64(time.Now().UnixMicro()), rm)}, nil)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xreal", *result.Response.TxHash)
	})

	t.Run("Submit fails at node0 - returns own hash", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockPostSubmitPoll(TransmissionInfo{Success: false})

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xmine", *result.Response.TxHash)
	})

	t.Run("Unexpected TxSuccess but no transmission onchain", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xmine"}, nil)
		h.mockPostSubmitPoll(TransmissionInfo{Success: false})

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "unexpected state")
	})

	t.Run("Submit fails - retrieves failed hash from prior node", func(t *testing.T) {
		rm, reqMeta, req := newReportFixture(t)
		transmissionIDStr := computeTransmissionIDStr(t, rm)
		h, node0Addr := newMultiNodeTestHelper(t, transmissionIDStr)

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockPostSubmitPoll(TransmissionInfo{Success: false})
		h.forwarderClient.On("GetTransmitterTransactions", mock.Anything, node0Addr, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{buildFakeTransaction(t, "0xnode0failed", false, 100, uint64(time.Now().UnixMicro()), rm)}, nil)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xnode0failed", *result.Response.TxHash)
	})
}

func TestGetSuccessfulTransmissionHash(t *testing.T) {
	t.Parallel()

	transmitter := aptos_sdk.AccountAddress{0xEE}

	t.Run("Phase 1 fails - no txns found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := thr.GetSuccessfulTransmissionHash(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transmitter transactions during phase 1")
	})

	t.Run("Phase 1 misses, Phase 2 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := uint64(requestStartTime.UnixMicro())
		unrelatedTx := buildFakeTransaction(t, "0xunrelated", true, 200, recentTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase2", true, 50, recentTs-500000, targetReportMetadata)

		// Phase 1: returns unrelated tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedTx}, nil).Once()
		// Phase 2 pagination: returns matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		hash, err := thr.GetSuccessfulTransmissionHash(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase2", hash)
	})

	t.Run("Phase 1 misses, Phase 2 misses but covers time, Phase 3 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := uint64(requestStartTime.UnixMicro())
		oldTs := uint64(requestStartTime.Add(-2 * time.Minute).UnixMicro())
		unrelatedRecentTx := buildFakeTransaction(t, "0xunrelated1", true, 200, recentTs, randomReportMetadata)
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated2", true, 50, oldTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase3", true, 300, recentTs, targetReportMetadata)

		// Phase 1: unrelated tx with recent timestamp
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedRecentTx}, nil).Once()
		// Phase 2 pagination: unrelated tx with old timestamp (covers the window, exits loop)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()
		// Phase 3 polling: matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		hash, err := thr.GetSuccessfulTransmissionHash(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase3", hash)
	})

	t.Run("Phase 1 misses, skips Phase 2, Phase 3 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		oldTs := uint64(requestStartTime.Add(-2 * time.Minute).UnixMicro())
		recentTs := uint64(requestStartTime.UnixMicro())
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", true, 50, oldTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase3", true, 300, recentTs, targetReportMetadata)

		// Phase 1: unrelated tx with old timestamp (earliestTxTimestamp <= startingPointMicro, skip phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()
		// Phase 3 polling: matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		hash, err := thr.GetSuccessfulTransmissionHash(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase3", hash)
	})

	t.Run("Phase 1 misses, skips Phase 2, Phase 3 fails", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		oldTs := uint64(requestStartTime.Add(-2 * time.Minute).UnixMicro())
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", true, 50, oldTs, randomReportMetadata)

		// All GetTransmitterTransactions calls return unrelated tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		_, err := thr.GetSuccessfulTransmissionHash(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "matching transmission not found yet")
	})
}

func TestGetFailedTransmissionHash(t *testing.T) {
	t.Parallel()

	transmitter := aptos_sdk.AccountAddress{0xEE}

	t.Run("Phase 1 finds matching failed tx", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := uint64(requestStartTime.UnixMicro())
		matchingTx := buildFakeTransaction(t, "0xfailed_phase1", false, 100, recentTs, targetRM)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		hash, err := thr.GetFailedTransmissionHash(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfailed_phase1", hash)
	})

	t.Run("Phase 1 fails - no txns found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := thr.GetFailedTransmissionHash(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transmitter transactions during phase 1")
	})

	t.Run("Phase 1 misses, Phase 2 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := uint64(requestStartTime.UnixMicro())
		unrelatedTx := buildFakeTransaction(t, "0xunrelated", false, 200, recentTs, otherRM)
		matchingTx := buildFakeTransaction(t, "0xfailed_phase2", false, 50, recentTs-500000, targetRM)

		// Phase 1: unrelated failed tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedTx}, nil).Once()
		// Phase 2 pagination: matching failed tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		hash, err := thr.GetFailedTransmissionHash(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfailed_phase2", hash)
	})

	t.Run("Phase 1 misses, skips Phase 2, returns not found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		oldTs := uint64(requestStartTime.Add(-2 * time.Minute).UnixMicro())
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", false, 50, oldTs, otherRM)

		// Phase 1: unrelated tx with old timestamp (skip phase 2, no phase 3)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()

		_, err := thr.GetFailedTransmissionHash(t.Context(), transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no matching failed transaction found")
	})

	t.Run("Phase 1 misses, Phase 2 misses but covers time, returns not found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := uint64(requestStartTime.UnixMicro())
		oldTs := uint64(requestStartTime.Add(-2 * time.Minute).UnixMicro())
		unrelatedRecentTx := buildFakeTransaction(t, "0xunrelated1", false, 200, recentTs, otherRM)
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated2", false, 50, oldTs, otherRM)

		// Phase 1: unrelated tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedRecentTx}, nil).Once()
		// Phase 2: unrelated tx with old timestamp (covers window, no match, no phase 3)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()

		_, err := thr.GetFailedTransmissionHash(t.Context(), transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no matching failed transaction found")
	})
}
