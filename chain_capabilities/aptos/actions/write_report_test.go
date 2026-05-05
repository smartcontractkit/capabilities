package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
	commontest "github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

// --- helpers ---

const testChainSelector = uint64(1)
const testGasUsed = uint64(500)
const testGasUnitPrice = uint64(100)

var (
	testForwarderAddr = aptos_sdk.AccountAddress{0xAA}
	testReceiver      = aptos_sdk.AccountAddress{0xBB}
	testTransmitter   = aptos_sdk.AccountAddress{0xCC}
)

func validateMeteringWriteReport(t *testing.T, metadata capabilities.ResponseMetadata, chainSelector uint64, expectedSpendValue string) {
	t.Helper()
	require.Len(t, metadata.Metering, 1)
	meteringNodeDetail := metadata.Metering[0]
	require.Equal(t, fmt.Sprintf(metering.WriteReportSpendUnitFormat, chainSelector), meteringNodeDetail.SpendUnit)
	require.Equal(t, expectedSpendValue, meteringNodeDetail.SpendValue)
	require.Empty(t, meteringNodeDetail.Peer2PeerID)
}

type testHelper struct {
	forwarderClient *CREForwarderClient_mock
	aptosService    *mocks.AptosService
	aptos           *Aptos
}

func newTestHelper(t *testing.T) *testHelper {
	t.Helper()
	lggr := logger.Test(t)
	mockClient := NewCREForwarderClient_mock(t)
	mockService := mocks.NewAptosService(t)
	myPeerID := p2ptypes.PeerID{1}

	a := &Aptos{
		AptosService:     mockService,
		forwarderClient:  mockClient,
		forwarderAddress: testForwarderAddr,
		lggr:             logger.Sugared(lggr),
		p2pConfig:        map[string]string{},
		chainSelector:    testChainSelector,
		transmissionScheduler: ts.NewTransmissionScheduler(
			myPeerID, []p2ptypes.PeerID{myPeerID}, 1*time.Second, 0, lggr),
		txSearchStartingBuffer: 1 * time.Minute,
		beholderProcessor:      commontest.NopBeholderProcessor{},
		messageBuilder:         monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	}
	require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
	return &testHelper{forwarderClient: mockClient, aptosService: mockService, aptos: a}
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
	scheduler := ts.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, 1*time.Second, 0, lggr)
	if scheduler.GetQueuePosition(transmissionIDStr) == 0 {
		myPeerID, otherPeerID = otherPeerID, myPeerID
		p2pCfg = buildCfg()
		scheduler = ts.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, 1*time.Second, 0, lggr)
	}
	require.Greater(t, scheduler.GetQueuePosition(transmissionIDStr), 0)

	mockService := mocks.NewAptosService(t)
	a := &Aptos{
		AptosService:           mockService,
		forwarderClient:        mockClient,
		forwarderAddress:       testForwarderAddr,
		lggr:                   logger.Sugared(lggr),
		p2pConfig:              p2pCfg,
		chainSelector:          testChainSelector,
		transmissionScheduler:  scheduler,
		txSearchStartingBuffer: 1 * time.Minute,
		beholderProcessor:      commontest.NopBeholderProcessor{},
		messageBuilder:         monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	}
	require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
	return &testHelper{forwarderClient: mockClient, aptosService: mockService, aptos: a}, node0Addr
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
	reportContext := make([]byte, 96) // zeroed 96-byte report context
	req := &aptoscap.WriteReportRequest{
		Receiver: testReceiver[:],
		Report:   &workflowpb.ReportResponse{ReportContext: reportContext, RawReport: encoded, Sigs: fixedTestSignatures()},
	}
	return rm, reqMeta, req
}

func fixedTestSignatures() []*workflowpb.AttributedSignature {
	sig := [32]byte{1, 2, 3}
	return []*workflowpb.AttributedSignature{{Signature: sig[:]}, {Signature: sig[:]}}
}

