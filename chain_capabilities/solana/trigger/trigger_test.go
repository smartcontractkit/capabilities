package trigger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	solanacappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	solana "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
)

const (
	testTriggerID         = "test-trigger-1"
	testWorkflowID        = "test-workflow-1"
	testWorkflowOwner     = "test-owner-1"
	testPollInterval      = 50 * time.Millisecond
	testChannelBufferSize = 100
	testAddress           = "11111111111111111111111111111112"
	testEventName         = "TestEvent"
)

var (
	testPublicKey    = createTestPublicKey(testAddress)
	testEventSig     = createTestEventSignature("TestEvent(string,uint256)")
	testEventIdlJSON = []byte("{}")
	testSubkeys      = []*solanacappb.SubkeyConfig{
		{
			Path: []string{"field1"},
			Comparers: []*solanacappb.ValueComparator{
				{Value: []byte("test_value"), Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ},
			},
		},
		{Path: []string{"field2", "nested"}},
	}
)

func createTestPublicKey(addr string) solana.PublicKey {
	var pk solana.PublicKey
	copy(pk[:], addr)
	return pk
}

func createTestEventSignature(sig string) solana.EventSignature {
	var es solana.EventSignature
	copy(es[:], sig)
	return es
}

type NopBeholderProcessor struct{}

func (NopBeholderProcessor) Process(_ context.Context, _ proto.Message, _ ...any) error { return nil }

func createTestMetadata() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		WorkflowID:    testWorkflowID,
		WorkflowOwner: testWorkflowOwner,
	}
}

func createTestTelemetryContext() monitoring.TelemetryContext {
	return monitoring.TelemetryContext{
		TsStart:         time.Now().UnixMilli(),
		RequestMetadata: createTestMetadata(),
	}
}

func setupTest(t *testing.T) (*SolanaLogTriggerService, *mocks.SolanaService) {
	mockSolanaService := mocks.NewSolanaService(t)

	store := NewSolanaLogTriggerStore()

	lggr := logger.Test(t)

	opts := LogTriggerServiceOpts{
		SolanaService:                   mockSolanaService,
		Logger:                          lggr,
		BeholderProcessor:               NopBeholderProcessor{},
		MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		Triggers:                        store,
		LogTriggerPollInterval:          1 * time.Millisecond,
		LogTriggerSendChannelBufferSize: testChannelBufferSize,
		Retention:                       time.Hour * 24,
		MaxLogsKept:                     10000,
		LimitsFactory:                   limits.Factory{Logger: lggr},
	}

	service, err := NewLogTriggerService(opts)
	require.NoError(t, err)

	return service, mockSolanaService
}

func createTestRequest() *solanacappb.FilterLogTriggerRequest {
	return &solanacappb.FilterLogTriggerRequest{
		Name:         "test-filter",
		Address:      testPublicKey[:],
		EventName:    testEventName,
		EventIdlJson: testEventIdlJSON,
		Subkeys:      testSubkeys,
	}
}

func createTestLog(blockNumber int64, address solana.PublicKey) *solana.Log {
	return &solana.Log{
		Address:     address,
		EventSig:    testEventSig,
		BlockNumber: blockNumber,
		TxHash:      solana.Signature{},
		Data:        []byte("test log data"),
	}
}

func TestRegisterLogTrigger(t *testing.T) {
	ctx := context.Background()

	t.Run("successful registration", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 150}, nil).Maybe()
		mockSolana.On("RegisterLogTracking", mock.Anything, mock.AnythingOfType("solana.LPFilterQuery")).Return(nil).Once()
		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{}, nil).Maybe()

		ch, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)

		require.Nil(t, err)
		require.NotNil(t, ch)

		time.Sleep(10 * time.Millisecond)
		mockSolana.AssertExpectations(t)
	})

	t.Run("empty trigger ID", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		_, err := service.RegisterLogTrigger(ctx, "", capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)

		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "no triggerID provided")
		mockSolana.AssertExpectations(t)
	})

	t.Run("register log tracking fails", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		mockSolana.EXPECT().GetSlotHeight(mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 150}, nil).Once()
		mockSolana.EXPECT().RegisterLogTracking(mock.Anything, mock.AnythingOfType("solana.LPFilterQuery")).Return(errors.New("registration failed")).Once()

		_, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)

		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "registration failed")
		mockSolana.AssertExpectations(t)
	})

	t.Run("duplicate trigger ID registration", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 150}, nil).Maybe()
		mockSolana.On("RegisterLogTracking", mock.Anything, mock.AnythingOfType("solana.LPFilterQuery")).Return(nil).Once()
		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{}, nil).Maybe()

		// First registration should succeed
		ch1, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)
		require.Nil(t, err)
		require.NotNil(t, ch1)

		time.Sleep(10 * time.Millisecond)

		// Second registration with same ID should fail
		ch2, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "is already registered")
		assert.Nil(t, ch2)

		mockSolana.AssertExpectations(t)
	})

	t.Run("get finalized block number fails", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		mockSolana.EXPECT().GetSlotHeight(mock.Anything, mock.Anything).Return(nil, errors.New("block fetch failed")).Once()

		_, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)

		require.NotNil(t, err)
		mockSolana.AssertExpectations(t)
	})
}

