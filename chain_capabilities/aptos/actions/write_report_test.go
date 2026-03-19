package actions

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
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
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/metering"
	commontest "github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
)

// --- helpers ---

const (
	testChainSelector = uint64(1)
	testGasUsed       = uint64(500)
	testGasUnitPrice  = uint64(100)
	testFeeOctas      = testGasUsed * testGasUnitPrice
)

var (
	testForwarderAddr = aptos_sdk.AccountAddress{0xAA}
	testReceiver      = aptos_sdk.AccountAddress{0xBB}
	testTransmitter   = aptos_sdk.AccountAddress{0xCC}
)

type testHelper struct {
	forwarderClient *CREForwarderClient_mock
	aptosService    *typesmocks.AptosService
	aptos           *Aptos
}

func newTestHelper(t *testing.T) *testHelper {
	t.Helper()
	lggr := logger.Test(t)
	mockClient := NewCREForwarderClient_mock(t)
	mockService := typesmocks.NewAptosService(t)
	myPeerID := p2ptypes.PeerID{1}

	a := &Aptos{
		AptosService:     mockService,
		forwarderClient:  mockClient,
		forwarderAddress: testForwarderAddr,
		lggr:             logger.Sugared(lggr),
		p2pConfig:        map[string]string{},
		chainSelector:    testChainSelector,
		transmissionScheduler: transmission_schedule.NewTransmissionScheduler(
			myPeerID, []p2ptypes.PeerID{myPeerID}, 1*time.Second, 0, lggr),
	}
	require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
	return &testHelper{forwarderClient: mockClient, aptosService: mockService, aptos: a}
}

func newTwoNodeTestHelperAtQueuePosition(t *testing.T, transmissionIDStr string, desiredPos int) (*testHelper, aptos_sdk.AccountAddress, aptos_sdk.AccountAddress) {
	t.Helper()
	lggr := logger.Test(t)
	mockClient := NewCREForwarderClient_mock(t)
	mockService := typesmocks.NewAptosService(t)

	peerA, peerB := p2ptypes.PeerID{1}, p2ptypes.PeerID{2}
	addrForPeer := map[p2ptypes.PeerID]aptos_sdk.AccountAddress{
		peerA: {0xCC},
		peerB: {0xDD},
	}

	buildP2PConfig := func() map[string]string {
		peerAAddr := addrForPeer[peerA]
		peerBAddr := addrForPeer[peerB]
		return map[string]string{
			fmt.Sprintf("%x", peerA[:]): peerAAddr.StringLong(),
			fmt.Sprintf("%x", peerB[:]): peerBAddr.StringLong(),
		}
	}

	candidates := []p2ptypes.PeerID{peerA, peerB}
	for _, myPeerID := range candidates {
		scheduler := transmission_schedule.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{peerA, peerB}, 15*time.Second, 0, lggr)
		if scheduler.GetQueuePosition(transmissionIDStr) != desiredPos {
			continue
		}
		otherPeerID := peerA
		if myPeerID == peerA {
			otherPeerID = peerB
		}

		a := &Aptos{
			AptosService:          mockService,
			forwarderClient:       mockClient,
			forwarderAddress:      testForwarderAddr,
			lggr:                  logger.Sugared(lggr),
			p2pConfig:             buildP2PConfig(),
			chainSelector:         testChainSelector,
			transmissionScheduler: scheduler,
		}
		require.NoError(t, a.initLimiters(limits.Factory{Logger: lggr}))
		return &testHelper{forwarderClient: mockClient, aptosService: mockService, aptos: a}, addrForPeer[myPeerID], addrForPeer[otherPeerID]
	}

	require.FailNowf(t, "queue position not found", "no two-node scheduler arrangement produced queue position %d", desiredPos)
	return nil, aptos_sdk.AccountAddress{}, aptos_sdk.AccountAddress{}
}

// newMultiNodeTestHelper builds a 2-node DON where our node is at queue position > 0
// for the given transmissionID string. Returns the helper and the prior node's transmitter address.
func newMultiNodeTestHelper(t *testing.T, transmissionIDStr string) (*testHelper, aptos_sdk.AccountAddress) {
	t.Helper()
	h, _, otherAddr := newTwoNodeTestHelperAtQueuePosition(t, transmissionIDStr, 1)
	require.Greater(t, h.aptos.transmissionScheduler.GetQueuePosition(transmissionIDStr), 0)
	return h, otherAddr
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
	return buildFakeTransactionWithDetails(t, txHash, success, seqNum, timestampMicro, reportMetadata, 0, 0, "")
}

