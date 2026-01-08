package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	_ "github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"

	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/stretchr/testify/mock"

	mocks2 "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/stretchr/testify/require"
)

const (
	ConfiguredReceiverGasMinimum = 1000
	EnoughReceiverGas            = ConfiguredReceiverGasMinimum + 1
	NotEnoughReceiverGas         = ConfiguredReceiverGasMinimum - 1
)

func TestWriteReport_InputValidation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	lggr := logger.Test(t)
	t.Run("Invalid receiver address", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		_, err := service.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evm.WriteReportRequest{
			Report: &workflowpb.ReportResponse{
				Sigs: generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Equal(t, "[2]Unknown: received address is not 20 bytes long. Address in HEX: ", err.Error())
	})
	t.Run("Invalid report metadata", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		_, err := service.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				Sigs: generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Equal(t, "[2]Unknown: metadata: raw too short, want ≥109, got 0", err.Error())
	})

	t.Run("Report signatures are not empty", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          []*workflowpb.AttributedSignature{},
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no signatures provided")
	})
	t.Run("Invalid request metadata", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		reportMetadata.Version = 20
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, "[2]Unknown: unsupported report version: 20", err.Error())
	})
	t.Run("Workflow names do not match", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		workflowName := [10]byte(test.RandomBytes(10))
		reportMetadata.WorkflowName = hex.EncodeToString(workflowName[:])
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowName in the report does not match WorkflowName in the request metadata.")
	})
	t.Run("Workflow IDs do not match", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		workflowID := [32]byte(test.RandomBytes(32))
		reportMetadata.WorkflowID = hex.EncodeToString(workflowID[:])
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowID in the report does not match WorkflowID in the request metadata.")
	})
	t.Run("Workflow execution IDs do not match and workflow name less than 10 characters work", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		reportMetadata.WorkflowName = "12345"
		encodedReportMetadata, _ := reportMetadata.Encode()
		workflowID := [32]byte(test.RandomBytes(32))
		reportMetadata.ExecutionID = hex.EncodeToString(workflowID[:])
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata.")
	})
}