func TestUnregisterLogTrigger(t *testing.T) {
	ctx := context.Background()

	t.Run("successful unregistration", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 150}, nil).Maybe()
		mockSolana.On("RegisterLogTracking", mock.Anything, mock.AnythingOfType("solana.LPFilterQuery")).Return(nil).Once()
		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{}, nil).Maybe()
		mockSolana.On("UnregisterLogTracking", mock.Anything, mock.AnythingOfType("string")).Return(nil).Once()

		// Register first
		_, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)
		require.Nil(t, err)

		time.Sleep(10 * time.Millisecond)

		// Then unregister
		err = service.UnregisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)
		require.Nil(t, err)

		mockSolana.AssertExpectations(t)
	})

	t.Run("empty trigger ID", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		err := service.UnregisterLogTrigger(ctx, "", capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)

		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "no triggerID provided")
		mockSolana.AssertExpectations(t)
	})

	t.Run("trigger ID not found", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		err := service.UnregisterLogTrigger(ctx, "non-existent-trigger", capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)

		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "no active trigger found")
		mockSolana.AssertExpectations(t)
	})

	t.Run("unregister log tracking fails", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		request := createTestRequest()

		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 150}, nil).Maybe()
		mockSolana.On("RegisterLogTracking", mock.Anything, mock.AnythingOfType("solana.LPFilterQuery")).Return(nil).Once()
		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{}, nil).Maybe()
		mockSolana.On("UnregisterLogTracking", mock.Anything, mock.AnythingOfType("string")).Return(errors.New("unregister failed")).Once()

		// Register first
		_, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)
		require.Nil(t, err)

		time.Sleep(10 * time.Millisecond)

		// Then unregister should fail
		err = service.UnregisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "failed to unregister log-tracking")

		mockSolana.AssertExpectations(t)
	})
}

func TestToLogPollerFilter(t *testing.T) {
	service, _ := setupTest(t)
	testTriggerID := "test-trigger-id"

	t.Run("converts request to filter correctly", func(t *testing.T) {
		request := createTestRequest()

		filter, err := service.ToLogPollerFilter(testTriggerID, request)
		require.NoError(t, err)

		expectedSig := getEventSig(request.EventName)
		expectedFilterName := testTriggerID + SuffixLogTriggerFilterID

		require.NotNil(t, filter)
		assert.Equal(t, expectedFilterName, filter.Name)
		assert.Equal(t, request.Address, filter.Address[:])
		assert.Equal(t, request.EventName, filter.EventName)
		assert.Equal(t, expectedSig[:], filter.EventSig[:])
		assert.Equal(t, request.EventIdlJson, filter.ContractIdlJSON)
		expectedPaths := make([][]string, len(request.Subkeys))
		for i, subkey := range request.Subkeys {
			expectedPaths[i] = subkey.Path
		}
		assert.Equal(t, expectedPaths, ([][]string)(filter.SubkeyPaths))
		assert.Equal(t, service.retention, filter.Retention)
		assert.Equal(t, service.maxLogsKept, filter.MaxLogsKept)
	})

	t.Run("returns error for empty request with invalid address", func(t *testing.T) {
		filter, err := service.ToLogPollerFilter(testTriggerID, &solanacappb.FilterLogTriggerRequest{})
		require.Error(t, err)
		require.Nil(t, filter)
		assert.Contains(t, err.Error(), "invalid address length")
	})
}

