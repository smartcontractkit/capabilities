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
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	mocks2 "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"
)

const LatestBlock = -2

func TestCREForwarderClient_GetTransmissionInfo(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	testLogger := logger.Test(t)
	forwarderAddress := common.BytesToAddress(test.RandomBytes(20))
	transmitterAddress := common.BytesToAddress(test.RandomBytes(20))
	expectedBlockNumberForGetTransmission := big.NewInt(LatestBlock)
	forwarderABI, _ := forwarder.KeystoneForwarderMetaData.GetAbi()
	testEncoder := TestEncoder{abi: *forwarderABI}

	t.Run("Get Transmission info - Successfully get transmission info", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		mockEVMService.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(&evmtypes.HeaderByNumberReply{Header: &evmtypes.Header{Number: big.NewInt(100)}}, nil).Maybe()
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

		mockEVMService.EXPECT().CallContract(ctx, evmtypes.CallContractRequest{
			Msg: &evmtypes.CallMsg{
				To:   forwarderAddress,
				Data: encodedData,
			},
			BlockNumber: expectedBlockNumberForGetTransmission,
		}).Return(&evmtypes.CallContractReply{Data: output}, nil)
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
		mockEVMService.EXPECT().CallContract(ctx, evmtypes.CallContractRequest{
			Msg: &evmtypes.CallMsg{
				To:   forwarderAddress,
				Data: encodedData,
			},
			BlockNumber: expectedBlockNumberForGetTransmission,
		}).Return(nil, errors.New(expectedError))
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

		mockEVMService.EXPECT().CallContract(ctx, evmtypes.CallContractRequest{
			Msg: &evmtypes.CallMsg{
				To:   forwarderAddress,
				Data: encodedData,
			},
			BlockNumber: expectedBlockNumberForGetTransmission,
		}).Return(&evmtypes.CallContractReply{Data: test.RandomBytes(20)}, nil)
		_, err := forwarderClient.GetTransmissionInfo(ctx, transmissionID)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to abi unpack getTransmissionInfo return data")
	})
}

func TestCREForwarderClient_GetReportProcessedEvents(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	testLogger := logger.Test(t)
	forwarderAddress := common.BytesToAddress(test.RandomBytes(20))
	receiverAddress := common.BytesToAddress(test.RandomBytes(20))
	reportID := [2]byte(test.RandomBytes(2))
	workflowExecutionID := [32]byte(test.RandomBytes(32))
	expectedHash := evmtypes.Hash(test.RandomBytes(32))

	t.Run("Get Transmission info - Successfully get transmission info", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		mockEVMService.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(&evmtypes.HeaderByNumberReply{Header: &evmtypes.Header{Number: big.NewInt(100)}}, nil).Maybe()
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, testLogger)
		mockLogs := []*evm.Log{{
			TxHash: expectedHash,
		}}
		mockEVMService.EXPECT().FilterLogs(ctx, mock.Anything).Return(&evmtypes.FilterLogsReply{Logs: mockLogs}, nil)

		evmLogs, err := forwarderClient.GetReportProcessedEvents(ctx, receiverAddress, workflowExecutionID, reportID)
		require.NoError(t, err)
		require.Equal(t, 1, len(evmLogs))
		require.Equal(t, expectedHash, evmLogs[0].TxHash)
	})
	t.Run("Get Transmission info - Error calling FilterLogs", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		mockEVMService.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(&evmtypes.HeaderByNumberReply{Header: &evmtypes.Header{Number: big.NewInt(100)}}, nil).Maybe()
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
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	testLogger := logger.Test(t)
	forwarderAddress := common.BytesToAddress(test.RandomBytes(20))
	receiverAddress := common.BytesToAddress(test.RandomBytes(20))
	forwarderABI, _ := forwarder.KeystoneForwarderMetaData.GetAbi()
	testEncoder := TestEncoder{abi: *forwarderABI}

	t.Run("Invoke On Report - Successfully send transaction - empty report", func(t *testing.T) {
		report := &workflowpb.ReportResponse{}
		expectedEncodedReport := testEncoder.encodeReport(receiverAddress, report)

		testSuccessfulReportSubmissionAndEncoding(ctx, t, forwarderAddress, testLogger, expectedEncodedReport, receiverAddress, report)
	})
	t.Run("Invoke On Report - Successfully send transaction - complete report", func(t *testing.T) {
		report := &workflowpb.ReportResponse{
			RawReport:     test.RandomBytes(100),
			ReportContext: test.RandomBytes(50),
			Sigs: []*workflowpb.AttributedSignature{
				{Signature: test.RandomBytes(20)},
				{Signature: test.RandomBytes(20)}},
		}

		expectedEncodedReport := testEncoder.encodeReport(receiverAddress, report)

		testSuccessfulReportSubmissionAndEncoding(ctx, t, forwarderAddress, testLogger, expectedEncodedReport, receiverAddress, report)
	})
	t.Run("Invoke On Report - Retry on GasLimit not supported", func(t *testing.T) {
		report := &workflowpb.ReportResponse{}
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
		report := &workflowpb.ReportResponse{}
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
		require.Contains(t, err.Error(), "failed to submit transaction: "+expectedError)
	})
}

