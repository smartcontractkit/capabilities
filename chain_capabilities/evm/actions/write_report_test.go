package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	evmtest "github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"

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

// successLogData returns 32 bytes of ABI-encoded boolean true for successful report result
func successLogData() []byte {
	data := make([]byte, 32)
	data[31] = 0x01
	return data
}

// failedLogData returns 32 bytes of ABI-encoded boolean false for failed report result
func failedLogData() []byte {
	return make([]byte, 32)
}

func nonNilPositiveGasCfgMatcher() interface{} {
	return mock.MatchedBy(func(gc *evm.GasConfig) bool {
		return gc != nil && gc.GasLimit > 0
	})
}

func TestWithQuickRetry(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)

	t.Run("retries until success", func(t *testing.T) {
		ctx := t.Context()
		attempts := 0

		result, err := withQuickRetry(ctx, lggr, func(ctx context.Context) (string, error) {
			attempts++
			if attempts < 3 {
				return "", errors.New("transient error")
			}
			return "success after retries", nil
		})

		require.NoError(t, err)
		require.Equal(t, "success after retries", result)
		require.Equal(t, 3, attempts)
	})

	t.Run("respects parent context timeout", func(t *testing.T) {
		// Parent context with 200ms timeout - shorter than withQuickRetry's 10s
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := withQuickRetry(ctx, lggr, func(ctx context.Context) (string, error) {
			return "", errors.New("always fails")
		})
		elapsed := time.Since(start)

		require.Error(t, err)
		// Should complete within parent timeout + some margin
		require.Less(t, elapsed, 500*time.Millisecond, "should respect parent context timeout")
	})

	t.Run("returns original error not context deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		expectedErr := "specific RPC error"
		_, err := withQuickRetry(ctx, lggr, func(ctx context.Context) (string, error) {
			return "", errors.New(expectedErr)
		})

		require.Error(t, err)
		require.Equal(t, expectedErr, err.Error())
		require.NotContains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("makes multiple retry attempts with backoff", func(t *testing.T) {
		// Use a 1s parent timeout to keep test fast
		ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
		defer cancel()

		attempts := 0
		_, err := withQuickRetry(ctx, lggr, func(ctx context.Context) (string, error) {
			attempts++
			return "", errors.New("always fails")
		})

		require.Error(t, err)
		// With 1s timeout and 100ms initial backoff, should get several attempts
		// (100ms + 200ms + 400ms = 700ms for 3 retries, then timeout)
		require.GreaterOrEqual(t, attempts, 3, "should make multiple retry attempts")
		require.LessOrEqual(t, attempts, 6, "should be bounded by timeout")
	})

	t.Run("with cancelled context returns original error from fn", func(t *testing.T) {
		// Create an already-cancelled context
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately

		expectedErr := "fn error"
		_, err := withQuickRetry(ctx, lggr, func(ctx context.Context) (string, error) {
			return "", errors.New(expectedErr)
		})

		require.Error(t, err)
		// The retry strategy calls fn at least once before checking context.
		// Since fn returns an error, lastErr is set, and we return the original error.
		require.Equal(t, expectedErr, err.Error())
	})
}

func TestWithPollingRetry(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)

	t.Run("retries until success", func(t *testing.T) {
		ctx := t.Context()
		attempts := 0

		result, err := withPollingRetry(ctx, lggr, func(ctx context.Context) (int, error) {
			attempts++
			if attempts < 4 {
				return 0, errors.New("not ready yet")
			}
			return 100, nil
		})

		require.NoError(t, err)
		require.Equal(t, 100, result)
		require.Equal(t, 4, attempts)
	})

	t.Run("respects parent context timeout", func(t *testing.T) {
		// Parent context with 300ms timeout - shorter than withPollingRetry's 60s
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := withPollingRetry(ctx, lggr, func(ctx context.Context) (int, error) {
			return 0, errors.New("always fails")
		})
		elapsed := time.Since(start)

		require.Error(t, err)
		// Should complete within parent timeout + some margin
		require.Less(t, elapsed, 600*time.Millisecond, "should respect parent context timeout")
	})

	t.Run("returns original error not context deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()

		expectedErr := "chain state not updated"
		_, err := withPollingRetry(ctx, lggr, func(ctx context.Context) (int, error) {
			return 0, errors.New(expectedErr)
		})

		require.Error(t, err)
		require.Equal(t, expectedErr, err.Error())
		require.NotContains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("uses longer backoff than quick retry", func(t *testing.T) {
		ctx := t.Context()
		attempts := 0
		var timestamps []time.Time

		start := time.Now()
		_, _ = withPollingRetry(ctx, lggr, func(ctx context.Context) (int, error) {
			attempts++
			timestamps = append(timestamps, time.Now())
			if attempts >= 4 {
				return 1, nil // succeed to stop
			}
			return 0, errors.New("not ready")
		})

		// Verify backoff is happening (gaps should increase)
		if len(timestamps) >= 3 {
			gap1 := timestamps[1].Sub(timestamps[0])
			gap2 := timestamps[2].Sub(timestamps[1])
			// Second gap should be roughly 2x the first (exponential backoff)
			// Allow some tolerance for timing variations
			require.Greater(t, gap2, gap1/2, "backoff should be exponential")
		}

		totalTime := time.Since(start)
		// Should complete reasonably fast with just 4 attempts
		require.Less(t, totalTime, 2*time.Second)
	})

	t.Run("with cancelled context returns original error from fn", func(t *testing.T) {
		// Create an already-cancelled context
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately

		expectedErr := "fn error"
		_, err := withPollingRetry(ctx, lggr, func(ctx context.Context) (int, error) {
			return 0, errors.New(expectedErr)
		})

		require.Error(t, err)
		// The retry strategy calls fn at least once before checking context.
		// Since fn returns an error, lastErr is set, and we return the original error.
		require.Equal(t, expectedErr, err.Error())
	})
}

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

	t.Run("GasConfig.GasLimit below minimum is rejected", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		belowMinimum := ConfiguredReceiverGasMinimum + contracts.ForwarderContractLogicGasCost - 1

		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
			GasConfig: &evm.GasConfig{GasLimit: uint64(belowMinimum)}, // nolint:gosec // G115: integer overflow conversion
		})
		require.Error(t, err)
	})

	t.Run("nil and 0 GasConfig skips gas limit check", func(t *testing.T) {
		_, _, service := createMocksAndCapability(t, lggr)
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		require.NoError(t, service.validateInputsAndReportMetadata(createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver:  testutils.NewAddress().Bytes(),
			Report:    &workflowpb.ReportResponse{RawReport: encodedReportMetadata, Sigs: generateRandomSignatures()},
			GasConfig: nil,
		}))

		require.NoError(t, service.validateInputsAndReportMetadata(createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver:  testutils.NewAddress().Bytes(),
			Report:    &workflowpb.ReportResponse{RawReport: encodedReportMetadata, Sigs: generateRandomSignatures()},
			GasConfig: &evmcappb.GasConfig{GasLimit: 0},
		}))
	})
}