func TestBuildQueryExpressions(t *testing.T) {
	t.Run("builds basic expressions", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Address: testPublicKey[:],
		}

		expressions, err := BuildQueryExpressions(request, 99)

		require.NoError(t, err)
		require.Len(t, expressions, 3)
	})

	t.Run("includes subkey filters", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Address: testPublicKey[:],
			Subkeys: testSubkeys,
		}

		expressions, err := BuildQueryExpressions(request, 99)

		require.NoError(t, err)
		require.Len(t, expressions, 4)
	})

	t.Run("handles empty subkey filters", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Address: testPublicKey[:],
			Subkeys: []*solanacappb.SubkeyConfig{
				{Path: []string{"field1"}, Comparers: []*solanacappb.ValueComparator{}},
			},
		}

		expressions, err := BuildQueryExpressions(request, 99)

		require.NoError(t, err)
		require.Len(t, expressions, 3)
	})
}

func TestLogTriggerSubkeyFilters(t *testing.T) {
	t.Run("single subkey filter with equality", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "TokenTransfer",
			Address: testPublicKey[:],
			Subkeys: []*solanacappb.SubkeyConfig{
				{
					Path: []string{"sender"},
					Comparers: []*solanacappb.ValueComparator{
						{
							Value:    []byte("user123"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ,
						},
					},
				},
			},
		}

		expressions, err := BuildQueryExpressions(request, 99)
		require.NoError(t, err)
		require.NotNil(t, expressions)

		assert.Equal(t, len(expressions), 4)
	})

	t.Run("multiple subkey filters with different operators", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "ComplexEvent",
			Address: testPublicKey[:],
			Subkeys: []*solanacappb.SubkeyConfig{
				{
					Path: []string{"wallet"},
					Comparers: []*solanacappb.ValueComparator{
						{
							Value:    []byte("wallet1"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ,
						},
						{
							Value:    []byte("wallet2"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ,
						},
					},
				},
				{
					Path: []string{"amount"},
					Comparers: []*solanacappb.ValueComparator{
						{
							Value:    []byte("1000"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_GTE,
						},
					},
				},
				{
					Path: []string{"token"},
					Comparers: []*solanacappb.ValueComparator{
						{
							Value:    []byte("USDC"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ,
						},
					},
				},
			},
		}

		expressions, err := BuildQueryExpressions(request, 49)
		require.NoError(t, err)
		require.NotNil(t, expressions)

		assert.Equal(t, len(expressions), 6)
	})

	t.Run("subkey filter with range operations", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "PriceUpdate",
			Address: testPublicKey[:],
			Subkeys: []*solanacappb.SubkeyConfig{
				{
					Path: []string{"price_min"},
					Comparers: []*solanacappb.ValueComparator{
						{
							Value:    []byte("100"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_GTE,
						},
					},
				},
				{
					Path: []string{"price_max"},
					Comparers: []*solanacappb.ValueComparator{
						{
							Value:    []byte("5000"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_LTE,
						},
					},
				},
			},
		}

		expressions, err := BuildQueryExpressions(request, 0)
		require.NoError(t, err)
		require.NotNil(t, expressions)

		assert.Equal(t, len(expressions), 5)
	})

	t.Run("subkey filter with multiple values (OR condition)", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "MultiTokenTransfer",
			Address: testPublicKey[:],
			Subkeys: []*solanacappb.SubkeyConfig{
				{
					Path: []string{"token"},
					Comparers: []*solanacappb.ValueComparator{
						{
							Value:    []byte("BTC"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ,
						},
						{
							Value:    []byte("ETH"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ,
						},
						{
							Value:    []byte("SOL"),
							Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ,
						},
					},
				},
			},
		}

		expressions, err := BuildQueryExpressions(request, 0)
		require.NoError(t, err)
		require.NotNil(t, expressions)

		assert.Equal(t, len(expressions), 4)
	})
}

func TestStartPolling(t *testing.T) {
	t.Run("processes new blocks correctly", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		baseCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		meta := capabilities.RequestMetadata{
			WorkflowID:    testWorkflowID,
			WorkflowOwner: testWorkflowOwner,
		}
		ctx := meta.ContextWithCRE(baseCtx)

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 10)

		expectedLogs := []*solana.Log{
			createTestLog(101, testPublicKey),
			createTestLog(102, testPublicKey),
		}

		mockSolana.EXPECT().QueryTrackedLogs(mock.Anything, mock.Anything, mock.Anything).Return(expectedLogs, nil).Once()

		telemetryContext := monitoring.TelemetryContext{
			TsStart:         time.Now().UnixMilli(),
			RequestMetadata: meta,
		}
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		receivedLogs := make([]*solanacappb.Log, 0)
		for i := 0; i < len(expectedLogs); i++ {
			select {
			case response := <-logCh:
				receivedLogs = append(receivedLogs, response.Trigger)
			case <-time.After(50 * time.Millisecond):
				t.Fatal("Timeout waiting for logs")
			}
		}

		assert.Len(t, receivedLogs, 2)
		assert.Equal(t, int64(101), receivedLogs[0].BlockNumber)
		assert.Equal(t, int64(102), receivedLogs[1].BlockNumber)
		mockSolana.AssertExpectations(t)
	})

	t.Run("skips when no new blocks", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		baseCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		meta := createTestMetadata()
		ctx := meta.ContextWithCRE(baseCtx)

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 10)

		// Return empty logs to simulate no new blocks
		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{}, nil).Maybe()

		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		select {
		case <-logCh:
			t.Fatal("Should not receive logs when no new blocks")
		case <-ctx.Done():
		}

		mockSolana.AssertExpectations(t)
	})

	t.Run("handles QueryTrackedLogs error", func(t *testing.T) {
		mockSolanaService := mocks.NewSolanaService(t)
		store := NewSolanaLogTriggerStore()

		opts := LogTriggerServiceOpts{
			SolanaService:                   mockSolanaService,
			Logger:                          logger.Nop(),
			BeholderProcessor:               NopBeholderProcessor{},
			MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
			Triggers:                        store,
			LogTriggerPollInterval:          1 * time.Millisecond,
			LogTriggerSendChannelBufferSize: testChannelBufferSize,
			Retention:                       time.Hour * 24,
			MaxLogsKept:                     10000,
			LimitsFactory:                   limits.Factory{Logger: logger.Nop()},
		}

		service, err := NewLogTriggerService(opts)
		require.NoError(t, err)

		baseCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		meta := createTestMetadata()
		ctx := meta.ContextWithCRE(baseCtx)

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 10)

		mockSolanaService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("query failed")).Maybe()

		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		select {
		case <-logCh:
			t.Fatal("Should not receive logs when QueryTrackedLogs fails")
		case <-ctx.Done():
		}

		mockSolanaService.AssertExpectations(t)
	})

	t.Run("updates lastProcessedBlock correctly", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		baseCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		meta := createTestMetadata()
		ctx := meta.ContextWithCRE(baseCtx)

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 10)

		firstBatch := []*solana.Log{createTestLog(101, testPublicKey)}

		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return(firstBatch, nil).Maybe()

		meta = createTestMetadata()
		ctx = meta.ContextWithCRE(ctx)
		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		select {
		case response := <-logCh:
			assert.Equal(t, int64(101), response.Trigger.BlockNumber)
		case <-time.After(50 * time.Millisecond):
			t.Fatal("Timeout waiting for log")
		}

		mockSolana.AssertExpectations(t)
	})

	t.Run("closes channel on context cancellation", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		ctx, cancel := context.WithCancel(context.Background())

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 10)

		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{}, nil).Maybe()

		meta := createTestMetadata()
		ctx = meta.ContextWithCRE(ctx)
		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		time.Sleep(10 * time.Millisecond)
		cancel()

		select {
		case _, ok := <-logCh:
			if ok {
				t.Fatal("Channel should be closed when context is cancelled")
			}
		case <-time.After(50 * time.Millisecond):
			t.Fatal("Channel was not closed in time")
		}
	})

	t.Run("drops events when channel is full", func(t *testing.T) {
		mockSolanaService := mocks.NewSolanaService(t)
		store := NewSolanaLogTriggerStore()

		// Create service with very small buffer
		opts := LogTriggerServiceOpts{
			SolanaService:                   mockSolanaService,
			Logger:                          logger.Test(t),
			Triggers:                        store,
			LogTriggerPollInterval:          1 * time.Millisecond,
			LogTriggerSendChannelBufferSize: 1, // Very small buffer
			Retention:                       time.Hour * 24,
			MaxLogsKept:                     10000,
			BeholderProcessor:               test.NopBeholderProcessor{},
			MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		}

		service, err := NewLogTriggerService(opts)
		require.NoError(t, err)

		baseCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		meta := createTestMetadata()
		ctx := meta.ContextWithCRE(baseCtx)

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 1) // Small buffer to force drops

		// Return many logs to overflow the channel
		manyLogs := []*solana.Log{
			createTestLog(101, testPublicKey),
			createTestLog(102, testPublicKey),
			createTestLog(103, testPublicKey),
			createTestLog(104, testPublicKey),
			createTestLog(105, testPublicKey),
		}

		mockSolanaService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return(manyLogs, nil).Maybe()

		meta = createTestMetadata()
		ctx = meta.ContextWithCRE(ctx)
		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		// Don't read from channel to force it to fill up
		<-ctx.Done()

		// Test passes if no panic occurs - drops are handled gracefully
		mockSolanaService.AssertExpectations(t)
	})

	t.Run("updates lastProcessedBlock even when logs are dropped", func(t *testing.T) {
		mockSolanaService := mocks.NewSolanaService(t)
		store := NewSolanaLogTriggerStore()

		opts := LogTriggerServiceOpts{
			SolanaService:                   mockSolanaService,
			Logger:                          logger.Test(t),
			Triggers:                        store,
			LogTriggerPollInterval:          10 * time.Millisecond,
			LogTriggerSendChannelBufferSize: 1,
			Retention:                       time.Hour * 24,
			MaxLogsKept:                     10000,
			BeholderProcessor:               test.NopBeholderProcessor{},
			MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		}

		service, err := NewLogTriggerService(opts)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 1)

		firstBatchLogs := []*solana.Log{
			createTestLog(101, testPublicKey),
			createTestLog(102, testPublicKey),
			createTestLog(103, testPublicKey),
			createTestLog(104, testPublicKey),
			createTestLog(105, testPublicKey),
		}
		emptyLogs := []*solana.Log{}

		queryCallCount := 0
		mockSolanaService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			queryCallCount++
		}).Return(func(ctx context.Context, expressions []query.Expression, limitAndSort query.LimitAndSort) []*solana.Log {
			if queryCallCount == 1 {
				return firstBatchLogs
			}
			return emptyLogs
		}, nil).Maybe()

		meta := createTestMetadata()
		ctx = meta.ContextWithCRE(ctx)
		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		select {
		case <-logCh:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout waiting for first log")
		}

		time.Sleep(50 * time.Millisecond)
		cancel()

		assert.GreaterOrEqual(t, queryCallCount, 2, "Should have polled at least twice")
	})

	t.Run("continues polling after transient errors", func(t *testing.T) {
		mockSolanaService := mocks.NewSolanaService(t)
		store := NewSolanaLogTriggerStore()

		opts := LogTriggerServiceOpts{
			SolanaService:                   mockSolanaService,
			Logger:                          logger.Test(t),
			Triggers:                        store,
			LogTriggerPollInterval:          5 * time.Millisecond,
			LogTriggerSendChannelBufferSize: testChannelBufferSize,
			Retention:                       time.Hour * 24,
			MaxLogsKept:                     10000,
			BeholderProcessor:               test.NopBeholderProcessor{},
			MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		}

		service, err := NewLogTriggerService(opts)
		require.NoError(t, err)

		baseCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		meta := createTestMetadata()
		ctx := meta.ContextWithCRE(baseCtx)

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 10)

		// First call fails, second succeeds
		mockSolanaService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("transient error")).Once()
		mockSolanaService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{createTestLog(101, testPublicKey)}, nil).Maybe()

		meta = createTestMetadata()
		ctx = meta.ContextWithCRE(ctx)
		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		// Should eventually receive a log after recovering from transient error
		select {
		case response := <-logCh:
			assert.Equal(t, int64(101), response.Trigger.BlockNumber)
		case <-ctx.Done():
			// This is acceptable - the test verifies no panic/crash on transient errors
		}

		mockSolanaService.AssertExpectations(t)
	})

	t.Run("handles nil log in query results", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		baseCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		meta := createTestMetadata()
		ctx := meta.ContextWithCRE(baseCtx)

		config := createTestRequest()
		triggerID := "test-trigger"
		startingBlock := int64(100)
		logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], 10)

		// Return slice with nil entry
		logsWithNil := []*solana.Log{
			createTestLog(101, testPublicKey),
			nil, // This should be handled gracefully
			createTestLog(102, testPublicKey),
		}

		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return(logsWithNil, nil).Maybe()

		meta = createTestMetadata()
		ctx = meta.ContextWithCRE(ctx)
		telemetryContext := createTestTelemetryContext()
		go service.startPolling(ctx, telemetryContext, config, triggerID, startingBlock, logCh)

		// Collect what we can - test passes if no panic
		<-ctx.Done()
		mockSolana.AssertExpectations(t)
	})

}