// buildFakeTransaction constructs an aptostypes.Transaction whose Data field is JSON
// matching the Go-default marshal output of UserTransaction (uppercase keys, numeric types),
// which is what scanTransactions unmarshals via the local userTxData struct.
func buildFakeTransaction(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro int64, reportMetadata ocrtypes.Metadata) *aptostypes.Transaction {
	t.Helper()
	var vmStatus string
	if !success {
		vmStatus = "Move abort"
	}
	return buildFakeTransactionFull(t, txHash, success, seqNum, timestampMicro, reportMetadata, testGasUsed, testGasUnitPrice, vmStatus)
}

func buildFakeTransactionWithGas(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro int64, reportMetadata ocrtypes.Metadata, gasUsed uint64, gasUnitPrice uint64) *aptostypes.Transaction {
	t.Helper()
	var vmStatus string
	if !success {
		vmStatus = "Move abort"
	}
	return buildFakeTransactionFull(t, txHash, success, seqNum, timestampMicro, reportMetadata, gasUsed, gasUnitPrice, vmStatus)
}

func buildFakeTransactionFull(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro int64, reportMetadata ocrtypes.Metadata, gasUsed uint64, gasUnitPrice uint64, vmStatus string) *aptostypes.Transaction {
	t.Helper()
	return buildFakeTransactionWithSigs(t, txHash, success, seqNum, timestampMicro, reportMetadata, gasUsed, gasUnitPrice, gasUsed, vmStatus, fixedTestSignatures())
}

func buildFakeTransactionWithSigs(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro int64, reportMetadata ocrtypes.Metadata, gasUsed uint64, gasUnitPrice uint64, maxGasAmount uint64, vmStatus string, sigs []*workflowpb.AttributedSignature) *aptostypes.Transaction {
	t.Helper()
	encodedReport, err := reportMetadata.Encode()
	require.NoError(t, err)

	// Build signatures as a JSON array of hex strings (Aptos REST API format for vector<vector<u8>>)
	var sigHexParts []string
	for _, sig := range sigs {
		sigHexParts = append(sigHexParts, fmt.Sprintf("%q", "0x"+hex.EncodeToString(sig.Signature)))
	}
	sigsJSON := "[" + strings.Join(sigHexParts, ",") + "]"

	receiverHex := "0x" + hex.EncodeToString(testReceiver[:])
	rawReportHex := "0x" + hex.EncodeToString(append(make([]byte, 96), encodedReport...))
	functionName := fmt.Sprintf("%s::forwarder::report", testForwarderAddr.String())

	txJSON := fmt.Sprintf(`{
		"Hash": %q, "Success": %t, "SequenceNumber": %d, "Timestamp": %d,
		"GasUsed": %d, "GasUnitPrice": %d, "MaxGasAmount": %d, "VmStatus": %q,
		"Payload": {"Inner": {"Function": %q, "Arguments": [%q, %q, %s]}}
	}`, txHash, success, seqNum, timestampMicro, gasUsed, gasUnitPrice, maxGasAmount, vmStatus, functionName, receiverHex, rawReportHex, sigsJSON)

	return &aptostypes.Transaction{Data: []byte(txJSON)}
}

// monitoring
type recordingTxInfoProcessor struct {
	messages []*monitoring.WriteReportTxInfoRetrievalPhase
}

func (r *recordingTxInfoProcessor) Process(_ context.Context, msg proto.Message, _ ...any) error {
	if phaseMsg, ok := msg.(*monitoring.WriteReportTxInfoRetrievalPhase); ok {
		r.messages = append(r.messages, phaseMsg)
	}
	return nil
}

func txInfoRetrieverMonitoringOption(processor *recordingTxInfoProcessor) TxInfoRetrieverOption {
	return WithTxInfoRetrieverMonitoring(
		processor,
		monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		monitoring.TelemetryContext{},
	)
}