func TestWriteReport_ExecuteWriteReport(t *testing.T) {
	t.Parallel()

	t.Run("Fail when workflow execution id is not valid", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, _, service := createMocksAndCapability(t, testLogger)

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		req := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		}

		md := createTestRequestMetadata(reportMetadata)
		md.WorkflowExecutionID = "not-hex"

		_, err := service.WriteReport(ctx, md, req)
		require.Error(t, err)
	})

	t.Run("Fail while getting transmission info", func(t *testing.T) {
		// Short timeout so quick retry fails fast in tests
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		errorMsg := "some error"
		expectedError := "[2]Unknown: failed to get transmission info: some error"

		// Will be retried until context timeout
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{}, errors.New(errorMsg))

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
		// Verify we get the original error, not a context timeout
		require.NotContains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("TX already transmitted successfully", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
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

	t.Run("TX already transmitted successfully - Failed to fetch report emitted log", func(t *testing.T) {
		// Short timeout so polling retry fails fast in tests
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		// Will be retried until context timeout
		expectedError := "Error getting report emitted log"
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New(expectedError))

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
		// Verify we get the original error, not a context timeout
		require.NotContains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("TX already transmitted successfully - Failed to fetch transaction receipt", func(t *testing.T) {
		// Short timeout so quick retry fails fast in tests
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		// Will be retried until context timeout
		expectedError := "Error getting tx receipt"
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(nil, errors.New(expectedError))

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
		// Verify we get the original error, not a context timeout
		require.NotContains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("TX already transmitted successfully - Failed to calculate transaction fee", func(t *testing.T) {
		// Short timeout so quick retry fails fast in tests
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)

		// Will be retried until context timeout
		expectedError := "Error calculating transaction fee"
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(nil, errors.New(expectedError))

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
		// Verify we get the original error, not a context timeout
		require.NotContains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("TX already transmitted - Invalid receiver (does NOT retry, returns reverted + invalid receiver message)", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		receiver := testutils.NewAddress().Bytes()

		// Transmission already attempted by someone else, marked invalid receiver => do not retry
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: true,
				State:           contracts.TransmissionStateInvalidReceiver,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		// Tx hash must be discovered from processed events
		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: failedLogData(), BlockNumber: big.NewInt(100)}}, nil)

		// Receipt + fee calculation for that tx hash
		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
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

		// No metering because we did not submit a new tx locally.
		require.Len(t, txResult.ResponseMetadata.Metering, 0)

		// Also prove we didn't attempt a new tx.
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("TX already transmitted and failed with enough gas - tx hash retrieval fails => returns error (no retry)", func(t *testing.T) {
		// Short timeout so polling retry fails fast in tests
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		// Make request gas big enough so code uses:
		//   txGasLimit = requestGasLimit - overhead
		desiredReceiverGas := uint64(EnoughReceiverGas * 100)
		writeReportGasLimit := contracts.ForwarderContractLogicGasCost + desiredReceiverGas

		// We want to hit:
		//   if transmissionInfo.GasLimit.Uint64() > txGasLimit { ... GetFailedTransmissionHash ... if err return err }
		// txGasLimit computed == desiredReceiverGas
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateFailed,
				GasLimit:        new(big.Int).SetUint64(desiredReceiverGas + 1), // strictly greater => no retry path
			}, nil)

		// Force TxHashRetriever.GetFailedTransmissionHash() to fail - will be retried until context timeout
		expectedErr := "error getting report emitted log"
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New(expectedErr))

		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
			GasConfig: &evm.GasConfig{GasLimit: writeReportGasLimit},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedErr)
		// Verify we get the original error, not a context timeout
		require.NotContains(t, err.Error(), "context deadline exceeded")

		// Ensure no retry / no new tx attempt happened.
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("TX already transmitted and failed with enough gas - does NOT retry (no InvokeOnReport)", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		// Make request.GasConfig.GasLimit *definitely* bigger than (ReceiverGasMinimum + overhead),
		// so executeWriteReport uses:
		//   txGasLimit = requestGasLimit - overhead
		desiredReceiverGas := uint64(EnoughReceiverGas * 100)
		writeReportGasLimit := contracts.ForwarderContractLogicGasCost + desiredReceiverGas

		// Previously attempted and failed, but transmission recorded gasLimit is strictly greater than
		// computed txGasLimit => NO retry.
		// (computed txGasLimit == desiredReceiverGas)
		transmissionInfo := contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateFailed,
			GasLimit:        new(big.Int).SetUint64(desiredReceiverGas + 1),
		}
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(transmissionInfo, nil).
			Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: failedLogData(), BlockNumber: big.NewInt(100)}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
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

		txResult, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
			GasConfig: &evm.GasConfig{GasLimit: writeReportGasLimit},
		})
		require.NoError(t, err)

		// Prove we didn't attempt a new tx.
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
			ErrorMessage:                    capcommon.Ptr("receiver contract execution failure"),
		}, txResult.Response)
		require.Len(t, txResult.ResponseMetadata.Metering, 0)
	})

	t.Run("TX previously failed with insufficient gas - retries by invoking again", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

		receiverAddress := testutils.NewAddress()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}

		// Also make request gas large enough that code uses:
		//   txGasLimit = requestGasLimit - overhead
		desiredReceiverGasMin := uint64(EnoughReceiverGas * 100)
		writeReportGasLimit := contracts.ForwarderContractLogicGasCost + desiredReceiverGasMin

		writeReportRequest := &evm.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
			GasConfig: &evm.GasConfig{
				GasLimit: writeReportGasLimit,
			},
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		// Previously attempted and failed with too-low transmission gas => should retry.
		// (computed txGasLimit == desiredReceiverGasMin, and NotEnoughReceiverGas should be <= that)
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateFailed,
				GasLimit:        big.NewInt(NotEnoughReceiverGas),
			}, nil).
			Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHash,
				TxStatus:         evmtypes.TxSuccess,
				TxIdempotencyKey: "retry-idempotency-key",
			}, nil).
			Once()

		// After retry, succeeded.
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         true,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)

		retryTxFee := int64(2000)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(retryTxFee)}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)

		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
		}, txResult.Response)

		// Retried tx => should be metered.
		evmtest.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("TX first transmission - Successful TX execution (ensures non-nil + positive gas config passed to forwarder)", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

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
			// IMPORTANT: no GasConfig provided here; service must synthesize one with GasLimit > 0.
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateNotAttempted,
			}, nil).
			Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHash,
				TxStatus:         evmtypes.TxSuccess,
				TxIdempotencyKey: "test-idempotency-key",
			}, nil).
			Once()

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         true,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).
			Return(&receipt, nil)

		retryTxFee := int64(2000)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(retryTxFee)}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)

		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(retryTxFee)),
		}, txResult.Response)

		evmtest.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("TX first transmission - Error submitting TX (ensures gas config passed is non-nil + positive)", func(t *testing.T) {
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

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateNotAttempted,
			}, nil).
			Once()

		expectedError := "Error sending tx to write report"
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(nil, errors.New(expectedError)).
			Once()

		_, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})

	t.Run("TX first transmission with zero-value scheduler (no DeltaStage) succeeds", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		// Override with zero-value scheduler to simulate DeltaStage not configured
		service.transmissionScheduler = ts.TransmissionScheduler{}

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

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

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateNotAttempted,
			}, nil).
			Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHash,
				TxStatus:         evmtypes.TxSuccess,
				TxIdempotencyKey: "test-idempotency-key",
			}, nil).
			Once()

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         true,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
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

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)

		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(2000)),
		}, txResult.Response)

		evmtest.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("TX first transmission - Duplicate tx: txmgr reports reverted but transmission succeeded => use onchain tx hash", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

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

		// Not attempted yet.
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateNotAttempted,
			}, nil).
			Once()

		// Txmgr returns "reverted" (duplicate tx scenario).
		txHashFromTxmgr := evmtypes.Hash(test.RandomBytes(32))
		txHashFromLogs := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHashFromTxmgr,
				TxStatus:         evmtypes.TxReverted, // triggers duplicate-tx branch
				TxIdempotencyKey: "dup-idempotency-key",
			}, nil).
			Once()

		// Final transmission status is succeeded.
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         true,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		// TxHashRetriever should be used in duplicate tx branch.
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHashFromLogs, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		// Receipt should be fetched for the *log* hash, not the txmgr hash.
		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHashFromLogs,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHashFromLogs, IsExternal: false}).
			Return(&receipt, nil)

		txFee := int64(2000)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(txFee)}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)

		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:], // MUST be the log hash
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(txFee)),
		}, txResult.Response)

		evmtest.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("TX first transmission - Fatal tx but transmission succeeded => use onchain tx hash", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

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

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateNotAttempted,
			}, nil).
			Once()

		txHashFromTxmgr := evmtypes.Hash(test.RandomBytes(32))
		txHashFromLogs := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHashFromTxmgr,
				TxStatus:         evmtypes.TxFatal,
				TxIdempotencyKey: "fatal-idempotency-key",
			}, nil).
			Once()

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         true,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHashFromLogs, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHashFromLogs,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().
			GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHashFromLogs, IsExternal: false}).
			Return(&receipt, nil)

		txFee := int64(2000)
		evmServiceMock.EXPECT().
			CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(txFee)}, nil)

		txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)

		equalWriteReportReply(t, &evm.WriteReportReply{
			TxStatus:                        evmcappb.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          receipt.TxHash[:],
			ReceiverContractExecutionStatus: evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS.Enum(),
			TransactionFee:                  pb.NewBigIntFromInt(big.NewInt(txFee)),
		}, txResult.Response)

		evmtest.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("TX first transmission - Fatal tx and GetSuccessfulTransmissionHash fails => returns error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
		defer cancel()
		testLogger := logger.Test(t)
		evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		evmServiceMock.EXPECT().
			GetTransactionFee(mock.Anything, mock.Anything).
			Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
			Maybe()

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

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateNotAttempted,
			}, nil).
			Once()

		txHashFromTxmgr := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHashFromTxmgr,
				TxStatus:         evmtypes.TxFatal,
				TxIdempotencyKey: "fatal-idempotency-key",
			}, nil).
			Once()

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         true,
				InvalidReceiver: false,
				State:           contracts.TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed to get report processed events"))

		_, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get report processed events")
	})

	t.Run("TX locally attempted but failed; queue position behavior", func(t *testing.T) {
		for _, queuePosition := range []int{0, 1, 2, 3} {
			t.Run("queue position "+strconv.Itoa(queuePosition), func(t *testing.T) {
				ctx := t.Context()
				testLogger := logger.Test(t)
				evmServiceMock, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

				evmServiceMock.EXPECT().
					GetTransactionFee(mock.Anything, mock.Anything).
					Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(300)}, nil).
					Maybe()

				var testPeerID p2ptypes.PeerID
				testPeerID[0] = 0x01
				var otherPeerID1 p2ptypes.PeerID
				otherPeerID1[0] = 0x02
				var otherPeerID2 p2ptypes.PeerID
				otherPeerID2[0] = 0x03
				var otherPeerID3 p2ptypes.PeerID
				otherPeerID3[0] = 0x04
				scheduler := ts.NewTransmissionScheduler(
					testPeerID,
					[]p2ptypes.PeerID{testPeerID, otherPeerID1, otherPeerID2, otherPeerID3},
					10*time.Millisecond,
					2,
					testLogger,
				)
				service.transmissionScheduler = scheduler

				receiverAddress := testutils.NewAddress()
				signedReport, capabilitiesMetadata, transmissionID := createReportAndMetadataForQueuePosition(
					t,
					&service.transmissionScheduler,
					receiverAddress.Bytes(),
					queuePosition,
				)

				// Make request gas large enough so we compute txGasLimit = requestGasLimit - overhead
				desiredReceiverGas := uint64(EnoughReceiverGas * 100)
				writeReportGasLimit := contracts.ForwarderContractLogicGasCost + desiredReceiverGas

				writeReportRequest := &evm.WriteReportRequest{
					Receiver: receiverAddress.Bytes(),
					Report:   signedReport,
					GasConfig: &evm.GasConfig{
						GasLimit: writeReportGasLimit,
					},
				}

				// 1) Initial GetTransmissionInfo: previously attempted and failed with too-low gas => we will retry (InvokeOnReport).
				mockForwarderClient.
					On("GetTransmissionInfo", mock.Anything, transmissionID).
					Return(contracts.TransmissionInfo{
						Success:         false,
						InvalidReceiver: false,
						State:           contracts.TransmissionStateFailed,
						GasLimit:        big.NewInt(NotEnoughReceiverGas),
					}, nil).
					Once()

				// 2) InvokeOnReport: local retry returns a "latest" tx hash.
				latestTxHash := evmtypes.Hash(test.RandomBytes(32))
				mockForwarderClient.
					On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
					Return(&evmtypes.TransactionResult{
						TxHash:           latestTxHash,
						TxStatus:         evmtypes.TxSuccess,
						TxIdempotencyKey: "retry-idempotency-key",
					}, nil).
					Once()

				// 3) After submitting, final transmission state is Failed.
				mockForwarderClient.
					On("GetTransmissionInfo", mock.Anything, transmissionID).
					Return(contracts.TransmissionInfo{
						Success:         false,
						InvalidReceiver: false,
						State:           contracts.TransmissionStateFailed,
						GasLimit:        big.NewInt(EnoughReceiverGas),
					}, nil).
					Once()

				var receiptTxHash evmtypes.Hash
				if queuePosition == 0 {
					receiptTxHash = latestTxHash
				} else {
					originalFailedTxHash := evmtypes.Hash(test.RandomBytes(32))
					receiptTxHash = originalFailedTxHash
					mockForwarderClient.EXPECT().
						GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
						Return([]*evmtypes.Log{
							{TxHash: latestTxHash, Data: failedLogData(), BlockNumber: big.NewInt(200)},
							{TxHash: originalFailedTxHash, Data: failedLogData(), BlockNumber: big.NewInt(100)},
						}, nil)
				}

				receipt := evmtypes.Receipt{
					Status:            1,
					TxHash:            receiptTxHash,
					GasUsed:           1000,
					EffectiveGasPrice: big.NewInt(2),
				}
				evmServiceMock.EXPECT().
					GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: receiptTxHash, IsExternal: false}).
					Return(&receipt, nil)

				txFee := int64(2000)
				evmServiceMock.EXPECT().
					CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).
					Return(&evmtypes.TransactionFee{TransactionFee: big.NewInt(txFee)}, nil)

				txResult, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
				require.NoError(t, err)
				require.NotNil(t, txResult)
				require.NotNil(t, txResult.Response)

				require.Equal(t, receiptTxHash[:], txResult.Response.TxHash)
				require.NotNil(t, txResult.Response.ReceiverContractExecutionStatus)
				require.Equal(t, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED.Enum(), txResult.Response.ReceiverContractExecutionStatus.Enum())
				evmtest.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")

				if queuePosition == 0 {
					mockForwarderClient.AssertNotCalled(t, "GetReportProcessedEvents", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
				}
			})
		}
	})

	t.Run("Invalid transmission state (default switch) => returns error", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		const invalidState = contracts.TransmissionStateFailed + 10

		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           invalidState,
			}, nil).
			Once()

		_, err := service.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &evm.WriteReportRequest{
			Receiver: testutils.NewAddress().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), getInvalidStateErrorMessage(invalidState))

		// Prove we didn't attempt to submit anything.
		mockForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestPollTransmissionInfo_QueuePositionScenarios(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("position 0 succeeded, others return succeeded", func(t *testing.T) {
		for _, queuePosition := range []int{0, 1, 2, 3} {
			t.Run("queue position "+strconv.Itoa(queuePosition), func(t *testing.T) {
				wr, testLogger, mockForwarderClient, request, transmissionID :=
					setupPollTransmissionInfoForQueuePosition(t, queuePosition)

				expectedInfo := contracts.TransmissionInfo{
					Success:         true,
					InvalidReceiver: false,
					State:           contracts.TransmissionStateSucceeded,
				}
				mockForwarderClient.
					On("GetTransmissionInfo", mock.Anything, transmissionID).
					Return(expectedInfo, nil).
					Once()

				txHashRetriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
				info, err := wr.pollTransmissionInfo(ctx, request, monitoring.TelemetryContext{}, transmissionID, queuePosition, txHashRetriever)
				require.NoError(t, err)
				require.Equal(t, expectedInfo.State, info.State)
			})
		}
	})

	t.Run("position 0 failed, next one succeeded, rest handle success", func(t *testing.T) {
		for _, queuePosition := range []int{0, 1, 2, 3} {
			t.Run("queue position "+strconv.Itoa(queuePosition), func(t *testing.T) {
				wr, testLogger, mockForwarderClient, request, transmissionID :=
					setupPollTransmissionInfoForQueuePosition(t, queuePosition)

				expectedState := contracts.TransmissionStateSucceeded
				if queuePosition == 0 {
					expectedState = contracts.TransmissionStateFailed
				}

				mockForwarderClient.
					On("GetTransmissionInfo", mock.Anything, transmissionID).
					Return(contracts.TransmissionInfo{
						Success:         expectedState == contracts.TransmissionStateSucceeded,
						InvalidReceiver: false,
						State:           expectedState,
					}, nil).
					Once()

				txHashRetriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
				info, err := wr.pollTransmissionInfo(ctx, request, monitoring.TelemetryContext{}, transmissionID, queuePosition, txHashRetriever)
				require.NoError(t, err)
				require.Equal(t, expectedState, info.State)
			})
		}
	})

	t.Run("all positions fail", func(t *testing.T) {
		for _, queuePosition := range []int{0, 1, 2, 3} {
			t.Run("queue position "+strconv.Itoa(queuePosition), func(t *testing.T) {
				wr, testLogger, mockForwarderClient, request, transmissionID :=
					setupPollTransmissionInfoForQueuePosition(t, queuePosition)

				mockForwarderClient.
					On("GetTransmissionInfo", mock.Anything, transmissionID).
					Return(contracts.TransmissionInfo{
						Success:         false,
						InvalidReceiver: false,
						State:           contracts.TransmissionStateFailed,
					}, nil).
					Once()

				if queuePosition > 0 {
					logs := []*evmtypes.Log{
						{TxHash: evmtypes.Hash(test.RandomBytes(32)), Data: failedLogData(), BlockNumber: big.NewInt(100)},
						{TxHash: evmtypes.Hash(test.RandomBytes(32)), Data: failedLogData(), BlockNumber: big.NewInt(101)},
						{TxHash: evmtypes.Hash(test.RandomBytes(32)), Data: failedLogData(), BlockNumber: big.NewInt(102)},
					}
					mockForwarderClient.EXPECT().
						GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
						Return(logs, nil)
				}

				txHashRetriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
				info, err := wr.pollTransmissionInfo(ctx, request, monitoring.TelemetryContext{}, transmissionID, queuePosition, txHashRetriever)
				require.NoError(t, err)
				require.Equal(t, contracts.TransmissionStateFailed, info.State)
			})
		}
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

	// Create a test TransmissionScheduler with a minimal DON setup
	// For tests, we use a single-node DON so position is always 0 (no waiting)
	var testPeerID p2ptypes.PeerID
	testPeerID[0] = 0x01
	testDONMembers := []p2ptypes.PeerID{testPeerID}
	transmissionScheduler := ts.NewTransmissionScheduler(
		testPeerID,
		testDONMembers,
		10*time.Millisecond, // Small deltaStage for tests
		2,
		lggr,
	)

	service := &EVM{
		keystoneForwarderAddress: common.BytesToAddress(test.RandomBytes(20)),
		forwarderClient:          mockForwarderClient,
		lggr:                     logger.Sugared(lggr),
		EVMService:               mockEVMService,
		ReceiverGasMinimum:       ConfiguredReceiverGasMinimum,
		chainSelector:            1,
		beholderProcessor:        test.NopBeholderProcessor{},
		messageBuilder:           monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		transmissionScheduler:    transmissionScheduler,
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

func createReportAndMetadataForQueuePosition(t *testing.T, scheduler *ts.TransmissionScheduler, receiver []byte, desiredPosition int) (*workflowpb.ReportResponse, capabilities.RequestMetadata, contracts.TransmissionID) {
	t.Helper()

	for i := 0; i < 1000; i++ {
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, err := reportMetadata.Encode()
		require.NoError(t, err)

		report := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		request := &evm.WriteReportRequest{
			Receiver: receiver,
			Report:   report,
		}
		requestMetadata := createTestRequestMetadata(reportMetadata)
		transmissionID, err := getTransmissionID(requestMetadata.WorkflowExecutionID, request)
		require.NoError(t, err)

		if scheduler.GetQueuePosition(transmissionID.GetDebugID()) == desiredPosition {
			return report, requestMetadata, transmissionID
		}
	}

	require.FailNow(t, "failed to generate metadata for desired queue position", "desiredPosition", desiredPosition)
	return nil, capabilities.RequestMetadata{}, contracts.TransmissionID{}
}

func setupPollTransmissionInfoForQueuePosition(
	t *testing.T,
	queuePosition int,
) (*WriteReport, logger.Logger, *mocks.CREForwarderClient, *evm.WriteReportRequest, contracts.TransmissionID) {
	t.Helper()

	testLogger := logger.Test(t)
	mockForwarderClient := mocks.NewCREForwarderClient(t)

	var testPeerID p2ptypes.PeerID
	testPeerID[0] = 0x01
	var otherPeerID1 p2ptypes.PeerID
	otherPeerID1[0] = 0x02
	var otherPeerID2 p2ptypes.PeerID
	otherPeerID2[0] = 0x03
	var otherPeerID3 p2ptypes.PeerID
	otherPeerID3[0] = 0x04

	scheduler := ts.NewTransmissionScheduler(
		testPeerID,
		[]p2ptypes.PeerID{testPeerID, otherPeerID1, otherPeerID2, otherPeerID3},
		10*time.Millisecond,
		2,
		testLogger,
	)

	wr := &WriteReport{
		forwarderClient:       mockForwarderClient,
		lggr:                  logger.Sugared(testLogger),
		beholderProcessor:     test.NopBeholderProcessor{},
		messageBuilder:        monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		transmissionScheduler: scheduler,
	}

	receiverAddress := testutils.NewAddress()
	signedReport, _, transmissionID := createReportAndMetadataForQueuePosition(
		t,
		&scheduler,
		receiverAddress.Bytes(),
		queuePosition,
	)

	request := &evm.WriteReportRequest{
		Receiver: receiverAddress.Bytes(),
		Report:   signedReport,
	}

	require.Equal(t, queuePosition, scheduler.GetQueuePosition(transmissionID.GetDebugID()))
	return wr, testLogger, mockForwarderClient, request, transmissionID
}

func TestDecodeReportMetadata(t *testing.T) {
	t.Parallel()

	t.Run("Successfully decode valid report metadata", func(t *testing.T) {
		originalMetadata := createTestReportMetadata()
		encodedData, err := originalMetadata.Encode()
		require.NoError(t, err)

		decodedMetadata, err := capcommon.DecodeReportMetadata(encodedData)
		require.NoError(t, err)
		require.Equal(t, originalMetadata.Version, decodedMetadata.Version)
		require.Equal(t, originalMetadata.ExecutionID, decodedMetadata.ExecutionID)
		require.Equal(t, originalMetadata.ReportID, decodedMetadata.ReportID)
		require.Equal(t, originalMetadata.WorkflowID, decodedMetadata.WorkflowID)
		require.Equal(t, originalMetadata.WorkflowOwner, decodedMetadata.WorkflowOwner)
	})

	t.Run("Fail to decode invalid data", func(t *testing.T) {
		invalidData := []byte("invalid data")
		_, err := capcommon.DecodeReportMetadata(invalidData)
		require.Error(t, err)
		require.Contains(t, err.Error(), "metadata: raw too short")
	})

	t.Run("Fail to decode empty data", func(t *testing.T) {
		emptyData := []byte{}
		_, err := capcommon.DecodeReportMetadata(emptyData)
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
			State:           contracts.TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHash,
				TxStatus:         evmtypes.TxSuccess,
				TxIdempotencyKey: "test-idempotency-key",
			}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil).Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, "test-idempotency-key").Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).Return(&receipt, nil)
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
			State:           contracts.TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHash,
				TxStatus:         evmtypes.TxSuccess,
				TxIdempotencyKey: "test-idempotency-key",
			}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil).Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, "test-idempotency-key").Return(nil, errors.New("fee calculation failed"))

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).Return(&receipt, nil)
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

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
		}
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := []*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		reply, responseMetadata, err := service.executeWriteReport(ctx, writeReportRequest, capabilitiesMetadata, monitoring.TelemetryContext{})
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, reply.TxStatus)

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

		// Short timeout so quick retry fails fast in tests
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		ctx = contexts.WithCRE(ctx, contexts.CRE{Workflow: "wf-id"})

		// Will be retried until context timeout
		expectedError := "transmission info error"
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(contracts.TransmissionInfo{}, errors.New(expectedError))

		reply, responseMetadata, err := service.executeWriteReport(ctx, writeReportRequest, capabilitiesMetadata, monitoring.TelemetryContext{})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
		// Verify we get the original error, not a context timeout
		require.NotContains(t, err.Error(), "context deadline exceeded")
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

		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(contracts.TransmissionInfo{
			Success:         false,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateNotAttempted,
		}, nil).Once()

		txHash := evmtypes.Hash(test.RandomBytes(32))

		mockForwarderClient.
			On("InvokeOnReport", mock.Anything, receiverAddress, signedReport, nonNilPositiveGasCfgMatcher()).
			Return(&evmtypes.TransactionResult{
				TxHash:           txHash,
				TxStatus:         evmtypes.TxSuccess,
				TxIdempotencyKey: "test-idempotency-key",
			}, nil)

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil).Once()

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}, nil)

		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, "test-idempotency-key").Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(3000),
		}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(3),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(3000),
		}, nil)

		result, err := service.WriteReport(t.Context(), capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Response)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)

		require.NotNil(t, result.ResponseMetadata.Metering)
		require.NotEmpty(t, result.ResponseMetadata.Metering)

		meteringData := result.ResponseMetadata.Metering
		require.Len(t, meteringData, 1)
		require.Equal(t, "GAS.1", meteringData[0].SpendUnit)
		require.NotEmpty(t, meteringData[0].SpendValue)
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

		transmissionInfo := contracts.TransmissionInfo{
			Success:         true,
			InvalidReceiver: false,
			State:           contracts.TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := []*evmtypes.Log{{TxHash: txHash, Data: successLogData(), BlockNumber: big.NewInt(100)}}
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(contracts.TransmissionStateSucceeded),
			TxHash:            txHash,
			GasUsed:           1000,
			EffectiveGasPrice: big.NewInt(2),
		}
		evmServiceMock.EXPECT().GetTransactionReceipt(mock.Anything, evmtypes.GeTransactionReceiptRequest{Hash: txHash, IsExternal: false}).Return(&receipt, nil)
		evmServiceMock.EXPECT().CalculateTransactionFee(mock.Anything, toReceiptGasInfo(receipt)).Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(2000),
		}, nil)

		result, err := service.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Response)
		require.Equal(t, evmcappb.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)

		require.Empty(t, result.ResponseMetadata.Metering)
	})
}