func buildFakeTransactionWithGas(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro uint64, reportMetadata ocrtypes.Metadata, gasUsed uint64, gasUnitPrice uint64) *aptostypes.Transaction {
	return buildFakeTransactionWithDetails(t, txHash, success, seqNum, timestampMicro, reportMetadata, gasUsed, gasUnitPrice, "")
}

func buildFakeTransactionWithDetails(t *testing.T, txHash string, success bool, seqNum uint64, timestampMicro uint64, reportMetadata ocrtypes.Metadata, gasUsed uint64, gasUnitPrice uint64, vmStatus string) *aptostypes.Transaction {
	t.Helper()
	encodedReport, err := reportMetadata.Encode()
	require.NoError(t, err)

	rawReportHex := "0x" + hex.EncodeToString(append(make([]byte, 96), encodedReport...))
	functionName := fmt.Sprintf("%s::forwarder::report", testForwarderAddr.String())

	txJSON := fmt.Sprintf(`{
		"Hash": %q, "Success": %t, "SequenceNumber": %d, "Timestamp": %d, "GasUsed": %d, "GasUnitPrice": %d, "VMStatus": %q,
		"Payload": {"Inner": {"Function": %q, "Arguments": ["0x01", %q, "0x01"]}}
	}`, txHash, success, seqNum, timestampMicro, gasUsed, gasUnitPrice, vmStatus, functionName, rawReportHex)

	return &aptostypes.Transaction{Data: []byte(txJSON)}
}

func validateMeteringWriteReport(t *testing.T, metadata capabilities.ResponseMetadata, chainSelector uint64, spendValue string) {
	t.Helper()
	require.Len(t, metadata.Metering, 1)
	require.Equal(t, fmt.Sprintf(metering.WriteReportSpendUnitFormat, chainSelector), metadata.Metering[0].SpendUnit)
	require.Equal(t, spendValue, metadata.Metering[0].SpendValue)
}

func validateWriteReportFee(t *testing.T, result *capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply], feeOctas uint64, spendValue string) {
	t.Helper()
	require.NotNil(t, result.Response.TransactionFee)
	require.Equal(t, feeOctas, *result.Response.TransactionFee)
	validateMeteringWriteReport(t, result.ResponseMetadata, testChainSelector, spendValue)
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

func (h *testHelper) mockTransactionByHash(txHash string, gasUsed uint64, gasUnitPrice uint64) {
	h.mockTransactionByHashWithVMStatus(txHash, gasUsed, gasUnitPrice, "")
}

func (h *testHelper) mockTransactionByHashWithVMStatus(txHash string, gasUsed uint64, gasUnitPrice uint64, vmStatus string) {
	txData := fmt.Sprintf(`{"Hash":%q,"Success":true,"GasUsed":%d,"GasUnitPrice":%d,"VMStatus":%q}`, txHash, gasUsed, gasUnitPrice, vmStatus)
	h.aptosService.On("TransactionByHash", mock.Anything, aptostypes.TransactionByHashRequest{Hash: txHash}).Return(
		&aptostypes.TransactionByHashReply{
			Transaction: &aptostypes.Transaction{Data: []byte(txData)},
		}, nil,
	).Once()
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
		h.mockTransactionByHash("0xabc", testGasUsed, testGasUnitPrice)
		h.mockPostSubmitPoll(TransmissionInfo{Success: true, Transmitter: testTransmitter})

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xabc", *result.Response.TxHash)
		validateWriteReportFee(t, result, testFeeOctas, "0.0005")
	})

	t.Run("Already transmitted - returns without submitting", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, req := newReportFixture(t)
		transmitter := aptos_sdk.AccountAddress{0xCC}

		h.forwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(TransmissionInfo{Success: true, Transmitter: transmitter}, nil)
		h.forwarderClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{buildFakeTransactionWithGas(t, "0xalready", true, 100, unixMicroUint64(t, time.Now()), rm, testGasUsed, testGasUnitPrice)}, nil)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xalready", *result.Response.TxHash)
		validateWriteReportFee(t, result, testFeeOctas, "0.0005")
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
			Return([]*aptostypes.Transaction{buildFakeTransactionWithGas(t, "0xreal", true, 100, unixMicroUint64(t, time.Now()), rm, testGasUsed, testGasUnitPrice)}, nil)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)
		require.Equal(t, "0xreal", *result.Response.TxHash)
		validateWriteReportFee(t, result, testFeeOctas, "0.0005")
	})

	t.Run("Submit fails at node0 - returns own hash", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransactionByHash("0xmine", testGasUsed, testGasUnitPrice)
		h.mockPostSubmitPoll(TransmissionInfo{Success: false})

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xmine", *result.Response.TxHash)
		validateWriteReportFee(t, result, testFeeOctas, "0.0005")
	})

	t.Run("Submit fails at node0 - populates receiver status and error from vm status", func(t *testing.T) {
		h := newTestHelper(t)
		_, reqMeta, req := newReportFixture(t)
		vmStatus := "Move abort in user::receiver::execute: E_TEST_ABORT"

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockTransactionByHashWithVMStatus("0xmine", testGasUsed, testGasUnitPrice, vmStatus)
		h.mockPostSubmitPoll(TransmissionInfo{Success: false})

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xmine", *result.Response.TxHash)
		require.NotNil(t, result.Response.ReceiverContractExecutionStatus)
		require.Equal(t, aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, *result.Response.ReceiverContractExecutionStatus)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Contains(t, *result.Response.ErrorMessage, "receiver execution failed")
		validateWriteReportFee(t, result, testFeeOctas, "0.0005")
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
		h.aptos.transmissionScheduler.F = 1
		h.aptos.transmissionScheduler.DeltaStage = time.Millisecond

		h.mockNoTransmission()
		h.mockInvokeOnReport(&aptostypes.SubmitTransactionReply{TxStatus: aptostypes.TxFatal, TxHash: "0xmine"}, nil)
		h.mockPostSubmitPoll(TransmissionInfo{Success: false})
		h.forwarderClient.On("GetTransmitterTransactions", mock.Anything, node0Addr, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{buildFakeTransactionWithDetails(t, "0xnode0failed", false, 100, unixMicroUint64(t, time.Now()), rm, testGasUsed, testGasUnitPrice, "Out of gas")}, nil)

		result, capErr := h.aptos.WriteReport(t.Context(), reqMeta, req)
		require.Nil(t, capErr)
		require.Equal(t, aptoscap.TxStatus_TX_STATUS_FATAL, result.Response.TxStatus)
		require.Equal(t, "0xnode0failed", *result.Response.TxHash)
		require.NotNil(t, result.Response.ErrorMessage)
		require.Equal(t, "Out of gas", *result.Response.ErrorMessage)
		validateWriteReportFee(t, result, testFeeOctas, "0.0005")
	})
}

