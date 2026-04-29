package actions

import (
	"context"
	"testing"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
)

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

func requireTxInfoPhaseEvent(t *testing.T, msg *monitoring.WriteReportTxInfoRetrievalPhase, lookupType string, phase uint32, result string, txHash string, transmitter aptos_sdk.AccountAddress) {
	t.Helper()
	require.Equal(t, phase, msg.GetPhase())
	require.Equal(t, result, msg.GetResult())
	require.Equal(t, txHash, msg.GetTxHash())
	require.Equal(t, transmitter.String(), msg.GetTransmitter())
	require.Equal(t, lookupType, msg.GetLookupType())
}

func TestGetSuccessfulTransmissionInfo(t *testing.T) {
	t.Parallel()

	transmitter := aptos_sdk.AccountAddress{0xEE}

	t.Run("Phase 1 fails - no txns found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := thr.GetSuccessfulTransmissionInfo(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transmitter transactions during phase 1")
		require.Len(t, processor.messages, 1)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeSuccessfulTransmission, 1, txInfoRetrievalResultFetchError, "", transmitter)
	})

	t.Run("Phase 1 finds - returns gas info", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := requestStartTime.UnixMicro()
		matchingTx := buildFakeTransactionWithGas(t, "0xfound", true, 100, recentTs, targetReportMetadata, 500, 100)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		result, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound", result.TxHash)
		require.Equal(t, uint64(500), result.GasUsed)
		require.Equal(t, uint64(100), result.GasUnitPrice)
		require.Len(t, processor.messages, 1)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeSuccessfulTransmission, 1, txInfoRetrievalResultFound, "0xfound", transmitter)
	})

	t.Run("Phase 1 misses, Phase 2 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := requestStartTime.UnixMicro()
		unrelatedTx := buildFakeTransaction(t, "0xunrelated", true, 200, recentTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase2", true, 50, recentTs-500000, targetReportMetadata)

		// Phase 1: returns unrelated tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedTx}, nil).Once()
		// Phase 2 pagination: returns matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		result, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase2", result.TxHash)
		require.Len(t, processor.messages, 2)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeSuccessfulTransmission, 1, txInfoRetrievalResultNotFound, "", transmitter)
		requireTxInfoPhaseEvent(t, processor.messages[1], txInfoRetrievalLookupTypeSuccessfulTransmission, 2, txInfoRetrievalResultFound, "0xfound_in_phase2", transmitter)
	})

	t.Run("Phase 1 misses, Phase 2 misses but covers time, Phase 3 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := requestStartTime.UnixMicro()
		oldTs := requestStartTime.Add(-2 * time.Minute).UnixMicro()
		unrelatedRecentTx := buildFakeTransaction(t, "0xunrelated1", true, 200, recentTs, randomReportMetadata)
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated2", true, 50, oldTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase3", true, 300, recentTs, targetReportMetadata)

		// Phase 1: unrelated tx with recent timestamp
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedRecentTx}, nil).Once()
		// Phase 2 pagination: unrelated tx with old timestamp (covers the window, exits loop)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()
		// Phase 3 polling: matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		result, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase3", result.TxHash)
		require.Len(t, processor.messages, 3)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeSuccessfulTransmission, 1, txInfoRetrievalResultNotFound, "", transmitter)
		requireTxInfoPhaseEvent(t, processor.messages[1], txInfoRetrievalLookupTypeSuccessfulTransmission, 2, txInfoRetrievalResultNotFound, "", transmitter)
		requireTxInfoPhaseEvent(t, processor.messages[2], txInfoRetrievalLookupTypeSuccessfulTransmission, 3, txInfoRetrievalResultFound, "0xfound_in_phase3", transmitter)
	})

	t.Run("Phase 1 misses, skips Phase 2, Phase 3 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		oldTs := requestStartTime.Add(-2 * time.Minute).UnixMicro()
		recentTs := requestStartTime.UnixMicro()
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", true, 50, oldTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase3", true, 300, recentTs, targetReportMetadata)

		// Phase 1: unrelated tx with old timestamp (earliestTxTimestamp <= startingPointMicro, skip phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()
		// Phase 3 polling: matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		result, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase3", result.TxHash)
		require.Len(t, processor.messages, 2)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeSuccessfulTransmission, 1, txInfoRetrievalResultNotFound, "", transmitter)
		requireTxInfoPhaseEvent(t, processor.messages[1], txInfoRetrievalLookupTypeSuccessfulTransmission, 3, txInfoRetrievalResultFound, "0xfound_in_phase3", transmitter)
	})

	t.Run("Phase 1 misses, skips Phase 2, Phase 3 fails", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		oldTs := requestStartTime.Add(-2 * time.Minute).UnixMicro()
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", true, 50, oldTs, randomReportMetadata)

		// All GetTransmitterTransactions calls return unrelated tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		_, err := thr.GetSuccessfulTransmissionInfo(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "matching transmission not found yet")
		require.Len(t, processor.messages, 2)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeSuccessfulTransmission, 1, txInfoRetrievalResultNotFound, "", transmitter)
		requireTxInfoPhaseEvent(t, processor.messages[1], txInfoRetrievalLookupTypeSuccessfulTransmission, 3, txInfoRetrievalResultNotFound, "", transmitter)
	})
}