func TestParseReportResult(t *testing.T) {
	t.Parallel()

	t.Run("returns true for success result (0x01)", func(t *testing.T) {
		data := make([]byte, 32)
		data[31] = 0x01

		result, err := parseReportResult(data)
		require.NoError(t, err)
		require.True(t, result)
	})

	t.Run("returns false for failed result (0x00)", func(t *testing.T) {
		data := make([]byte, 32)

		result, err := parseReportResult(data)
		require.NoError(t, err)
		require.False(t, result)
	})

	t.Run("returns error for data shorter than 32 bytes", func(t *testing.T) {
		data := make([]byte, 31)

		_, err := parseReportResult(data)
		require.Error(t, err)
		require.Contains(t, err.Error(), "malformed log data: expected at least 32 bytes, got 31")
	})

	t.Run("returns error for empty data", func(t *testing.T) {
		_, err := parseReportResult([]byte{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "got 0")
	})

	t.Run("handles data longer than 32 bytes", func(t *testing.T) {
		data := make([]byte, 64)
		data[31] = 0x01

		result, err := parseReportResult(data)
		require.NoError(t, err)
		require.True(t, result)
	})

	t.Run("only checks byte 31 for result", func(t *testing.T) {
		// Any non-0x01 value at byte 31 is false
		data := make([]byte, 32)
		data[31] = 0x02

		result, err := parseReportResult(data)
		require.NoError(t, err)
		require.False(t, result)
	})
}

func TestBuildLogDetails(t *testing.T) {
	t.Parallel()

	t.Run("parses single successful log", func(t *testing.T) {
		txHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{{
			TxHash:      txHash,
			BlockNumber: big.NewInt(100),
			Data:        successLogData(),
		}}

		details, err := buildLogDetails(logs)

		require.NoError(t, err)
		require.Len(t, details, 1)
		require.Equal(t, txHash, details[0].TxHash)
		require.Equal(t, big.NewInt(100), details[0].BlockNumber)
		require.True(t, details[0].IsSuccess)
	})

	t.Run("parses single failed log", func(t *testing.T) {
		txHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{{
			TxHash:      txHash,
			BlockNumber: big.NewInt(200),
			Data:        failedLogData(),
		}}

		details, err := buildLogDetails(logs)

		require.NoError(t, err)
		require.Len(t, details, 1)
		require.Equal(t, txHash, details[0].TxHash)
		require.False(t, details[0].IsSuccess)
	})

	t.Run("returns error for malformed log data", func(t *testing.T) {
		txHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{{
			TxHash:      txHash,
			BlockNumber: big.NewInt(100),
			Data:        []byte{0x01}, // Too short
		}}

		_, err := buildLogDetails(logs)

		require.Error(t, err)
		require.Contains(t, err.Error(), "malformed log data")
		require.Contains(t, err.Error(), hex.EncodeToString(txHash[:]))
	})

	t.Run("returns error at first malformed log", func(t *testing.T) {
		goodTxHash := evmtypes.Hash(test.RandomBytes(32))
		badTxHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{
			{TxHash: goodTxHash, BlockNumber: big.NewInt(100), Data: successLogData()},
			{TxHash: badTxHash, BlockNumber: big.NewInt(101), Data: []byte{}}, // Malformed
		}

		_, err := buildLogDetails(logs)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse report result for tx")
		require.Contains(t, err.Error(), hex.EncodeToString(badTxHash[:]))
	})

	t.Run("parses multiple logs with mixed results", func(t *testing.T) {
		logs := []*evmtypes.Log{
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(100), Data: successLogData()},
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(101), Data: failedLogData()},
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(102), Data: successLogData()},
		}

		details, err := buildLogDetails(logs)

		require.NoError(t, err)
		require.Len(t, details, 3)
		require.True(t, details[0].IsSuccess)
		require.False(t, details[1].IsSuccess)
		require.True(t, details[2].IsSuccess)
	})

	t.Run("handles empty logs slice", func(t *testing.T) {
		details, err := buildLogDetails([]*evmtypes.Log{})
		require.NoError(t, err)
		require.Empty(t, details)
	})
}

