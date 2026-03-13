package actions

import (
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/metering"
	commontest "github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
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

const testChainSelector = uint64(1)

var (
	testForwarderAddr = aptos_sdk.AccountAddress{0xAA}
	testReceiver      = aptos_sdk.AccountAddress{0xBB}
	testTransmitter   = aptos_sdk.AccountAddress{0xCC}
)

func validateMeteringWriteReport(t *testing.T, metadata capabilities.ResponseMetadata, chainSelector uint64) {
	t.Helper()
	require.Len(t, metadata.Metering, 1)
	meteringNodeDetail := metadata.Metering[0]
	require.Equal(t, fmt.Sprintf(metering.WriteReportSpendUnitFormat, chainSelector), meteringNodeDetail.SpendUnit)
	require.Equal(t, "0", meteringNodeDetail.SpendValue)
	require.Empty(t, meteringNodeDetail.Peer2PeerID)
}

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
		chainSelector:    testChainSelector,
		transmissionScheduler: transmission_schedule.NewTransmissionScheduler(
			myPeerID, []p2ptypes.PeerID{myPeerID}, 1*time.Second, 0, lggr),
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
	scheduler := transmission_schedule.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, 15*time.Second, 0, lggr)
	if scheduler.GetQueuePosition(transmissionIDStr) == 0 {
		myPeerID, otherPeerID = otherPeerID, myPeerID
		p2pCfg = buildCfg()
		scheduler = transmission_schedule.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, 15*time.Second, 0, lggr)
	}
	require.Greater(t, scheduler.GetQueuePosition(transmissionIDStr), 0)

	a := &Aptos{
		AptosService:          &types.UnimplementedAptosService{},
		forwarderClient:       mockClient,
		forwarderAddress:      testForwarderAddr,
		lggr:                  logger.Sugared(lggr),
		p2pConfig:             p2pCfg,
		chainSelector:         testChainSelector,
		transmissionScheduler: scheduler,
	}
	require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
	return &testHelper{forwarderClient: mockClient, aptos: a}, node0Addr
}

func newReportFixture(t *testing.T) (ocrtypes.Metadata, capabilities.RequestMetadata, *aptoscap.WriteReportRequest) {
	t.Helper()
	rm := ocrtypes.Metadata{
		Version: 1, ExecutionID: hex.EncodeToString(commontest.RandomBytes(32)),
		Timestamp: 1000, DONID: 10, DONConfigVersion: 2,
		WorkflowID: hex.EncodeToString(commontest.RandomBytes(32)), WorkflowName: hex.EncodeToString(commontest.RandomBytes(10)),
		WorkflowOwner: hex.EncodeToString(commontest.RandomBytes(20)), ReportID: hex.EncodeToString(commontest.RandomBytes(2)),
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

// buildFakeTransaction constructs an aptostypes.Transaction whose Data field is JSON
// matching the Go-default marshal output of UserTransaction (uppercase keys, numeric types),
// which is what scanTransactions unmarshals via the local userTxData struct.
func buildFakeTransaction(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro uint64, reportMetadata ocrtypes.Metadata) *aptostypes.Transaction {
	t.Helper()
	encodedReport, err := reportMetadata.Encode()
	require.NoError(t, err)

	rawReportHex := "0x" + hex.EncodeToString(append(make([]byte, 96), encodedReport...))
	functionName := fmt.Sprintf("%s::forwarder::report", testForwarderAddr.String())

	txJSON := fmt.Sprintf(`{
		"Hash": %q, "Success": %t, "SequenceNumber": %d, "Timestamp": %d,
		"Payload": {"Inner": {"Function": %q, "Arguments": ["0x01", %q, "0x01"]}}
	}`, txHash, success, seqNum, timestampMicro, functionName, rawReportHex)

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
		reqMeta.WorkflowID = hex.EncodeToString(commontest.RandomBytes(32))

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
			Report:   &workflowpb.ReportResponse{RawReport: append(encoded, make([]byte, 1000)...), Sigs: generateRandomSignatures()},
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
		h.mockPostSubmitPoll(TransmissionInfo{Success: true, Transmitter: testTransmitter})

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xabc", *result.Response.TxHash)
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector)
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
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector)
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
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector)
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
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector)
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
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector)
	})
}