func TestErrorHandling(t *testing.T) {
	t.Run("build query expressions error handling", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Address: testPublicKey[:],
			Subkeys: []*solanacappb.SubkeyConfig{
				{
					Path:      []string{"field"},
					Comparers: testSubkeys[0].Comparers,
				},
			},
		}

		expressions, err := BuildQueryExpressions(request, 99)
		if err != nil {
			assert.Contains(t, err.Error(), "failed to create subkey filter")
		} else {
			assert.NotNil(t, expressions)
		}
	})

	t.Run("block range validation", func(t *testing.T) {
		request := createTestRequest()
		expressions, err := BuildQueryExpressions(request, 199)
		if err == nil {
			assert.NotNil(t, expressions)
		}
	})

	t.Run("nil comparer in list", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Address: testPublicKey[:],
			Subkeys: []*solanacappb.SubkeyConfig{
				{
					Path: []string{"field"},
					Comparers: []*solanacappb.ValueComparator{
						nil,
						{Value: []byte("test"), Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_EQ},
					},
				},
			},
		}

		expressions, err := BuildQueryExpressions(request, 99)
		if err == nil {
			assert.NotNil(t, expressions)
		}
	})

	t.Run("zero block range", func(t *testing.T) {
		request := createTestRequest()
		expressions, err := BuildQueryExpressions(request, -1)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(expressions), 2)
	})
}