func TestLogDetails_String(t *testing.T) {
	t.Parallel()

	t.Run("single logDetails formats correctly", func(t *testing.T) {
		txHash := evmtypes.Hash(test.RandomBytes(32))
		d := logDetails{
			TxHash:      txHash,
			BlockNumber: big.NewInt(100),
			IsSuccess:   true,
		}

		str := d.String()

		require.Contains(t, str, hex.EncodeToString(txHash[:]))
		require.Contains(t, str, "block=100")
		require.Contains(t, str, "result=success")
	})

	t.Run("failed log shows result=failed", func(t *testing.T) {
		d := logDetails{
			TxHash:      evmtypes.Hash(test.RandomBytes(32)),
			BlockNumber: big.NewInt(200),
			IsSuccess:   false,
		}

		require.Contains(t, d.String(), "result=failed")
	})
}

func TestLogDetailsList_String(t *testing.T) {
	t.Parallel()

	t.Run("formats multiple logs", func(t *testing.T) {
		details := logDetailsList{
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(100), IsSuccess: true},
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(101), IsSuccess: false},
		}

		str := details.String()

		require.Contains(t, str, "result=success")
		require.Contains(t, str, "result=failed")
		require.Contains(t, str, "block=100")
		require.Contains(t, str, "block=101")
	})

	t.Run("empty list returns []", func(t *testing.T) {
		details := logDetailsList{}
		require.Equal(t, "[]", details.String())
	})
}

