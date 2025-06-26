package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	_ "github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"

	evmcommon "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lggr := logger.Test(t)
	t.Run("Invalid receiver address", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		_, err := service.WriteReport(ctx, capabilities.RequestMetadata{}, &evm.WriteReportRequest{
			Report: &evm.SignedReport{
				RawReport:     []byte{},
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte{},
			},
		})
		require.Error(t, err)
		require.Equal(t, "received address is not 20 bytes long. Address in HEX: ", err.Error())
	})
	t.Run("Invalid report metadata", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		_, err := service.WriteReport(ctx, capabilities.RequestMetadata{}, &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     []byte{},
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte{},
			},
		})
		require.Error(t, err)
		require.Equal(t, "metadata: raw too short, want ≥109, got 0", err.Error())
	})
	t.Run("Report ID does not matches metadata Report ID", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            test.RandomBytes(10),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reportID in the report does not match ReportID in the inputs.")
	})

	t.Run("Report signatures are not empty", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    [][]byte{},
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.Error(t, err)
		require.Contains(t, "no signatures provided", err.Error())
	})
	t.Run("Invalid request metadata", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		reportMetadata.Version = 20
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            test.RandomBytes(10),
			},
		})
		require.Error(t, err)
		require.Contains(t, "unsupported report version: 20", err.Error())
	})
	t.Run("Workflow names do not match", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		workflowName := [10]byte(test.RandomBytes(10))
		reportMetadata.WorkflowName = hex.EncodeToString(workflowName[:])
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
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
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
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
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata.")
	})
}