func newTestTxInfoRetriever(t *testing.T, mockClient *CREForwarderClient_mock, targetReportMetadata ocrtypes.Metadata, requestStartTime time.Time) (TxInfoRetriever, *recordingTxInfoProcessor) {
	t.Helper()
	rawExecID, _ := hex.DecodeString(targetReportMetadata.ExecutionID)
	reportIDBytes, _ := hex.DecodeString(targetReportMetadata.ReportID)
	tid := TransmissionID{
		Receiver: testReceiver, WorkflowExecutionID: [32]byte(rawExecID), ReportID: [2]byte(reportIDBytes),
	}
	encodedReport, err := targetReportMetadata.Encode()
	require.NoError(t, err)
	report := &workflowpb.ReportResponse{
		ReportContext: make([]byte, 96),
		RawReport:     encodedReport,
		Sigs:          fixedTestSignatures(),
	}
	processor := &recordingTxInfoProcessor{}
	return NewTxInfoRetriever(mockClient, logger.Test(t), tid, testForwarderAddr.String(), requestStartTime, 1*time.Minute, report, txInfoRetrieverMonitoringOption(processor)), processor
}

func computeTransmissionIDStr(t *testing.T, rm ocrtypes.Metadata) string {
	t.Helper()
	rawExecID, _ := hex.DecodeString(rm.ExecutionID)
	reportIDBytes, _ := hex.DecodeString(rm.ReportID)
	return TransmissionID{
		Receiver: testReceiver, WorkflowExecutionID: [32]byte(rawExecID), ReportID: [2]byte(reportIDBytes),
	}.GetDebugID()
}

// mockTransmission sets GetTransmissionInfo to return the given info.
// Chain .Once() for single-use registrations.
func (h *testHelper) mockTransmission(info TransmissionInfo) *mock.Call {
	return h.forwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
		Return(info, nil)
}

// mockTransactionByHash sets TransactionByHash to return gas data for the given tx hash.
func (h *testHelper) mockTransactionByHash(txHash string, gasUsed, gasUnitPrice uint64) {
	h.mockTransactionByHashWithVmStatus(txHash, true, gasUsed, gasUnitPrice, "Executed successfully")
}

// mockTransactionByHashFailed sets TransactionByHash to return a failed tx with the given VmStatus.
func (h *testHelper) mockTransactionByHashFailed(txHash string, gasUsed, gasUnitPrice uint64, vmStatus string) {
	h.mockTransactionByHashWithVmStatus(txHash, false, gasUsed, gasUnitPrice, vmStatus)
}

func (h *testHelper) mockTransactionByHashWithVmStatus(txHash string, success bool, gasUsed, gasUnitPrice uint64, vmStatus string) {
	txData := fmt.Sprintf(`{"Hash":%q,"Success":%t,"GasUsed":%d,"GasUnitPrice":%d,"VmStatus":%q}`, txHash, success, gasUsed, gasUnitPrice, vmStatus)
	h.aptosService.On("TransactionByHash", mock.Anything, aptostypes.TransactionByHashRequest{Hash: txHash}).Return(
		&aptostypes.TransactionByHashReply{
			Transaction: &aptostypes.Transaction{Data: []byte(txData)},
		}, nil)
}

// mockInvokeOnReport sets InvokeOnReport to return the given reply.
func (h *testHelper) mockInvokeOnReport(reply *aptostypes.SubmitTransactionReply, err error) {
	h.forwarderClient.On("InvokeOnReport", mock.Anything, testReceiver[:], mock.Anything, mock.Anything).
		Return(reply, err)
}

// mockSearchTx sets GetTransmitterTransactions for addr to return tx.
// Chain .Once() when the same addr needs a different response on a subsequent call.
func (h *testHelper) mockSearchTx(t *testing.T, addr aptos_sdk.AccountAddress, tx *aptostypes.Transaction) *mock.Call {
	t.Helper()
	return h.forwarderClient.On("GetTransmitterTransactions", mock.Anything, addr, mock.Anything, mock.Anything).
		Return([]*aptostypes.Transaction{tx}, nil)
}

type recordingWriteReportProcessor struct {
	invokeDurations []*monitoring.WriteReportInvokeOnReportDuration
}