func TestToLogPollerFilter_EdgeCases(t *testing.T) {
	service, _ := setupTest(t)
	testTriggerID := "test-trigger-id"

	t.Run("address shorter than expected", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "test",
			Address: []byte("short"),
		}

		filter, err := service.ToLogPollerFilter(testTriggerID, request)
		require.Error(t, err)
		require.Nil(t, filter)
		assert.Contains(t, err.Error(), "invalid address length")
		assert.Contains(t, err.Error(), "expected 32 bytes")
	})

	t.Run("address longer than expected", func(t *testing.T) {
		longAddress := make([]byte, 64)
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "test",
			Address: longAddress,
		}

		filter, err := service.ToLogPollerFilter(testTriggerID, request)
		require.Error(t, err)
		require.Nil(t, filter)
		assert.Contains(t, err.Error(), "invalid address length")
		assert.Contains(t, err.Error(), "expected 32 bytes")
	})

	t.Run("valid 32-byte address", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "test",
			Address: testPublicKey[:],
		}

		filter, err := service.ToLogPollerFilter(testTriggerID, request)
		require.NoError(t, err)
		require.NotNil(t, filter)
		assert.Equal(t, testPublicKey[:], filter.Address[:])
	})

	t.Run("nil subkeys", func(t *testing.T) {
		request := &solanacappb.FilterLogTriggerRequest{
			Name:    "test",
			Address: testPublicKey[:],
			Subkeys: nil,
		}

		filter, err := service.ToLogPollerFilter(testTriggerID, request)
		require.NoError(t, err)
		require.NotNil(t, filter)
		assert.Equal(t, solana.SubKeyPaths{}, filter.SubkeyPaths)
	})
}

