package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
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
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil).Once()

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
			State:           TransmissionStateSucceeded,
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
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash}}, nil)

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
			State:           TransmissionStateSucceeded,
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
				State:           TransmissionStateInvalidReceiver,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		// Tx hash must be discovered from processed events
		txHash := evmtypes.Hash(test.RandomBytes(32))
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHash}}, nil)

		// Receipt + fee calculation for that tx hash
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
		//   if transmissionInfo.GasLimit.Uint64() > txGasLimit { ... GetHash ... if err return err }
		// txGasLimit computed == desiredReceiverGas
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(contracts.TransmissionInfo{
				Success:         false,
				InvalidReceiver: false,
				State:           TransmissionStateFailed,
				GasLimit:        new(big.Int).SetUint64(desiredReceiverGas + 1), // strictly greater => no retry path
			}, nil)

		// Force TxHashRetriever.GetHash() to fail - will be retried until context timeout
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
			State:           TransmissionStateFailed,
			GasLimit:        new(big.Int).SetUint64(desiredReceiverGas + 1),
		}
		mockForwarderClient.
			On("GetTransmissionInfo", mock.Anything, mock.Anything).
			Return(transmissionInfo, nil).
			Once()

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
			ErrorMessage:                    ptr("Receiver contract execution failure"),
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
				State:           TransmissionStateFailed,
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
				State:           TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
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
		test.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
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
				State:           TransmissionStateNotAttempted,
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
				State:           TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
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

		test.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
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
				State:           TransmissionStateNotAttempted,
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
				State:           TransmissionStateNotAttempted,
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
				State:           TransmissionStateSucceeded,
				GasLimit:        big.NewInt(EnoughReceiverGas),
			}, nil).
			Once()

		// TxHashRetriever should be used in duplicate tx branch.
		mockForwarderClient.EXPECT().
			GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{{TxHash: txHashFromLogs}}, nil)

		// Receipt should be fetched for the *log* hash, not the txmgr hash.
		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
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

		test.ValidateMeteringWriteReport(t, txResult.ResponseMetadata, 1, "0.0000000000000003")
	})

	t.Run("Invalid transmission state (default switch) => returns error", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		_, mockForwarderClient, service := createMocksAndCapability(t, testLogger)

		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		const invalidState = TransmissionStateFailed + 10

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
			State:           TransmissionStateNotAttempted,
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
			State:           TransmissionStateSucceeded,
		}
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{TxHash: txHash})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
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
			State:           TransmissionStateNotAttempted,
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
			State:           TransmissionStateSucceeded,
			GasLimit:        big.NewInt(EnoughReceiverGas),
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil).Once()

		evmServiceMock.EXPECT().GetTransactionFee(mock.Anything, "test-idempotency-key").Return(&evmtypes.TransactionFee{
			TransactionFee: big.NewInt(3000),
		}, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
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
			State:           TransmissionStateSucceeded,
		}
		mockForwarderClient.On("GetTransmissionInfo", mock.Anything, transmissionID).Return(transmissionInfo, nil)

		txHash := evmtypes.Hash(test.RandomBytes(32))

		logs := append([]*evmtypes.Log{}, &evmtypes.Log{TxHash: txHash})
		mockForwarderClient.EXPECT().GetReportProcessedEvents(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(logs, nil)

		receipt := evmtypes.Receipt{
			Status:            uint64(TransmissionStateSucceeded),
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