func TestWriteReport_ExecuteWriteReport(t *testing.T) {
	t.Parallel()

	t.Run("Fail with invalid workflow execution id", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		errorMsg := "some error"
		expectedError := "[2]Unknown: some error"
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(contracts.TransmissionInfo{}, errors.New(errorMsg))
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Equal(t, expectedError, err.Error())
	})
	t.Run("Fail while getting transmission info", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		errorMsg := "some error"
		expectedError := "[2]Unknown: some error"
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(contracts.TransmissionInfo{}, errors.New(errorMsg))
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Equal(t, expectedError, err.Error())
	})

	t.Run("TX already transmitted successfully", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, mock.Anything).Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).Maybe()

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{
			TxHash: txHash,
		})

		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)

		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
		}, txResult.Response)
		require.Len(t, txResult.ResponseMetadata.Metering, 0)
	})
	t.Run("TX already transmitted successfully - Failed to fetch transmission details", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		expectedError := "Error getting transmission info"
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(contracts.TransmissionInfo{}, errors.New(expectedError))

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})
	t.Run("TX already transmitted successfully - Getting invalid state in transmission info", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		const invalidState = TransmissionStateSucceeded + 10
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           invalidState,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
	})
	t.Run("TX already transmitted successfully - Failed to fetch report emitted log", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		expectedError := "Error getting report emitted log"
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New(expectedError))

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})
	t.Run("TX already transmitted successfully - Failed to fetch transaction receipt", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{
			TxHash: txHash,
		})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		expectedError := "Error getting tx receipt"
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(nil, errors.New(expectedError))

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})
	t.Run("TX already transmitted successfully - Receiver contract reverted", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, mock.Anything).Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).Maybe()

		transmissionInfo := contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateFailed,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}

		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)

		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(2000)}, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
			GasConfig: &evm.GasConfig{GasLimit: 100 * EnoughReceiverGas},
		})
		require.NoError(t, err)

		errMsg := func() *string { s := "Receiver contract execution failure"; return &s }()
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
			ErrorMessage:                    errMsg,
		}, txResult.Response)
		require.Len(t, txResult.ResponseMetadata.Metering, 0)
	})
	t.Run("TX already transmitted successfully - Invalid receiver", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, mock.Anything).Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).Maybe()

		transmissionInfo := contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: true,
			State:           TransmissionStateInvalidReceiver,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{
			TxHash: txHash,
		})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)

		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		receiver := testutils.NewAddress().Bytes()
		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: receiver,
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
			ErrorMessage:                    getInvalidReceiverMessage(receiver),
		}, txResult.Response)
		require.Len(t, txResult.ResponseMetadata.Metering, 0)
	})
	t.Run("TX first transmission - Successful TX execution", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, mock.Anything).Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).Maybe()

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		// transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:           txHash,
			TxStatus:         evmtypes.TxSuccess,
			TxIdempotencyKey: "test-idempotency-key",
		}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil).Once()

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)

		retryTxFee := int64(2000)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(retryTxFee),
		}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
		}, txResult.Response)
		test.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})
	t.Run("TX first transmission - Error submitting TX", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		expectedError := "Error sending tx to write report"

		mockForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, mock.Anything).Return(nil, errors.New(expectedError))

		_, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.Error(t, err)
	})
	t.Run("TX first transmission - Failed to get transmission info and then succeed", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, mock.Anything).Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).Maybe()

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Twice()
		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:           txHash,
			TxStatus:         evmtypes.TxSuccess,
			TxIdempotencyKey: "test-idempotency-key",
		}, nil)
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil).Once()
		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)

		retryTxFee := int64(2000)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(retryTxFee),
		}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
			ErrorMessage:                    nil,
		}, txResult.Response)
		test.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("TX first transmission - Invalid receiver", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, mock.Anything).Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).Maybe()

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:           txHash,
			TxStatus:         evmtypes.TxSuccess,
			TxIdempotencyKey: "test-idempotency-key",
		}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateInvalidReceiver,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil).Once()

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)

		retryTxFee := int64(2000)

		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(retryTxFee),
		}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
			ErrorMessage:                    getInvalidReceiverMessage(receiverAddress[:]),
		}, txResult.Response)
		test.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("TX first transmission - Unexpected transmission state", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		const invalidState = TransmissionStateSucceeded + 10
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           invalidState,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
	})
	t.Run("TX already transmitted successfully - Receiver contract reverted - sets default error message when nil", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, mock.Anything).Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).Maybe()

		transmissionInfo := contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateFailed,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{
			TxHash: txHash,
		})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
			GasConfig: &evm.GasConfig{
				GasLimit: 100 * EnoughReceiverGas,
			},
		})
		require.NoError(t, err)

		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
			ErrorMessage:                    ptr("Receiver contract execution failure"),
		}, txResult.Response)

		require.Len(t, txResult.ResponseMetadata.Metering, 0)
	})

}

func TestGetTransmissionID(t *testing.T) {
	t.Parallel()
	workflowExecutionID := hex.EncodeToString(test.RandomBytes(32))
	request := &evm.WriteReportRequest{}

	t.Run("Successfully creates transmission ID", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, err := reportMetadata.Encode()
		require.NoError(t, err)

		workflowExecutionID := reportMetadata.ExecutionID
		request = &evm.WriteReportRequest{
			Receiver: test.RandomBytes(20),
			Report: &workflowpb.ReportResponse{
				RawReport: encodedReportMetadata,
			},
		}

		transmissionID, err := getTransmissionID(workflowExecutionID, request)
		require.NoError(t, err)

		expectedReceiver := common.BytesToAddress(request.Receiver)
		expectedWorkflowID, _ := hex.DecodeString(workflowExecutionID)
		expectedReportID, _ := hex.DecodeString(reportMetadata.ReportID)

		require.Equal(t, expectedReceiver, transmissionID.Receiver)
		require.Equal(t, [32]byte(expectedWorkflowID), transmissionID.WorkflowExecutionID)
		require.Equal(t, [2]byte(expectedReportID), transmissionID.ReportID)
	})

	t.Run("Fails when decodeReportMetadata returns error", func(t *testing.T) {
		request.Report = &workflowpb.ReportResponse{RawReport: []byte("invalid report data")}

		_, err := getTransmissionID(workflowExecutionID, request)
		require.Error(t, err)
	})

	t.Run("Fails when workflow execution ID is invalid hex", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, err := reportMetadata.Encode()
		require.NoError(t, err)

		invalidWorkflowExecutionID := "invalid-hex-string"
		request := &evm.WriteReportRequest{
			Receiver: test.RandomBytes(20),
			Report: &workflowpb.ReportResponse{
				RawReport: encodedReportMetadata,
			},
		}

		_, err = getTransmissionID(invalidWorkflowExecutionID, request)
		require.Error(t, err)
	})
}