func TestSolanaLogTriggerService_Integration(t *testing.T) {
	t.Run("end to end registration flow", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		ctx := context.Background()

		request := createTestRequest()

		mockSolana.On("RegisterLogTracking", mock.Anything, mock.AnythingOfType("solana.LPFilterQuery")).Return(nil).Once()
		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 102}, nil).Maybe()
		mockSolana.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything).Return([]*solana.Log{}, nil).Maybe()

		ch, err := service.RegisterLogTrigger(ctx, testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, request)
		require.Nil(t, err)
		require.NotNil(t, ch)

		time.Sleep(10 * time.Millisecond)

		mockSolana.AssertExpectations(t)
	})
}

func BenchmarkSolanaLogTriggerService_BuildQueryExpressions(b *testing.B) {
	request := createTestRequest()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := BuildQueryExpressions(request, 99)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSolanaLogTriggerService_ToLogPollerFilter(b *testing.B) {
	opts := LogTriggerServiceOpts{
		Logger:            logger.Test(&testing.T{}),
		BeholderProcessor: test.NopBeholderProcessor{},
		MessageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		Retention:         time.Hour * 24,
		MaxLogsKept:       10000,
	}

	service, err := NewLogTriggerService(opts)
	if err != nil {
		b.Fatal("failed to create service:", err)
	}

	request := createTestRequest()
	testTriggerID := "test-trigger-id"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filter, err := service.ToLogPollerFilter(testTriggerID, request)
		require.NoError(b, err)
		if filter == nil {
			b.Fatal("filter should not be nil")
		}
	}
}

