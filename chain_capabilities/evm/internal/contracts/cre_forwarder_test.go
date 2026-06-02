package contracts_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/sha3"

	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	mocks2 "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
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
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)

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
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)

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
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)

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
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)
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
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)
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
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)
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
		forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)
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

func TestNewCREForwarderClient_LookbackConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	testLogger := logger.Test(t)
	forwarderAddress := common.BytesToAddress(test.RandomBytes(20))

	t.Run("forwarderLookbackBlocks defaults when set to 0", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		latest := big.NewInt(150)
		mockEVMService.EXPECT().
			HeaderByNumber(mock.Anything, mock.Anything).
			Return(&evmtypes.HeaderByNumberReply{Header: &evmtypes.Header{Number: latest}}, nil).
			Once()

		forwarderClient, err := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, 0, testLogger)
		require.NoError(t, err)

		mockEVMService.EXPECT().
			FilterLogs(ctx, mock.MatchedBy(func(req evmtypes.FilterLogsRequest) bool {
				// FromBlock 50 == 150 - 100 (default value) = 90
				return big.NewInt(50).Cmp(req.FilterQuery.FromBlock) == 0
			})).
			Return(&evmtypes.FilterLogsReply{Logs: []*evm.Log{}}, nil)

		_, err = forwarderClient.GetReportProcessedEvents(ctx, common.BytesToAddress(test.RandomBytes(20)), [32]byte(test.RandomBytes(32)), [2]byte(test.RandomBytes(2)))
		require.NoError(t, err)
	})

	t.Run("forwarderLookbackBlocks remains when set to 10", func(t *testing.T) {
		mockEVMService := mocks2.NewEVMService(t)
		latest := big.NewInt(100)
		mockEVMService.EXPECT().
			HeaderByNumber(mock.Anything, mock.Anything).
			Return(&evmtypes.HeaderByNumberReply{Header: &evmtypes.Header{Number: latest}}, nil).
			Once()

		forwarderClient, err := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, 10, testLogger)
		require.NoError(t, err)

		mockEVMService.EXPECT().
			FilterLogs(ctx, mock.MatchedBy(func(req evmtypes.FilterLogsRequest) bool {
				// FromBlock 90 == 100 - 10 = 90
				return big.NewInt(90).Cmp(req.FilterQuery.FromBlock) == 0
			})).
			Return(&evmtypes.FilterLogsReply{Logs: []*evm.Log{}}, nil)

		_, err = forwarderClient.GetReportProcessedEvents(ctx, common.BytesToAddress(test.RandomBytes(20)), [32]byte(test.RandomBytes(32)), [2]byte(test.RandomBytes(2)))
		require.NoError(t, err)
	})
}

func testSuccessfulReportSubmissionAndEncoding(ctx context.Context, t *testing.T, forwarderAddress common.Address, testLogger logger.Logger, expectedEncodedReport []byte, receiverAddress common.Address, report *workflowpb.ReportResponse) {
	mockEVMService := mocks2.NewEVMService(t)
	forwarderClient, _ := contracts.NewCREForwarderClient(mockEVMService, forwarderAddress, contracts.DefaultLookbackBlocks, testLogger)
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

// legacyDebugID reproduces the TransmissionID.GetDebugID() implementation as it
// existed on main, when its output was used to seed the transmission schedule
// permutation. The seed (and therefore every node's queue position) must not
// change, so TransmissionID.String() must produce a byte-identical string.
func legacyDebugID(t contracts.TransmissionID) string {
	return fmt.Sprintf("receiver: %s, reportID: %s, workflowExecutionID %s",
		common.Bytes2Hex(t.Receiver[:]),
		common.Bytes2Hex(t.ReportID[:]),
		common.Bytes2Hex(t.WorkflowExecutionID[:]))
}

// transmissionScheduleSeed mirrors the seed derivation in
// common/transmission_schedule.transmissionScheduleSeed, which is what actually
// consumes the string and feeds it to the permutation.
func transmissionScheduleSeed(transmissionID string) [16]byte {
	hash := sha3.New256()
	hash.Write([]byte(transmissionID))
	var key [16]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func TestTransmissionID_ScheduleSeed_MatchesLegacySeed(t *testing.T) {
	cases := map[string]contracts.TransmissionID{
		"zero value": {},
		"all set": {
			Receiver:            common.BytesToAddress([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14}),
			ReportID:            [2]byte{0xab, 0xcd},
			WorkflowExecutionID: [32]byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00},
		},
	}

	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			// The exact string must match, since it is hashed verbatim into the seed.
			require.Equal(t, legacyDebugID(id), id.String(),
				"String format drifted from the legacy GetDebugID format used to seed the schedule")
			// And therefore the derived permutation seed must be identical too.
			require.Equal(t, transmissionScheduleSeed(legacyDebugID(id)), transmissionScheduleSeed(id.String()))
		})
	}
}