func testSuccessfulReportSubmissionAndEncoding(ctx context.Context, t *testing.T, forwarderAddress common.Address, testLogger logger.Logger, expectedEncodedReport []byte, receiverAddress common.Address, report *workflowpb.ReportResponse) {
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

func (t TestEncoder) encodeReport(receiver common.Address, report *workflowpb.ReportResponse) []byte {
	var sigs [][]byte
	for _, sig := range report.Sigs {
		sigs = append(sigs, sig.Signature)
	}

	data, _ := t.abi.Pack("report", receiver, report.RawReport, report.ReportContext, sigs)
	return data
}

func TestCreForwarderCodecImpl_EncodeReport(t *testing.T) {
	t.Parallel()
	codec, err := contracts.NewCREForwarderCodec()
	require.NoError(t, err)
	receiver := common.BytesToAddress(test.RandomBytes(20))

	t.Run("Successfully encode report with signatures", func(t *testing.T) {
		report := &workflowpb.ReportResponse{
			RawReport:     test.RandomBytes(100),
			ReportContext: test.RandomBytes(50),
			Sigs: []*workflowpb.AttributedSignature{
				{Signature: test.RandomBytes(32)},
				{Signature: test.RandomBytes(32)},
			},
		}

		data, err := codec.EncodeReport(receiver, report)
		require.NoError(t, err)
		require.NotEmpty(t, data)
		require.GreaterOrEqual(t, len(data), 4)
	})

	t.Run("Successfully encode report with empty signatures", func(t *testing.T) {
		report := &workflowpb.ReportResponse{
			RawReport:     test.RandomBytes(100),
			ReportContext: test.RandomBytes(50),
			Sigs:          []*workflowpb.AttributedSignature{},
		}

		data, err := codec.EncodeReport(receiver, report)
		require.NoError(t, err)
		require.NotEmpty(t, data)
	})

	t.Run("Successfully encode report with nil fields", func(t *testing.T) {
		report := &workflowpb.ReportResponse{}

		data, err := codec.EncodeReport(receiver, report)
		require.NoError(t, err)
		require.NotEmpty(t, data)
		require.GreaterOrEqual(t, len(data), 4)
	})
}

func TestCreForwarderCodecImpl_DecodeQueryTransmissionInfo(t *testing.T) {
	t.Parallel()
	codec, err := contracts.NewCREForwarderCodec()
	require.NoError(t, err)
	abi, err := forwarder.KeystoneForwarderMetaData.GetAbi()
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		expected := contracts.TransmissionInfo{
			State:           2,
			Success:         true,
			InvalidReceiver: false,
			TransmissionId:  [32]byte(test.RandomBytes(32)),
			Transmitter:     common.BytesToAddress(test.RandomBytes(20)),
			GasLimit:        big.NewInt(1000),
		}

		data, err := abi.Methods["getTransmissionInfo"].Outputs.Pack(expected)
		require.NoError(t, err)

		result, err := codec.DecodeQueryTransmissionInfo(data)
		require.NoError(t, err)
		require.Equal(t, expected.State, result.State)
		require.Equal(t, expected.Success, result.Success)
		require.Equal(t, expected.InvalidReceiver, result.InvalidReceiver)
	})

	t.Run("unpack error", func(t *testing.T) {
		_, err := codec.DecodeQueryTransmissionInfo([]byte("invalid"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to abi unpack")
	})

	t.Run("unmarshal error", func(t *testing.T) {
		badData := map[string]any{"gasLimit": "not_a_number", "state": "not_a_number"}
		data, err := abi.Methods["getTransmissionInfo"].Outputs.Pack(badData)
		if err == nil {
			_, err = codec.DecodeQueryTransmissionInfo(data)
			if err != nil {
				require.Contains(t, err.Error(), "failed to unmarshal")
			}
		}
	})
}