func toReceiptGasInfo(receipt evmtypes.Receipt) evmtypes.ReceiptGasInfo {
	return evmtypes.ReceiptGasInfo{
		GasUsed:           receipt.GasUsed,
		EffectiveGasPrice: receipt.EffectiveGasPrice,
	}
}

func createMocksAndCapability(t *testing.T, lggr logger.Logger) (*mocks2.EVMService, *mocks.CREForwarderClient, *EVM) {
	mockEVMService := mocks2.NewEVMService(t)
	mockForwarderClient := mocks.NewCREForwarderClient(t)
	mockEVMService.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(&evmtypes.HeaderByNumberReply{Header: &evmtypes.Header{Number: big.NewInt(100)}}, nil).Maybe()
	service := &EVM{
		keystoneForwarderAddress: common.BytesToAddress(test.RandomBytes(20)),
		forwarderClient:          mockForwarderClient,
		lggr:                     logger.Sugared(lggr),
		EVMService:               mockEVMService,
		ReceiverGasMinimum:       ConfiguredReceiverGasMinimum,
		chainSelector:            1,
		beholderProcessor:        test.NopBeholderProcessor{},
		messageBuilder:           &monitoring.MessageBuilder{},
	}
	require.NoError(t, service.initLimiters(limits.Factory{Logger: lggr}))
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	require.NotNil(t, service.txGasLimit)
	return mockEVMService, mockForwarderClient, service
}

func equalWriteReportReply(t *testing.T, expected *evm.WriteReportReply, actual *evm.WriteReportReply) {
	require.Equal(t, expected.TxStatus.Enum(), actual.TxStatus.Enum())
	require.Equal(t, expected.TxHash, actual.TxHash)
	require.Equal(t, expected.TransactionFee, actual.TransactionFee)
	if expected.ReceiverContractExecutionStatus != nil {
		require.NotNil(t, actual.ReceiverContractExecutionStatus)
		require.Equal(t, expected.ReceiverContractExecutionStatus.Enum(), actual.ReceiverContractExecutionStatus.Enum())
	}
	require.Equal(t, expected.ErrorMessage, actual.ErrorMessage)
}

func generateRandomSignatures() []*workflowpb.AttributedSignature {
	return []*workflowpb.AttributedSignature{
		{Signature: []byte{0x01, 0x02, 0x03, 0x04}},
		{Signature: []byte{0xAA, 0xBB, 0xCC, 0xDD}},
	}
}

func createTestReportMetadata() ocrtypes.Metadata {
	return ocrtypes.Metadata{
		Version:          1,
		ExecutionID:      hex.EncodeToString(test.RandomBytes(32)),
		Timestamp:        1000,
		DONID:            10,
		DONConfigVersion: 2,
		WorkflowID:       hex.EncodeToString(test.RandomBytes(32)),
		WorkflowName:     hex.EncodeToString(test.RandomBytes(10)),
		WorkflowOwner:    hex.EncodeToString(test.RandomBytes(20)),
		ReportID:         hex.EncodeToString(test.RandomBytes(2)),
	}
}

func createTestRequestMetadata(metadata ocrtypes.Metadata) capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		WorkflowID:               metadata.WorkflowID,
		WorkflowOwner:            metadata.WorkflowOwner,
		WorkflowName:             metadata.WorkflowName,
		WorkflowDonID:            metadata.DONID,
		WorkflowDonConfigVersion: metadata.DONConfigVersion,
		WorkflowExecutionID:      metadata.ExecutionID,
	}
}

func TestDecodeReportMetadata(t *testing.T) {
	t.Parallel()

	t.Run("Successfully decode valid report metadata", func(t *testing.T) {
		originalMetadata := createTestReportMetadata()
		encodedData, err := originalMetadata.Encode()
		require.NoError(t, err)

		decodedMetadata, err := decodeReportMetadata(encodedData)
		require.NoError(t, err)
		require.Equal(t, originalMetadata.Version, decodedMetadata.Version)
		require.Equal(t, originalMetadata.ExecutionID, decodedMetadata.ExecutionID)
		require.Equal(t, originalMetadata.ReportID, decodedMetadata.ReportID)
		require.Equal(t, originalMetadata.WorkflowID, decodedMetadata.WorkflowID)
		require.Equal(t, originalMetadata.WorkflowOwner, decodedMetadata.WorkflowOwner)
	})

	t.Run("Fail to decode invalid data", func(t *testing.T) {
		invalidData := []byte("invalid data")
		_, err := decodeReportMetadata(invalidData)
		require.Error(t, err)
		require.Contains(t, err.Error(), "metadata: raw too short")
	})

	t.Run("Fail to decode empty data", func(t *testing.T) {
		emptyData := []byte{}
		_, err := decodeReportMetadata(emptyData)
		require.Error(t, err)
	})
}