func TestTxHashRetriever_GetSuccessfulTransmissionHash(t *testing.T) {
	t.Parallel()

	t.Run("returns hash when single successful log exists", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		txHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{{
			TxHash:      txHash,
			BlockNumber: big.NewInt(100),
			Data:        successLogData(),
		}}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, transmissionID.Receiver, transmissionID.WorkflowExecutionID, transmissionID.ReportID).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		result, err := retriever.GetSuccessfulTransmissionHash(ctx)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, txHash, *result)
	})

	t.Run("returns first successful hash when multiple logs exist", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		failedTxHash := evmtypes.Hash(test.RandomBytes(32))
		successTxHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{
			{TxHash: failedTxHash, BlockNumber: big.NewInt(100), Data: failedLogData()},
			{TxHash: successTxHash, BlockNumber: big.NewInt(101), Data: successLogData()},
		}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		result, err := retriever.GetSuccessfulTransmissionHash(ctx)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, successTxHash, *result)
	})

	t.Run("returns error when all logs are failed", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		logs := []*evmtypes.Log{
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(100), Data: failedLogData()},
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(101), Data: failedLogData()},
		}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		_, err := retriever.GetSuccessfulTransmissionHash(ctx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "no successful transmission found")
		require.Contains(t, err.Error(), "Found 2 transactions (all failed)")
	})

	t.Run("returns error when log data is malformed", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		logs := []*evmtypes.Log{{
			TxHash:      evmtypes.Hash(test.RandomBytes(32)),
			BlockNumber: big.NewInt(100),
			Data:        []byte{0x01}, // Too short
		}}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		_, err := retriever.GetSuccessfulTransmissionHash(ctx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "malformed log data")
	})
}

