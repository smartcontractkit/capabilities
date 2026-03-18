package actions

import (
	"context"
	"testing"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
)

func TestGetSuccessfulTransmissionInfo(t *testing.T) {
	t.Parallel()

	transmitter := aptos_sdk.AccountAddress{0xEE}

	t.Run("Phase 1 fails - no txns found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := thr.GetSuccessfulTransmissionInfo(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transmitter transactions during phase 1")
	})

	t.Run("Phase 1 finds and returns tx info", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := unixMicroUint64(t, requestStartTime)
		matchingTx := buildFakeTransactionWithGas(t, "0xfound", true, 100, recentTs, targetReportMetadata, testGasUsed, testGasUnitPrice)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		info, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound", info.TxHash)
		require.Equal(t, testGasUsed, info.GasUsed)
		require.Equal(t, testGasUnitPrice, info.GasUnitPrice)
	})

	t.Run("Phase 1 misses, Phase 2 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := unixMicroUint64(t, requestStartTime)
		unrelatedTx := buildFakeTransaction(t, "0xunrelated", true, 200, recentTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase2", true, 50, recentTs-500000, targetReportMetadata)

		// Phase 1: returns unrelated tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedTx}, nil).Once()
		// Phase 2 pagination: returns matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		info, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase2", info.TxHash)
	})

	t.Run("Phase 1 misses, Phase 2 misses but covers time, Phase 3 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		recentTs := unixMicroUint64(t, requestStartTime)
		oldTs := unixMicroUint64(t, requestStartTime.Add(-2*time.Minute))
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

		info, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase3", info.TxHash)
	})

	t.Run("Phase 1 misses, skips Phase 2, Phase 3 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		oldTs := unixMicroUint64(t, requestStartTime.Add(-2*time.Minute))
		recentTs := unixMicroUint64(t, requestStartTime)
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", true, 50, oldTs, randomReportMetadata)
		matchingTx := buildFakeTransaction(t, "0xfound_in_phase3", true, 300, recentTs, targetReportMetadata)

		// Phase 1: unrelated tx with old timestamp (earliestTxTimestamp <= startingPointMicro, skip phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()
		// Phase 3 polling: matching tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		info, err := thr.GetSuccessfulTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfound_in_phase3", info.TxHash)
	})

	t.Run("Phase 1 misses, skips Phase 2, Phase 3 fails", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetReportMetadata, _, _ := newReportFixture(t)
		randomReportMetadata, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetReportMetadata, requestStartTime)

		oldTs := unixMicroUint64(t, requestStartTime.Add(-2*time.Minute))
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", true, 50, oldTs, randomReportMetadata)

		// All GetTransmitterTransactions calls return unrelated tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		_, err := thr.GetSuccessfulTransmissionInfo(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "matching transmission not found yet")
	})
}

func TestGetFailedTransmissionInfo(t *testing.T) {
	t.Parallel()

	transmitter := aptos_sdk.AccountAddress{0xEE}

	t.Run("Phase 1 finds matching failed tx", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := unixMicroUint64(t, requestStartTime)
		matchingTx := buildFakeTransaction(t, "0xfailed_phase1", false, 100, recentTs, targetRM)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		info, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfailed_phase1", info.TxHash)
	})

	t.Run("Phase 1 finds matching failed tx info", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := unixMicroUint64(t, requestStartTime)
		matchingTx := buildFakeTransactionWithDetails(t, "0xfailed_info", false, 100, recentTs, targetRM, testGasUsed, testGasUnitPrice, "Out of gas")

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		info, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfailed_info", info.TxHash)
		require.Equal(t, testGasUsed, info.GasUsed)
		require.Equal(t, testGasUnitPrice, info.GasUnitPrice)
		require.Equal(t, "Out of gas", info.VMStatus)
	})

	t.Run("Phase 1 fails - no txns found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := thr.GetFailedTransmissionInfo(ctx, transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transmitter transactions during phase 1")
	})

	t.Run("Phase 1 misses, Phase 2 finds", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := unixMicroUint64(t, requestStartTime)
		unrelatedTx := buildFakeTransaction(t, "0xunrelated", false, 200, recentTs, otherRM)
		matchingTx := buildFakeTransaction(t, "0xfailed_phase2", false, 50, recentTs-500000, targetRM)

		// Phase 1: unrelated failed tx with recent timestamp (triggers phase 2)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedTx}, nil).Once()
		// Phase 2 pagination: matching failed tx
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{matchingTx}, nil).Once()

		info, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.NoError(t, err)
		require.Equal(t, "0xfailed_phase2", info.TxHash)
	})

	t.Run("Phase 1 misses, skips Phase 2, returns not found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		oldTs := unixMicroUint64(t, requestStartTime.Add(-2*time.Minute))
		unrelatedOldTx := buildFakeTransaction(t, "0xunrelated", false, 50, oldTs, otherRM)

		// Phase 1: unrelated tx with old timestamp (skip phase 2, no phase 3)
		mockClient.On("GetTransmitterTransactions", mock.Anything, transmitter, mock.Anything, mock.Anything).
			Return([]*aptostypes.Transaction{unrelatedOldTx}, nil).Once()

		_, err := thr.GetFailedTransmissionInfo(t.Context(), transmitter)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no matching failed transaction found")
	})

	t.Run("Phase 1 misses, Phase 2 misses but covers time, returns not found", func(t *testing.T) {
		mockClient := NewCREForwarderClient_mock(t)
		targetRM, _, _ := newReportFixture(t)
		otherRM, _, _ := newReportFixture(t)
		requestStartTime := time.Now()
		thr := newTestTxHashRetriever(t, mockClient, targetRM, requestStartTime)

		recentTs := unixMicroUint64(t, requestStartTime)
		oldTs := unixMicroUint64(t, requestStartTime.Add(-2*time.Minute))
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
	})
}