func (r *recordingWriteReportProcessor) Process(_ context.Context, msg proto.Message, _ ...any) error {
	if invokeDurationMsg, ok := msg.(*monitoring.WriteReportInvokeOnReportDuration); ok {
		r.invokeDurations = append(r.invokeDurations, invokeDurationMsg)
	}
	return nil
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
		req.GasConfig = &aptoscap.GasConfig{MaxGasAmount: 3_000_000}

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "provided gas config exceeds limit")
	})

	t.Run("Gas config below forwarder overhead", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)
		req.GasConfig = &aptoscap.GasConfig{MaxGasAmount: 1_000}

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "below the forwarder gas overhead")
	})

	t.Run("Report size exceeds limit", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, _ := newReportFixture(t)
		encoded, err := rm.Encode()
		require.NoError(t, err)

		req := &aptoscap.WriteReportRequest{
			Receiver: testReceiver[:],
			Report:   &workflowpb.ReportResponse{RawReport: append(encoded, make([]byte, 6000)...), Sigs: fixedTestSignatures()},
		}
		h.mockTransmission(TransmissionInfo{Success: false}).Once()

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

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xabc"}, nil)
		h.mockTransmission(TransmissionInfo{Success: true, Transmitter: testTransmitter})
		h.mockTransactionByHash("0xabc", testGasUsed, testGasUnitPrice)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xabc", *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testGasUsed*testGasUnitPrice, *result.Response.TransactionFee)
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector, "0.0005")
	})

	t.Run("Already transmitted - returns without submitting", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, req := newReportFixture(t)
		transmitter := aptos_sdk.AccountAddress{0xCC}

		h.forwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(TransmissionInfo{Success: true, Transmitter: transmitter}, nil)
		h.mockSearchTx(t, transmitter, buildFakeTransaction(t, "0xalready", true, 100, time.Now().UnixMicro(), rm)) // find the already-submitted successful tx

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xalready", *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testGasUsed*testGasUnitPrice, *result.Response.TransactionFee)
		require.Empty(t, result.ResponseMetadata.Metering)
		h.forwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("InvokeOnReport fails", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(nil, errors.New("rpc connection refused"))

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "failed to invoke forwarder report")
	})

	t.Run("InvokeOnReport duration metric includes tx status", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)
		processor := &recordingWriteReportProcessor{}
		h.aptos.beholderProcessor = processor

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xabc"}, nil)
		h.mockTransmission(TransmissionInfo{Success: true, Transmitter: testTransmitter})
		h.mockTransactionByHash("0xabc", testGasUsed, testGasUnitPrice)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)

		require.Len(t, processor.invokeDurations, 1)
		invokeDuration := processor.invokeDurations[0]
		require.EqualValues(t, aptostypes.TxSuccess, invokeDuration.GetTxStatus())
		require.NotNil(t, invokeDuration.GetExecutionContext())
	})

	t.Run("Submit reverts but transmission succeeded - resolves real hash", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, req := newReportFixture(t)
		transmitter := aptos_sdk.AccountAddress{0xEE}

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxReverted, TxHash: "0xreverted"}, nil)
		h.mockTransmission(TransmissionInfo{Success: true, Transmitter: transmitter})
		h.mockTransactionByHash("0xreverted", testGasUsed, testGasUnitPrice)
		h.mockSearchTx(t, transmitter, buildFakeTransaction(t, "0xreal", true, 100, time.Now().UnixMicro(), rm)) // find the real successful tx from the winning transmitter

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xreal", *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testGasUsed*testGasUnitPrice, *result.Response.TransactionFee)
	})

	t.Run("Submit fails at node0 - returns own hash with ErrorMessage", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockTransactionByHashFailed("0xmine", testGasUsed, testGasUnitPrice, "Move abort in 0x1::coin: EINSUFFICIENT_BALANCE(0x10006)")

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xmine", *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testGasUsed*testGasUnitPrice, *result.Response.TransactionFee)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Equal(t, "Move abort in 0x1::coin: EINSUFFICIENT_BALANCE(0x10006)", *result.Response.ErrorMessage)
		require.NotNil(t, result.Response.ReceiverContractExecutionStatus)
		require.Equal(t, aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, *result.Response.ReceiverContractExecutionStatus)
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector, "0.0005")
	})

	t.Run("Unexpected TxSuccess but no transmission onchain", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xmine"}, nil)
		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockTransactionByHash("0xmine", testGasUsed, testGasUnitPrice)

		_, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.NotNil(t, capErr)
		require.Contains(t, capErr.Error(), "unexpected state")
	})

	t.Run("Submit fails - retrieves failed hash from prior node with ErrorMessage", func(t *testing.T) {
		rm, reqMeta, req := newReportFixture(t)
		transmissionIDStr := computeTransmissionIDStr(t, rm)
		h, node0Addr := newMultiNodeTestHelper(t, transmissionIDStr)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockTransactionByHash("0xmine", testGasUsed, testGasUnitPrice)
		h.mockSearchTx(t, node0Addr, buildFakeTransaction(t, "0xnode0failed", false, 100, time.Now().UnixMicro(), rm)) // post-submission search: finds node 0's failed tx

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xnode0failed", *result.Response.TxHash)
		require.NotNil(t, result.Response.TransactionFee)
		require.Equal(t, testGasUsed*testGasUnitPrice, *result.Response.TransactionFee)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Equal(t, "Move abort", *result.Response.ErrorMessage)
		require.Nil(t, result.Response.ReceiverContractExecutionStatus)
		require.Empty(t, result.ResponseMetadata.Metering)
	})

	t.Run("Submit fails - prior node receiver revert includes metering", func(t *testing.T) {
		rm, reqMeta, req := newReportFixture(t)
		transmissionIDStr := computeTransmissionIDStr(t, rm)
		h, node0Addr := newMultiNodeTestHelper(t, transmissionIDStr)

		vmReceiverRevert := "Move abort in 0x1::receiver: E_RECEIVER_FAILURE(0x64):"

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockTransactionByHash("0xmine", testGasUsed, testGasUnitPrice)
		h.mockSearchTx(t, node0Addr, buildFakeTransactionFull(t, "0xnode0failed", false, 100, time.Now().UnixMicro(), rm, testGasUsed, testGasUnitPrice, vmReceiverRevert)) // post-submission search: finds node 0's receiver revert

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xnode0failed", *result.Response.TxHash)
		require.NotNil(t, result.Response.ReceiverContractExecutionStatus)
		require.Equal(t, aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, *result.Response.ReceiverContractExecutionStatus)
		validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector, "0.0005")
	})
}