func TestTxHashRetriever_GetFailedTransmissionHash(t *testing.T) {
	t.Parallel()

	t.Run("returns hash when single failed log exists", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		txHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{{
			TxHash:      txHash,
			BlockNumber: big.NewInt(100),
			Data:        failedLogData(),
		}}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, transmissionID.Receiver, transmissionID.WorkflowExecutionID, transmissionID.ReportID).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		result, err := retriever.GetFailedTransmissionHash(ctx)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, txHash, *result)
	})

	t.Run("returns earliest hash by block number when multiple failed logs exist", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		earliestTxHash := evmtypes.Hash(test.RandomBytes(32))
		laterTxHash := evmtypes.Hash(test.RandomBytes(32))
		logs := []*evmtypes.Log{
			{TxHash: laterTxHash, BlockNumber: big.NewInt(200), Data: failedLogData()},
			{TxHash: earliestTxHash, BlockNumber: big.NewInt(100), Data: failedLogData()},
		}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		result, err := retriever.GetFailedTransmissionHash(ctx)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, earliestTxHash, *result)
	})

	t.Run("returns error when any log is successful", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		logs := []*evmtypes.Log{
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(100), Data: failedLogData()},
			{TxHash: evmtypes.Hash(test.RandomBytes(32)), BlockNumber: big.NewInt(101), Data: successLogData()},
		}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		_, err := retriever.GetFailedTransmissionHash(ctx)

		require.Error(t, err)
		require.ErrorIs(t, err, ErrUnexpectedSuccessfulTransmission)
	})

	t.Run("returns error when log data is malformed", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		mockForwarderClient := mocks.NewCREForwarderClient(t)

		transmissionID := contracts.TransmissionID{
			Receiver:            common.HexToAddress("0x1234"),
			WorkflowExecutionID: [32]byte{1, 2, 3},
			ReportID:            [2]byte{0x00, 0x01},
		}

		logs := []*evmtypes.Log{{
			TxHash:      evmtypes.Hash(test.RandomBytes(32)),
			BlockNumber: big.NewInt(100),
			Data:        []byte{}, // Empty, malformed
		}}

		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(logs, nil)

		retriever := NewTxHashRetriever(mockForwarderClient, testLogger, transmissionID)
		_, err := retriever.GetFailedTransmissionHash(ctx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "malformed log data")
	})
}
