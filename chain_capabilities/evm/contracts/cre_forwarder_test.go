package contracts_test

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	mocks2 "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"
)

const LatestBlock = -2

func TestCREForwarderClient_GetTransmissionInfo(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testLogger := logger.Test(t)
	forwarderAddress := common.BytesToAddress(test.RandomBytes(20))
	transmitterAddress := common.BytesToAddress(test.RandomBytes(20))
	expectedBlockNumberForGetTransmission := big.NewInt(LatestBlock)
	forwarderABI, _ := forwarder.KeystoneForwarderMetaData.GetAbi()
	testEncoder := TestEncoder{abi: *forwarderABI}

	t.Run("Get Transmission info - Successfully get transmission info", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)

		expectedTransmissionInfo := contracts.TransmissionInfo{
			State:           2,
			Success:         true,
			InvalidReceiver: false,
			TransmissionId:  [32]byte(test.RandomBytes(32)),
			Transmitter:     transmitterAddress,
			GasLimit:        &big.Int{},
		}
		output := testEncoder.encodeTransmissionInfo(expectedTransmissionInfo)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.BytesToAddress(test.RandomBytes(20)),
			ReportID:            [2]byte(test.RandomBytes(2)),
			WorkflowExecutionID: [32]byte(test.RandomBytes(32)),
		}
		encodedData := testEncoder.encodeTransmissionIDCall(transmissionID)

		mockEVMService.EXPECT().CallContract(ctx, &evmtypes.CallMsg{
			To:   forwarderAddress,
			Data: encodedData,
		}, expectedBlockNumberForGetTransmission).Return(output, nil)
		transmissionInfo, err := forwarderClient.GetTransmissionInfo(ctx, transmissionID)

		require.NoError(t, err)
		require.Equal(t, expectedTransmissionInfo, transmissionInfo)
	})
	t.Run("Get Transmission info - Fail calling CallContract ", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.BytesToAddress(test.RandomBytes(20)),
			ReportID:            [2]byte(test.RandomBytes(2)),
			WorkflowExecutionID: [32]byte(test.RandomBytes(32)),
		}
		encodedData := testEncoder.encodeTransmissionIDCall(transmissionID)

		expectedError := "failed calling call contract"
		mockEVMService.EXPECT().CallContract(ctx, &evmtypes.CallMsg{
			To:   forwarderAddress,
			Data: encodedData,
		}, expectedBlockNumberForGetTransmission).Return([]byte{}, errors.New(expectedError))
		_, err := forwarderClient.GetTransmissionInfo(ctx, transmissionID)

		require.Error(t, err)
		require.Equal(t, expectedError, err.Error())
	})
	t.Run("Get Transmission info - Fail decoding data from call contract", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.BytesToAddress(test.RandomBytes(20)),
			ReportID:            [2]byte(test.RandomBytes(2)),
			WorkflowExecutionID: [32]byte(test.RandomBytes(32)),
		}
		encodedData := testEncoder.encodeTransmissionIDCall(transmissionID)

		mockEVMService.EXPECT().CallContract(ctx, &evmtypes.CallMsg{
			To:   forwarderAddress,
			Data: encodedData,
		}, expectedBlockNumberForGetTransmission).Return(test.RandomBytes(20), nil)
		_, err := forwarderClient.GetTransmissionInfo(ctx, transmissionID)

		require.Error(t, err)
		require.Contains(t, err.Error(), "Failed to decode getTransmissionInfo return data")
	})
}

func TestCREForwarderClient_GetReportProcessedEvents(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testLogger := logger.Test(t)
	forwarderAddress := common.BytesToAddress(test.RandomBytes(20))
	receiverAddress := common.BytesToAddress(test.RandomBytes(20))
	reportID := [2]byte(test.RandomBytes(2))
	workflowExecutionID := [32]byte(test.RandomBytes(32))
	expectedHash := evmtypes.Hash(test.RandomBytes(32))

	t.Run("Get Transmission info - Successfully get transmission info", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)
		mockLogs := []*evm.Log{{
			TxHash: expectedHash,
		}}
		mockEVMService.EXPECT().FilterLogs(ctx, mock.Anything).Return(mockLogs, nil)

		evmLogs, err := forwarderClient.GetReportProcessedEvents(ctx, receiverAddress, workflowExecutionID, reportID)
		require.NoError(t, err)
		require.Equal(t, 1, len(evmLogs))
		require.Equal(t, expectedHash, evmLogs[0].TxHash)
	})
	t.Run("Get Transmission info - Error calling FilterLogs", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)
		expectedError := "fail calling EVM FilterLogs"
		mockEVMService.EXPECT().FilterLogs(ctx, mock.Anything).Return(nil, errors.New(expectedError))

		_, err := forwarderClient.GetReportProcessedEvents(ctx, receiverAddress, workflowExecutionID, reportID)
		require.Error(t, err)
		require.Equal(t, expectedError, err.Error())
	})
}