func TestExecuteWriteReport_MeteringMetadata(t *testing.T) {
	t.Parallel()

	t.Run("Successful transaction includes metering metadata", func(t *testing.T) {
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)

		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:           txHash,
			TxStatus:         evmtypes.TxSuccess,
			TxIdempotencyKey: "test-idempotency-key",
		}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil).Once()

		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, "test-idempotency-key").Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		reply, responseMetadata, err := service.executeWriteReport(ctx, writeReportRequest, capabilitiesMetadata, monitoring.TelemetryContext{})
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, reply.TxStatus)

		require.NotNil(t, responseMetadata.Metering)
		require.NotEmpty(t, responseMetadata.Metering)
	})

	t.Run("Transaction fee calculation failure still returns response", func(t *testing.T) {
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)

		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:           txHash,
			TxStatus:         evmtypes.TxSuccess,
			TxIdempotencyKey: "test-idempotency-key",
		}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil).Once()

		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, "test-idempotency-key").Return(nil, errors.New("fee calculation failed"))

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		reply, responseMetadata, err := service.executeWriteReport(ctx, writeReportRequest, capabilitiesMetadata, monitoring.TelemetryContext{})
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, reply.TxStatus)

		require.Empty(t, responseMetadata.Metering)
	})
}

func TestExecuteWriteReport_TransmissionStates(t *testing.T) {
	t.Parallel()

	t.Run("Already succeeded transmission returns empty metering metadata", func(t *testing.T) {
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)

		// Mock GetTransmissionInfo - already succeeded
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{
			TxHash: txHash,
		})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reply, responseMetadata, err := service.executeWriteReport(ctx, writeReportRequest, capabilitiesMetadata, monitoring.TelemetryContext{})
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, reply.TxStatus)

		// Verify empty metering metadata for pre-existing successful transactions
		require.Empty(t, responseMetadata.Metering)
	})

	t.Run("GetTransmissionInfo failure returns error with empty metadata", func(t *testing.T) {
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)

		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})

		expectedError := "transmission info error"
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(contracts.TransmissionInfo{}, errors.New(expectedError))

		reply, responseMetadata, err := service.executeWriteReport(ctx, writeReportRequest, capabilitiesMetadata, monitoring.TelemetryContext{})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
		require.Nil(t, reply)
		require.Empty(t, responseMetadata.Metering)
	})
}

func TestWriteReport_BillingMetadata(t *testing.T) {
	t.Parallel()

	t.Run("Successful WriteReport includes billing metadata", func(t *testing.T) {
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)

		// Mock GetTransmissionInfo for initial check
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		// Mock InvokeOnReport
		mockForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:           txHash,
			TxStatus:         evmtypes.TxSuccess,
			TxIdempotencyKey: "test-idempotency-key",
		}, nil)

		// Mock GetTransmissionInfo for retry logic
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil).Once()

		// Mock transaction fee calculation
		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, "test-idempotency-key").Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(3000),
		}, nil)

		// Mock receipt fetching
		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(3),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(3000),
		}, nil)

		result, err := service.WriteReport(t.Context(), capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Response)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)

		// Verify billing metadata is included in the response
		require.NotNil(t, result.ResponseMetadata.Metering)
		require.NotEmpty(t, result.ResponseMetadata.Metering)

		// Verify the metering contains the expected chain selector in the SpendUnit
		meteringData := result.ResponseMetadata.Metering
		require.Len(t, meteringData, 1)
		require.Equal(t, "GAS.1", meteringData[0].SpendUnit) // Chain selector 1 from createMocksAndCapability
		require.NotEmpty(t, meteringData[0].SpendValue)      // Should contain the transaction fee
	})

	t.Run("WriteReport with pre-existing successful transaction has empty billing metadata", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)

		// Mock GetTransmissionInfo - already succeeded
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{
			TxHash: txHash,
		})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		result, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Response)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)

		// Verify empty billing metadata for pre-existing successful transactions
		require.Empty(t, result.ResponseMetadata.Metering)
	})
}