func TestWriteReport_PreSubmissionCheck(t *testing.T) {
	t.Parallel()

	t.Run("Node 0 OOG with lower gas - pre-submission passes, our submit also fails and returns node 0 failure", func(t *testing.T) {
		rm, reqMeta, req := newReportFixture(t)
		transmissionIDStr := computeTransmissionIDStr(t, rm)
		h, node0Addr := newMultiNodeTestHelper(t, transmissionIDStr)

		// Our gas (200k) > node 0's gas (100k) → pre-submission check lets us through
		req.GasConfig = &aptoscap.GasConfig{MaxGasAmount: 200_000}
		node0MaxGas := uint64(100_000)
		node0FailedTx := buildFakeTransactionWithSigs(t, "0xnode0oog", false, 100, time.Now().UnixMicro(), rm, testGasUsed, testGasUnitPrice, node0MaxGas, "Out of gas", req.Report.Sigs)

		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockSearchTx(t, node0Addr, node0FailedTx)

		// Our submission also fails → lands in post-submission failure path
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransactionByHashFailed("0xmine", testGasUsed, testGasUnitPrice, "Out of gas")

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)

		// Post-submission failure path finds node 0's failed tx and returns it
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xnode0oog", *result.Response.TxHash)
		require.Equal(t, "Out of gas", *result.Response.ErrorMessage)
		require.Nil(t, result.Response.ReceiverContractExecutionStatus)
		require.Empty(t, result.ResponseMetadata.Metering)
	})

	t.Run("Node 0 has no matching failed tx - proceed to submit", func(t *testing.T) {
		rm, reqMeta, req := newReportFixture(t)
		transmissionIDStr := computeTransmissionIDStr(t, rm)
		h, node0Addr := newMultiNodeTestHelper(t, transmissionIDStr)

		otherRM, _, _ := newReportFixture(t)
		// Use old timestamp so GetFailedTransmissionInfo's phase 2 pagination is skipped
		oldTs := time.Now().Add(-2 * time.Minute).UnixMicro()
		unrelatedTx := buildFakeTransaction(t, "0xunrelated", false, 100, oldTs, otherRM)

		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockSearchTx(t, node0Addr, unrelatedTx) // pre-submission and post-submission: no match (unrelated tx)
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransactionByHashFailed("0xmine", testGasUsed, testGasUnitPrice, "Move abort")

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
	})

	t.Run("Position 0 - skip pre-submission check entirely", func(t *testing.T) {
		// Single-node helper puts us at position 0
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockTransmission(TransmissionInfo{Success: false}).Once()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxSuccess, TxHash: "0xabc"}, nil)
		h.mockTransmission(TransmissionInfo{Success: true, Transmitter: testTransmitter})
		h.mockTransactionByHash("0xabc", testGasUsed, testGasUnitPrice)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		h.forwarderClient.AssertNotCalled(t, "GetTransmitterTransactions", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("orderedTransmitters[0] is empty - skip check, submit", func(t *testing.T) {
		rm, reqMeta, req := newReportFixture(t)
		transmissionIDStr := computeTransmissionIDStr(t, rm)
		lggr := logger.Test(t)

		myPeerID, otherPeerID := p2ptypes.PeerID{2}, p2ptypes.PeerID{1}
		myAddr := aptos_sdk.AccountAddress{0xDD}
		// Only our own peer is in p2pConfig — otherPeerID maps to empty string in orderedTransmitters
		p2pCfg := map[string]string{fmt.Sprintf("%x", myPeerID[:]): myAddr.StringLong()}
		scheduler := ts.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, 1*time.Second, 0, lggr)
		if scheduler.GetQueuePosition(transmissionIDStr) == 0 {
			myPeerID, otherPeerID = otherPeerID, myPeerID
			p2pCfg = map[string]string{fmt.Sprintf("%x", myPeerID[:]): myAddr.StringLong()}
			scheduler = ts.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{otherPeerID, myPeerID}, 1*time.Second, 0, lggr)
		}
		require.Greater(t, scheduler.GetQueuePosition(transmissionIDStr), 0)

		mockClient := NewCREForwarderClient_mock(t)
		mockService := mocks.NewAptosService(t)
		a := &Aptos{
			AptosService: mockService, forwarderClient: mockClient, forwarderAddress: testForwarderAddr,
			lggr: logger.Sugared(lggr), p2pConfig: p2pCfg, chainSelector: testChainSelector,
			transmissionScheduler: scheduler, txSearchStartingBuffer: 1 * time.Minute,
			beholderProcessor: commontest.NopBeholderProcessor{},
			messageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		}
		require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
		h := &testHelper{forwarderClient: mockClient, aptosService: mockService, aptos: a}

		// Permanent false — initial poll waits for stageTimer, then proceeds to submit
		h.mockTransmission(TransmissionInfo{Success: false})
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransactionByHashFailed("0xmine", testGasUsed, testGasUnitPrice, "Out of gas")

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		// Pre-submission check skipped because orderedTransmitters[0] is empty
		h.forwarderClient.AssertNotCalled(t, "GetTransmitterTransactions", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func setupAptosPollTransmissionInfo(t *testing.T) (*writeReport, *CREForwarderClient_mock) {
	t.Helper()
	lggr := logger.Test(t)
	mockClient := NewCREForwarderClient_mock(t)

	var peer0, peer1, peer2, peer3 p2ptypes.PeerID
	peer0[0], peer1[0], peer2[0], peer3[0] = 0x01, 0x02, 0x03, 0x04
	scheduler := ts.NewTransmissionScheduler(
		peer0,
		[]p2ptypes.PeerID{peer0, peer1, peer2, peer3},
		10*time.Millisecond,
		2,
		lggr,
	)

	wr := &writeReport{
		forwarderClient:       mockClient,
		lggr:                  logger.Sugared(lggr),
		transmissionScheduler: scheduler,
		beholderProcessor:     commontest.NopBeholderProcessor{},
		messageBuilder:        monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	}
	return wr, mockClient
}

func TestPollTransmissionInfo_RaceConditions_Aptos(t *testing.T) {
	t.Parallel()

	t.Run("timer returns fresh state via final boundary read", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		wr, mockClient := setupAptosPollTransmissionInfo(t)
		wr.transmissionScheduler.DeltaStage = 150 * time.Millisecond

		var chainStateUpdated atomic.Bool
		go func() {
			time.Sleep(120 * time.Millisecond)
			chainStateUpdated.Store(true)
		}()

		mockClient.EXPECT().
			GetTransmissionInfo(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, TransmissionID) (TransmissionInfo, error) {
				if chainStateUpdated.Load() {
					return TransmissionInfo{Success: true, Transmitter: testTransmitter}, nil
				}
				return TransmissionInfo{Success: false}, nil
			}).
			Maybe()

		info, err := wr.pollTransmissionInfo(ctx, TransmissionID{}, 1, monitoring.TelemetryContext{})
		require.NoError(t, err)
		require.True(t, chainStateUpdated.Load(), "chain state should have updated before stage timer returned")
		require.True(t, info.Success)
	})

	t.Run("all rpc errors including boundary read return error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		wr, mockClient := setupAptosPollTransmissionInfo(t)
		wr.transmissionScheduler.DeltaStage = 50 * time.Millisecond

		var rpcCalls atomic.Int64
		mockClient.EXPECT().
			GetTransmissionInfo(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, TransmissionID) (TransmissionInfo, error) {
				rpcCalls.Add(1)
				return TransmissionInfo{}, errors.New("rpc unavailable")
			}).
			Maybe()

		_, err := wr.pollTransmissionInfo(ctx, TransmissionID{}, 2, monitoring.TelemetryContext{})
		require.Greater(t, rpcCalls.Load(), int64(0))
		require.Error(t, err)
	})
}