func TestWriteReport_ExecuteWriteReport(t *testing.T) {
	// t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testLogger := logger.Test(t)

	t.Run("Fail with invalid workflow execution id", func(t *testing.T) {
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		expectedError := "some error"
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(contracts.TransmissionInfo{}, errors.New(expectedError))
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), expectedError)
	})
	t.Run("Fail while getting transmission info", func(t *testing.T) {
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		expectedError := "some error"
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(contracts.TransmissionInfo{}, errors.New(expectedError))
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), expectedError)
	})

	t.Run("TX already transmitted successfully", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil)

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
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, txHash).Return(&receipt, nil)

		evmServiceMock.EXPECT().CalculateTransactionFee(ctx, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcommon.TxStatus_TX_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
		}, txResult)
	})
	t.Run("TX already transmitted successfully - Failed to fetch transmission details", func(t *testing.T) {
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		expectedError := "Error getting transmission info"
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(contracts.TransmissionInfo{}, errors.New(expectedError))

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})
	t.Run("TX already transmitted successfully - Getting invalid state in transmission info", func(t *testing.T) {
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		const invalidState = TransmissionStateSucceeded + 10
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           invalidState,
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:     evmcommon.TxStatus_TX_FATAL,
			ErrorMessage: ptr(getInvalidStateErrorMessage(invalidState)),
		}, txResult)
	})
	t.Run("TX already transmitted successfully - Failed to fetch report emitted log", func(t *testing.T) {
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil)

		expectedError := "Error getting report emitted log"
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New(expectedError))

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})
	t.Run("TX already transmitted successfully - Failed to fetch transaction receipt", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{
			TxHash: txHash,
		})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		expectedError := "Error getting tx receipt"
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, txHash).Return(nil, errors.New(expectedError))

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})
	t.Run("TX already transmitted successfully - Receiver contract reverted - enough gas", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateFailed,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil)

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
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, txHash).Return(&receipt, nil)

		evmServiceMock.EXPECT().CalculateTransactionFee(ctx, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcommon.TxStatus_TX_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_REVERTED.Enum().Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
		}, txResult)
	})
	t.Run("TX already transmitted successfully - Receiver contract reverted - not enough gas", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		signedReport := &evm.SignedReport{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Signatures:    generateRandomSignatures(),
			Id:            []byte(reportMetadata.ReportID),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)
		transmissionInfo := contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateFailed,
			GasLimit:        big.NewInt(NotEnoughReceiverGas),
		}

		mockForwarderClient.On("GetTransmissionInfo", ctx, transmissionID).Return(transmissionInfo, nil).Once()

		retryTxHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", ctx, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:   retryTxHash,
			TxStatus: evmtypes.TxSuccess,
		}, nil)

		retryTransmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, transmissionID).Return(retryTransmissionInfo, nil)

		retryReceipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            retryTxHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(3),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, retryTxHash).Return(&retryReceipt, nil)

		retryTxFee := int64(3000)
		evmServiceMock.EXPECT().CalculateTransactionFee(ctx, toReceiptGasInfo(retryReceipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(retryTxFee),
		}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcommon.TxStatus_TX_SUCCESS,
			TxHash:                          retryReceipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
		}, txResult)
	})
	t.Run("TX already transmitted successfully - Invalid receiver", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: true,
			State:           TransmissionStateInvalidReceiver,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil)

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
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, txHash).Return(&receipt, nil)

		evmServiceMock.EXPECT().CalculateTransactionFee(ctx, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		receiver := testutils.NewAddress().Bytes()
		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: receiver,
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcommon.TxStatus_TX_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_REVERTED.Enum().Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
			ErrorMessage:                    getInvalidReceiverMessage(receiver),
		}, txResult)
	})
	t.Run("TX first transmission - Successful TX execution", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		signedReport := &evm.SignedReport{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Signatures:    generateRandomSignatures(),
			Id:            []byte(reportMetadata.ReportID),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		// transmissionID, _ := getTransmissionID(capabilitiesMetadata.WorkflowExecutionID, writeReportRequest)
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", ctx, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:   txHash,
			TxStatus: evmtypes.TxSuccess,
		}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil).Once()

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, txHash).Return(&receipt, nil)

		retryTxFee := int64(2000)
		evmServiceMock.EXPECT().CalculateTransactionFee(ctx, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(retryTxFee),
		}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcommon.TxStatus_TX_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
		}, txResult)
	})
	t.Run("TX first transmission - Error submitting TX", func(t *testing.T) {
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		signedReport := &evm.SignedReport{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Signatures:    generateRandomSignatures(),
			Id:            []byte(reportMetadata.ReportID),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		expectedError := "Error sending tx to write report"

		mockForwarderClient.On("InvokeOnReport", ctx, receiverAddress, signedReport, mock.Anything).Return(nil, errors.New(expectedError))

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:     evmcommon.TxStatus_TX_FATAL,
			ErrorMessage: &expectedError,
		}, txResult)
	})
	t.Run("TX first transmission - Failed to execute receiver contract", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)
		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		signedReport := &evm.SignedReport{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Signatures:    generateRandomSignatures(),
			Id:            []byte(reportMetadata.ReportID),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", ctx, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:   txHash,
			TxStatus: evmtypes.TxSuccess,
		}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateFailed,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil).Once()

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, txHash).Return(&receipt, nil)

		retryTxFee := int64(2000)
		evmServiceMock.EXPECT().CalculateTransactionFee(ctx, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(retryTxFee),
		}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcommon.TxStatus_TX_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_REVERTED.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
			ErrorMessage:                    ptr(UnknownIssueExecutingReceiverContractMessage),
		}, txResult)
	})

	t.Run("TX first transmission - Invalid receiver", func(t *testing.T) {
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		signedReport := &evm.SignedReport{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Signatures:    generateRandomSignatures(),
			Id:            []byte(reportMetadata.ReportID[:]),
		}
		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.On("InvokeOnReport", ctx, receiverAddress, signedReport, mock.Anything).Return(&evmtypes.TransactionResult{
			TxHash:   txHash,
			TxStatus: evmtypes.TxSuccess,
		}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           TransmissionStateInvalidReceiver,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil).Once()

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(ctx, txHash).Return(&receipt, nil)

		retryTxFee := int64(2000)

		evmServiceMock.EXPECT().CalculateTransactionFee(ctx, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(retryTxFee),
		}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcommon.TxStatus_TX_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_REVERTED.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
			ErrorMessage:                    getInvalidReceiverMessage(receiverAddress[:]),
		}, txResult)
	})

	t.Run("TX first transmission - Unexpected transmission state", func(t *testing.T) {
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		const invalidState = TransmissionStateSucceeded + 10
		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           invalidState,
		}
		mockForwarderClient.On("GetTransmissionInfo", ctx, mock.Anything).Return(transmissionInfo, nil)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &evm.SignedReport{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Signatures:    generateRandomSignatures(),
				Id:            []byte(reportMetadata.ReportID),
			},
		})
		require.NoError(t, err)
		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:     evmcommon.TxStatus_TX_FATAL,
			ErrorMessage: ptr(getInvalidStateErrorMessage(invalidState)),
		}, txResult)
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
	service := EVM{
		keystoneForwarderAddress: common.BytesToAddress(test.RandomBytes(20)),
		forwarderClient:          mockForwarderClient,
		lggr:                     lggr,
		EVMService:               mockEVMService,
		ReceiverGasMinimum:       ConfiguredReceiverGasMinimum,
	}
	return mockEVMService, mockForwarderClient, &service
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

func generateRandomSignatures() [][]byte {
	return [][]byte{
		{0x01, 0x02, 0x03, 0x04},
		{0xAA, 0xBB, 0xCC, 0xDD},
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