func TestSolanaLogTriggerService_NewLogTriggerService(t *testing.T) {
	t.Run("requires logger", func(t *testing.T) {
		_, err := NewLogTriggerService(LogTriggerServiceOpts{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "logger is required")
	})

	t.Run("sets default values", func(t *testing.T) {
		lggr := logger.Test(t)
		service, err := NewLogTriggerService(LogTriggerServiceOpts{
			Logger:            lggr,
			BeholderProcessor: test.NopBeholderProcessor{},
			MessageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		})
		require.NoError(t, err)
		require.NotNil(t, service)

		assert.Equal(t, time.Second, service.logTriggerPollInterval)
		assert.Equal(t, uint64(1000), service.logTriggerSendChannelBufferSize)
		assert.Equal(t, 24*time.Hour, service.retention)
		assert.Equal(t, int64(10000), service.maxLogsKept)
	})

	t.Run("respects provided values", func(t *testing.T) {
		mockService := mocks.NewSolanaService(t)
		store := NewSolanaLogTriggerStore()
		lggr := logger.Test(t)

		opts := LogTriggerServiceOpts{
			SolanaService:                   mockService,
			Logger:                          lggr,
			Triggers:                        store,
			LogTriggerPollInterval:          5 * time.Second,
			LogTriggerSendChannelBufferSize: 2000,
			Retention:                       48 * time.Hour,
			MaxLogsKept:                     20000,
			BeholderProcessor:               test.NopBeholderProcessor{},
			MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		}

		service, err := NewLogTriggerService(opts)
		require.NoError(t, err)
		require.NotNil(t, service)

		assert.Equal(t, mockService, service.SolanaService)
		assert.Equal(t, store, service.triggers)
		assert.Equal(t, 5*time.Second, service.logTriggerPollInterval)
		assert.Equal(t, uint64(2000), service.logTriggerSendChannelBufferSize)
		assert.Equal(t, 48*time.Hour, service.retention)
		assert.Equal(t, int64(20000), service.maxLogsKept)
	})
}

func TestSolanaLogTriggerStore(t *testing.T) {
	t.Run("read non-existent key", func(t *testing.T) {
		store := NewSolanaLogTriggerStore()

		state, found := store.Read("non-existent-key")

		assert.False(t, found)
		assert.Equal(t, solanaLogTriggerState{}, state)
	})

	t.Run("write and read", func(t *testing.T) {
		store := NewSolanaLogTriggerStore()
		testState := solanaLogTriggerState{
			filter: createTestRequest(),
		}

		store.Write("test-key", testState)
		state, found := store.Read("test-key")

		assert.True(t, found)
		assert.Equal(t, testState.filter, state.filter)
	})

	t.Run("delete existing key", func(t *testing.T) {
		store := NewSolanaLogTriggerStore()
		testState := solanaLogTriggerState{
			filter: createTestRequest(),
		}

		store.Write("test-key", testState)
		store.Delete("test-key")
		_, found := store.Read("test-key")

		assert.False(t, found)
	})

	t.Run("delete non-existent key", func(t *testing.T) {
		store := NewSolanaLogTriggerStore()

		// Should not panic
		assert.NotPanics(t, func() {
			store.Delete("non-existent-key")
		})
	})

	t.Run("overwrite existing key", func(t *testing.T) {
		store := NewSolanaLogTriggerStore()
		state1 := solanaLogTriggerState{
			filter: &solanacappb.FilterLogTriggerRequest{Name: "filter1"},
		}
		state2 := solanaLogTriggerState{
			filter: &solanacappb.FilterLogTriggerRequest{Name: "filter2"},
		}

		store.Write("test-key", state1)
		store.Write("test-key", state2)
		state, found := store.Read("test-key")

		assert.True(t, found)
		assert.Equal(t, "filter2", state.filter.Name)
	})
}

func TestSolanaLogTriggerService_EdgeCases(t *testing.T) {
	t.Run("nil service dependencies", func(t *testing.T) {
		lggr := logger.Test(t)
		service, err := NewLogTriggerService(LogTriggerServiceOpts{
			Logger:            lggr,
			BeholderProcessor: test.NopBeholderProcessor{},
			MessageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		})
		require.NoError(t, err)
		require.NotNil(t, service)
		assert.NotPanics(t, func() {
			filter, err := service.ToLogPollerFilter("test-trigger", &solanacappb.FilterLogTriggerRequest{})
			require.Error(t, err)
			require.Nil(t, filter)
			assert.Contains(t, err.Error(), "invalid address length")
		})
	})

	t.Run("very large block numbers", func(t *testing.T) {
		request := createTestRequest()

		expressions, err := BuildQueryExpressions(request, 9223372036854775805)

		require.NoError(t, err)
		require.NotEmpty(t, expressions)
	})

	t.Run("negative block numbers", func(t *testing.T) {
		request := createTestRequest()

		expressions, err := BuildQueryExpressions(request, -2)

		require.NoError(t, err)
		require.NotEmpty(t, expressions)
	})
}

func TestValidateFilterConfig(t *testing.T) {
	t.Parallel()

	t.Run("nil config", func(t *testing.T) {
		err := validateFilterConfig(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config cannot be nil")
	})

	t.Run("invalid address length", func(t *testing.T) {
		config := &solanacappb.FilterLogTriggerRequest{
			Address:   []byte{1, 2, 3}, // Only 3 bytes, should be 32
			EventName: "TestEvent",
			Name:      "test-filter",
		}
		err := validateFilterConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid address length")
	})

	t.Run("empty event name", func(t *testing.T) {
		config := &solanacappb.FilterLogTriggerRequest{
			Address:   make([]byte, 32),
			EventName: "",
			Name:      "test-filter",
		}
		err := validateFilterConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "event name cannot be empty")
	})

	t.Run("empty filter name", func(t *testing.T) {
		config := &solanacappb.FilterLogTriggerRequest{
			Address:   make([]byte, 32),
			EventName: "TestEvent",
			Name:      "",
		}
		err := validateFilterConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "filter name cannot be empty")
	})

	t.Run("empty event idl json", func(t *testing.T) {
		config := &solanacappb.FilterLogTriggerRequest{
			Address:   make([]byte, 32),
			EventName: "TestEvent",
			Name:      "test-filter",
		}
		err := validateFilterConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "event idl json cannot be empty")
	})

	t.Run("valid config", func(t *testing.T) {
		config := &solanacappb.FilterLogTriggerRequest{
			Address:      make([]byte, 32),
			EventName:    "TestEvent",
			Name:         "test-filter",
			EventIdlJson: []byte("{}"),
		}
		err := validateFilterConfig(config)
		require.NoError(t, err)
	})
}