func TestReceiverContractExecutionStatusFromFailedVmStatus(t *testing.T) {
	t.Parallel()

	fwd := aptos_sdk.AccountAddress{0xAA}
	receiverAddr := "0x2e8f43a1266b6b513741a7101ac18ad59de61f068bd13d8a26ff742f7528f052"
	vmReceiver := fmt.Sprintf("Move abort in %s::receiver: E_RECEIVER_FAILURE(0x64):", receiverAddr)

	t.Run("receiver module abort yields REVERTED", func(t *testing.T) {
		st := receiverContractExecutionStatusFromFailedVMStatus(vmReceiver, fwd)
		require.NotNil(t, st)
		require.Equal(t, aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, *st)
	})

	t.Run("forwarder module abort leaves unset", func(t *testing.T) {
		vmFwd := fmt.Sprintf("Move abort in %s::forwarder: E_SOMETHING(0x1):", fwd.StringLong())
		require.Nil(t, receiverContractExecutionStatusFromFailedVMStatus(vmFwd, fwd))
	})

	t.Run("non-move-abort leaves unset", func(t *testing.T) {
		require.Nil(t, receiverContractExecutionStatusFromFailedVMStatus("out of gas", fwd))
	})

	t.Run("malformed move abort leaves unset", func(t *testing.T) {
		require.Nil(t, receiverContractExecutionStatusFromFailedVMStatus("move abort in notanaddress::m:", fwd))
	})
}