func TestGetFailedTransmissionInfo(t *testing.T) {
	t.Parallel()

	transmitter := aptos_sdk.AccountAddress{0xEE}

	t.Run("Phase 1 finds matching failed tx with vmStatus", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := requestStartTime.UnixMicro()
		matchingTx := buildFakeTransaction(t, "0xfailed_phase1", false, 100, recentTs, targetRM)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		result, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfailed_phase1", result.TxHash)
		require.Equal(t, "Move abort", result.VmStatus)
		require.Len(t, processor.messages, 1)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeFailedTransmission, 1, txInfoRetrievalResultFound, "0xfailed_phase1", transmitter)
	})

	t.Run("Phase 1 fails - no txns found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetRM, requestStartTime)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := thr.GetFailedTransmissionInfo(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transmitter transactions during phase 1")
		require.Len(t, processor.messages, 1)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeFailedTransmission, 1, txInfoRetrievalResultFetchError, "", transmitter)
	})

	t.Run("Phase 1 misses, Phase 2 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := requestStartTime.UnixMicro()
		unrelatedTx := buildFakeTransaction(t, "0xunrelated", false, 200, recentTs, otherRM)
		matchingTx := buildFakeTransaction(t, "0xfailed_phase2", false, 50, recentTs-500000, targetRM)

		// Phase 1: unrelated failed tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedTx}, nil).Once()
		// Phase 2 pagination: matching failed tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		result, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfailed_phase2", result.TxHash)
		require.Equal(t, "Move abort", result.VmStatus)
		require.Len(t, processor.messages, 2)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeFailedTransmission, 1, txInfoRetrievalResultNotFound, "", transmitter)
		requireTxInfoPhaseEvent(t, processor.messages[1], txInfoRetrievalLookupTypeFailedTransmission, 2, txInfoRetrievalResultFound, "0xfailed_phase2", transmitter)
	})

	t.Run("Phase 1 misses, skips Phase 2, returns not found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetRM, requestStartTime)

		oldTs := requestStartTime.Add(-2 * time.Minute).UnixMicro()
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", false, 50, oldTs, otherRM)

		// Phase 1: unrelated tx with old timestamp (skip phase 2, no phase 3)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()

		_, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no matching failed transaction found")
		require.Len(t, processor.messages, 1)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeFailedTransmission, 1, txInfoRetrievalResultNotFound, "", transmitter)
	})

	t.Run("Phase 1 misses, Phase 2 misses but covers time, returns not found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, processor := newTestTxInfoRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := requestStartTime.UnixMicro()
		oldTs := requestStartTime.Add(-2 * time.Minute).UnixMicro()
		unrelatedRecentTx := buildFakeTransaction(t, "0xunrelated1", false, 200, recentTs, otherRM)
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated2", false, 50, oldTs, otherRM)

		// Phase 1: unrelated tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedRecentTx}, nil).Once()
		// Phase 2: unrelated tx with old timestamp (covers window, no match, no phase 3)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()

		_, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no matching failed transaction found")
		require.Len(t, processor.messages, 2)
		requireTxInfoPhaseEvent(t, processor.messages[0], txInfoRetrievalLookupTypeFailedTransmission, 1, txInfoRetrievalResultNotFound, "", transmitter)
		requireTxInfoPhaseEvent(t, processor.messages[1], txInfoRetrievalLookupTypeFailedTransmission, 2, txInfoRetrievalResultNotFound, "", transmitter)
	})
}

func TestPayloadMatching(t *testing.T) {
	t.Parallel()

	transmitter := aptos_sdk.AccountAddress{0xEE}

	t.Run("different report body does not match", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr, _ := newTestTxInfoRetriever(t, mockClient, targetRM, requestStartTime)

		// Create a tx with the same metadata IDs but a different report body
		alteredRM := targetRM
		alteredRM.Timestamp = targetRM.Timestamp + 999 // changes the encoded bytes

		// Use old timestamp so phase 2 pagination is skipped → returns not found immediately
		oldTs := requestStartTime.Add(-2 * time.Minute).UnixMicro()
		mismatchTx := buildFakeTransaction(t, "0xmismatch", false, 100, oldTs, alteredRM)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{mismatchTx}, nil)

		_, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no matching failed transaction found")
	})

}