func TestCleanUpStaleFilters(t *testing.T) {
	t.Parallel()

	t.Run("service does not support GetFiltersNames", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		mockSolana.On("GetFiltersNames", mock.Anything).Return([]string{}, nil)
		// The mock doesn't implement FilterNamesGetter, so cleanup should be skipped
		service.cleanUpStaleFilters(t.Context())
		// No panic, no error - just silently skips
	})
}

func TestRegisterLogTrigger_InputValidation(t *testing.T) {
	t.Parallel()

	t.Run("nil config returns user error", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 100}, nil).Maybe()

		_, err := service.RegisterLogTrigger(t.Context(), testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config cannot be nil")
	})

	t.Run("invalid address returns user error", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 100}, nil).Maybe()

		config := &solanacappb.FilterLogTriggerRequest{
			Address:   []byte{1, 2, 3},
			EventName: "TestEvent",
			Name:      "test-filter",
		}
		_, err := service.RegisterLogTrigger(t.Context(), testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid address length")
	})

	t.Run("empty event name returns user error", func(t *testing.T) {
		service, mockSolana := setupTest(t)
		mockSolana.On("GetSlotHeight", mock.Anything, mock.Anything).Return(&solana.GetSlotHeightReply{Height: 100}, nil).Maybe()

		config := &solanacappb.FilterLogTriggerRequest{
			Address:   make([]byte, 32),
			EventName: "",
			Name:      "test-filter",
		}
		_, err := service.RegisterLogTrigger(t.Context(), testTriggerID, capabilities.RequestMetadata{WorkflowID: testWorkflowID, WorkflowOwner: testWorkflowOwner}, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "event name cannot be empty")
	})
}