func TestCREForwarderClient_InvokeOnReport(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testLogger := logger.Test(t)
	forwarderAddress := common.BytesToAddress(test.RandomBytes(20))
	receiverAddress := common.BytesToAddress(test.RandomBytes(20))
	forwarderABI, _ := forwarder.KeystoneForwarderMetaData.GetAbi()
	testEncoder := TestEncoder{abi: *forwarderABI}

	t.Run("Invoke On Report - Successfully send transaction - empty report", func(t *testing.T) {
		report := &evmcap.SignedReport{}
		expectedEncodedReport := testEncoder.encodeReport(receiverAddress, report)

		testSuccessfulReportSubmissionAndEncoding(ctx, t, forwarderAddress, testLogger, expectedEncodedReport, receiverAddress, report)
	})
	t.Run("Invoke On Report - Successfully send transaction - complete report", func(t *testing.T) {
		report := &evmcap.SignedReport{
			RawReport:     test.RandomBytes(100),
			ReportContext: test.RandomBytes(50),
			Signatures:    [][]byte{test.RandomBytes(20), test.RandomBytes(20)},
			Id:            test.RandomBytes(4),
		}

		expectedEncodedReport := testEncoder.encodeReport(receiverAddress, report)

		testSuccessfulReportSubmissionAndEncoding(ctx, t, forwarderAddress, testLogger, expectedEncodedReport, receiverAddress, report)
	})
	t.Run("Invoke On Report - Retry on GasLimit not supported", func(t *testing.T) {
		report := &evmcap.SignedReport{}
		expectedEncodedReport := testEncoder.encodeReport(receiverAddress, report)

		mockEVMService := mocks2.NewEVMService(t)
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)
		expectedGasLimit := uint64(100)
		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockEVMService.EXPECT().SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
			To:   forwarderAddress,
			Data: expectedEncodedReport,
			GasConfig: &evmtypes.GasConfig{
				GasLimit: &expectedGasLimit,
			},
		}).Return(nil, types.ErrSettingTransactionGasLimitNotSupported).Once()

		mockEVMService.EXPECT().SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
			To:   forwarderAddress,
			Data: expectedEncodedReport,
		}).Return(&evmtypes.TransactionResult{
			TxHash:   txHash,
			TxStatus: evmtypes.TxSuccess,
		}, nil)

		txResult, err := forwarderClient.InvokeOnReport(ctx, receiverAddress, report, &evmcap.GasConfig{GasLimit: expectedGasLimit})
		require.NoError(t, err)
		require.Equal(t, &evmtypes.TransactionResult{
			TxHash:   txHash,
			TxStatus: evmtypes.TxSuccess,
		}, txResult)
	})

	t.Run("Invoke On Report - Failed to send transaction", func(t *testing.T) {
		report := &evmcap.SignedReport{}
		expectedEncodedReport := testEncoder.encodeReport(receiverAddress, report)

		mockEVMService := mocks2.NewEVMService(t)
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)
		expectedGasLimit := uint64(100)
		expectedError := "some random error sending TX"

		mockEVMService.EXPECT().SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
			To:   forwarderAddress,
			Data: expectedEncodedReport,
			GasConfig: &evmtypes.GasConfig{
				GasLimit: &expectedGasLimit,
			},
		}).Return(nil, errors.New(expectedError))

		_, err := forwarderClient.InvokeOnReport(ctx, receiverAddress, report, &evmcap.GasConfig{GasLimit: expectedGasLimit})
		require.Error(t, err)
		require.Contains(t, "failed to submit transaction: "+expectedError, err.Error())
	})
}

func testSuccessfulReportSubmissionAndEncoding(ctx context.Context, t *testing.T, forwarderAddress common.Address, testLogger logger.Logger, expectedEncodedReport []byte, receiverAddress common.Address, report *evmcap.SignedReport) {
	mockEVMService := mocks2.NewEVMService(t)
	forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)
	expectedGasLimit := uint64(100)
	txHash := evmtypes.Hash(test.RandomBytes(32))

	mockEVMService.EXPECT().SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
		To:   forwarderAddress,
		Data: expectedEncodedReport,
		GasConfig: &evmtypes.GasConfig{
			GasLimit: &expectedGasLimit,
		},
	}).Return(&evmtypes.TransactionResult{
		TxHash:   txHash,
		TxStatus: evmtypes.TxSuccess,
	}, nil)

	txResult, err := forwarderClient.InvokeOnReport(ctx, receiverAddress, report, &evmcap.GasConfig{GasLimit: expectedGasLimit})
	require.NoError(t, err)
	require.Equal(t, &evmtypes.TransactionResult{
		TxHash:   txHash,
		TxStatus: evmtypes.TxSuccess,
	}, txResult)
}

type TestEncoder struct {
	abi abi.ABI
}

func (t TestEncoder) encodeTransmissionIDCall(transmissionID contracts.TransmissionID) []byte {
	encodedData, _ := t.abi.Pack("getTransmissionInfo", transmissionID.Receiver, transmissionID.WorkflowExecutionID, transmissionID.ReportID)
	return encodedData
}

func (t TestEncoder) encodeTransmissionInfo(transmissionInfo contracts.TransmissionInfo) []byte {
	encodedData, _ := t.abi.Methods["getTransmissionInfo"].Outputs.Pack(transmissionInfo)
	return encodedData
}

func (t TestEncoder) encodeReport(receiver common.Address, report *evmcap.SignedReport) []byte {
	data, _ := t.abi.Pack("report", receiver, report.RawReport, report.ReportContext, report.Signatures)
	return data
}