func TestGetTransactionFeeOctas(t *testing.T) {
	t.Parallel()

	t.Run("Uses gas info from tx info when available", func(t *testing.T) {
		wr := &writeReport{lggr: logger.Sugared(logger.Test(t))}
		fee, err := wr.getTransactionFeeOctas(t.Context(), txInfo{TxHash: "0xabc", GasUsed: testGasUsed, GasUnitPrice: testGasUnitPrice})
		require.NoError(t, err)
		require.NotNil(t, fee)
		require.Equal(t, testFeeOctas, *fee)
	})

	t.Run("Falls back to RPC when tx info has no fee data", func(t *testing.T) {
		mockService := typesmocks.NewAptosService(t)
		wr := &writeReport{
			aptosService: mockService,
			lggr:         logger.Sugared(logger.Test(t)),
		}

		txData := fmt.Sprintf(`{"Hash":"0xabc","Success":true,"GasUsed":%d,"GasUnitPrice":%d}`, testGasUsed, testGasUnitPrice)
		mockService.On("TransactionByHash", mock.Anything, mock.Anything).Return(
			&aptostypes.TransactionByHashReply{
				Transaction: &aptostypes.Transaction{Data: []byte(txData)},
			}, nil)

		fee, err := wr.getTransactionFeeOctas(t.Context(), txInfo{TxHash: "0xabc"})
		require.NoError(t, err)
		require.NotNil(t, fee)
		require.Equal(t, testFeeOctas, *fee)
	})

	t.Run("Returns error when RPC fails", func(t *testing.T) {
		mockService := typesmocks.NewAptosService(t)
		wr := &writeReport{
			aptosService: mockService,
			lggr:         logger.Sugared(logger.Test(t)),
		}

		mockService.On("TransactionByHash", mock.Anything, mock.Anything).Return(
			(*aptostypes.TransactionByHashReply)(nil), errors.New("rpc error"))

		_, err := wr.getTransactionFeeOctas(t.Context(), txInfo{TxHash: "0xabc"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transaction by hash")
	})
}

func TestWriteReport_HelperValidation(t *testing.T) {
	t.Parallel()

	t.Run("getTransmissionID validates receiver length", func(t *testing.T) {
		_, reqMeta, req := newReportFixture(t)
		req.Receiver = []byte{1, 2, 3}

		_, err := getTransmissionID(reqMeta.WorkflowExecutionID, req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "receiver address must be 32 bytes")
	})

	t.Run("calculateTransactionFeeOctas rejects overflow", func(t *testing.T) {
		_, err := calculateTransactionFeeOctas(math.MaxUint64, 2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "transaction fee exceeds uint64 range")
	})

	t.Run("validateWriteReportInputs catches additional mismatches", func(t *testing.T) {
		h := newTestHelper(t)
		rm, reqMeta, req := newReportFixture(t)

		err := h.aptos.validateWriteReportInputs(reqMeta, &aptoscap.WriteReportRequest{
			Receiver: req.Receiver,
			Report:   &workflowpb.ReportResponse{RawReport: req.Report.RawReport},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no signatures provided")

		err = h.aptos.validateWriteReportInputs(reqMeta, &aptoscap.WriteReportRequest{
			Receiver: req.Receiver,
			Report:   &workflowpb.ReportResponse{RawReport: []byte("bad"), Sigs: generateRandomSignatures()},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to decode report metadata")

		rm.Version = 2
		encoded, encErr := rm.Encode()
		require.NoError(t, encErr)
		err = h.aptos.validateWriteReportInputs(reqMeta, &aptoscap.WriteReportRequest{
			Receiver: req.Receiver,
			Report:   &workflowpb.ReportResponse{RawReport: encoded, Sigs: generateRandomSignatures()},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported report version")

		err = h.aptos.validateWriteReportInputs(capabilities.RequestMetadata{
			WorkflowExecutionID: reqMeta.WorkflowExecutionID,
			WorkflowOwner:       hex.EncodeToString(commontest.RandomBytes(20)),
			WorkflowID:          reqMeta.WorkflowID,
		}, req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowOwner mismatch")
	})
}

func TestResolveTxInfo_AdditionalBranches(t *testing.T) {
	t.Parallel()

	t.Run("returns existing info when already complete", func(t *testing.T) {
		wr := &writeReport{lggr: logger.Sugared(logger.Test(t))}
		info := txInfo{TxHash: "0xabc", GasUsed: 1, GasUnitPrice: 2, VMStatus: "done"}

		resolved, err := wr.resolveTxInfo(t.Context(), info)
		require.NoError(t, err)
		require.Equal(t, info, resolved)
	})

	t.Run("fills missing info from rpc", func(t *testing.T) {
		mockService := typesmocks.NewAptosService(t)
		wr := &writeReport{
			aptosService: mockService,
			lggr:         logger.Sugared(logger.Test(t)),
		}

		txData := fmt.Sprintf(`{"Hash":"0xabc","Success":false,"GasUsed":%d,"GasUnitPrice":%d,"VMStatus":"Move abort"}`, testGasUsed, testGasUnitPrice)
		mockService.On("TransactionByHash", mock.Anything, aptostypes.TransactionByHashRequest{Hash: "0xabc"}).Return(
			&aptostypes.TransactionByHashReply{Transaction: &aptostypes.Transaction{Data: []byte(txData)}}, nil,
		).Once()

		resolved, err := wr.resolveTxInfo(t.Context(), txInfo{TxHash: "0xabc"})
		require.NoError(t, err)
		require.Equal(t, testGasUsed, resolved.GasUsed)
		require.Equal(t, testGasUnitPrice, resolved.GasUnitPrice)
		require.Equal(t, "Move abort", resolved.VMStatus)
	})

	t.Run("returns error on nil rpc reply", func(t *testing.T) {
		mockService := typesmocks.NewAptosService(t)
		wr := &writeReport{
			aptosService: mockService,
			lggr:         logger.Sugared(logger.Test(t)),
		}

		mockService.On("TransactionByHash", mock.Anything, aptostypes.TransactionByHashRequest{Hash: "0xabc"}).Return(
			(*aptostypes.TransactionByHashReply)(nil), nil,
		).Once()

		_, err := wr.resolveTxInfo(t.Context(), txInfo{TxHash: "0xabc"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil transaction by hash reply")
	})

	t.Run("returns error on invalid transaction payload", func(t *testing.T) {
		mockService := typesmocks.NewAptosService(t)
		wr := &writeReport{
			aptosService: mockService,
			lggr:         logger.Sugared(logger.Test(t)),
		}

		mockService.On("TransactionByHash", mock.Anything, aptostypes.TransactionByHashRequest{Hash: "0xabc"}).Return(
			&aptostypes.TransactionByHashReply{Transaction: &aptostypes.Transaction{Data: []byte("{")}}, nil,
		).Once()

		_, err := wr.resolveTxInfo(t.Context(), txInfo{TxHash: "0xabc"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal transaction data")
	})
}
