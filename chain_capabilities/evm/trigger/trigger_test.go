package trigger

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/evm"
)

var (
	expectedAddress = []byte{0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x11, 0x22, 0x33, 0x44}
	addresses       = [][]byte{
		expectedAddress,
	}
	brokenAddresses = [][]byte{
		{0xad, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x11, 0x22, 0x33, 0x44},
	}
	eventSig0Example = []byte{
		0xdd, 0xf2, 0x52, 0xad, 0x1b, 0xe2, 0xc8, 0x9b,
		0x69, 0xc2, 0xb0, 0x68, 0xfc, 0x37, 0x8d, 0xaa,
		0x95, 0x2b, 0xa7, 0xf1, 0x63, 0xc4, 0xa1, 0x16,
		0x28, 0xf5, 0x5a, 0x4d, 0xf5, 0x23, 0xb3, 0xef,
	}
	topicsWithEventSig0 = []*evmcappb.TopicValues{
		{Values: [][]byte{eventSig0Example}},
	}

	triggerID        = "trigger-1"
	latestExpHead    = evmtypes.Head{Number: big.NewInt(30)}
	finalizedExpHead = evmtypes.Head{Number: big.NewInt(25)}
	pollInterval     = 10 * time.Millisecond
)

func initMocks(t *testing.T) *evmmock.EVMService {
	t.Helper()
	evmSvc := evmmock.NewEVMService(t)
	return evmSvc
}

func TestLogTriggerService_Close_WaitsForPollingGoroutine(t *testing.T) {
	t.Run("close awaits on syncGroup to finalize", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lggr := logger.Test(t)
		evmService := initMocks(t)
		evmService.On("LatestAndFinalizedHead", mock.Anything).Return(latestExpHead, finalizedExpHead, nil)
		evmService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*evmtypes.Log{}, nil)
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
		evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
		store := NewLogTriggerStore()
		service := NewLogTriggerService(evmService, store, lggr, 10*time.Millisecond)
		err := service.Start(ctx)
		require.NoError(t, err)
		ch, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			Topics:    topicsWithEventSig0,
		})
		require.NoError(t, err)
		require.NotNil(t, ch)
		time.Sleep(10 * time.Millisecond) // let it run a bit more
		_, exists := store.Read(triggerID)
		require.True(t, exists)

		// Wait a bit to ensure polling goroutine is running
		time.Sleep(100 * time.Millisecond)

		done := make(chan struct{})
		go func() {
			require.NoError(t, service.Close())
			close(done)
		}()

		// Unregister to allow polling goroutine to exit
		err = service.UnregisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{})
		require.NoError(t, err)
		_, exists = store.Read(triggerID)
		require.False(t, exists)

		select {
		case <-done:
			// Success: Close() returned after goroutine finished
		case <-time.After(1 * time.Second):
			t.Fatal("Close() did not return in time, likely did not wait for goroutine startPolling() to finish")
		}
	})
}

// testing all the input parameters and some minor validations
func TestRegisterLogTrigger_InputValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lggr := logger.Test(t)
	service := NewLogTriggerService(nil, NewLogTriggerStore(), lggr, pollInterval)

	t.Run("missing triggerID", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(ctx, "", capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), "no triggerID provided")
	})

	t.Run("already registered triggerID", func(t *testing.T) {
		store := NewLogTriggerStore()
		service := NewLogTriggerService(nil, store, lggr, pollInterval)
		// we simulate a RegisterLogTrigger() by tampering the store
		store.Write(triggerID, logTriggerState{})
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
		})
		require.Error(t, err)
		require.Equal(t, "triggerID \"trigger-1\" is already registered", err.Error())
	})

	t.Run("missing addresses", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: [][]byte{},
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), "no valid addresses provided (at least one address is required)")
	})

	t.Run("too many topics", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			Topics: []*evmcappb.TopicValues{
				{Values: [][]byte{}},
				{Values: [][]byte{}},
				{Values: [][]byte{}},
				{Values: [][]byte{}},
				{Values: [][]byte{}}, // 5th topic, should fail
			},
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), "there can be at most 4 topics provided, got 5 instead")
	})

	t.Run("missing eventSig", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), "no valid event sig provided (at least one event sig is required in topics)")

		_, err = service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			Topics:    []*evmcappb.TopicValues{},
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), "no valid event sig provided (at least one event sig is required in topics)")
	})

	t.Run("fail to get latest head", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{}, evmtypes.Head{}, errors.New("mocked failure error"))
		service := NewLogTriggerService(evmService, NewLogTriggerStore(), lggr, pollInterval)
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			Topics:    topicsWithEventSig0,
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), "failed to register latest and finalized head: 'mocked failure error' for triggerID: trigger-1")
	})

	t.Run("fail to register log-tracking", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{}, evmtypes.Head{}, nil)
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(errors.New("mocking error, making register failing on purpose")).Once()
		service := NewLogTriggerService(evmService, NewLogTriggerStore(), lggr, pollInterval)
		_, err := service.RegisterLogTrigger(ctx, triggerID+"-logtracking", capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: brokenAddresses,
			Topics:    topicsWithEventSig0,
		})
		require.Error(t, err)
		require.Equal(t,
			"failed to register log-tracking: 'mocking error, making register failing on purpose' for triggerID: trigger-1-logtracking, addresses: [[173 173 190 239 202 254 186 190 18 52 86 120 154 188 222 240 17 34 51 68]], eventSig: [[221 242 82 173 27 226 200 155 105 194 176 104 252 55 141 170 149 43 167 241 99 196 161 22 40 245 90 77 245 35 179 239]], topic2: [], topic3: [], topic4: []",
			err.Error())
	})
}

func TestUnregisterLogTrigger_InputValidation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := &LogTriggerService{}
	lggr := logger.Test(t)

	emptyMetadata := capabilities.RequestMetadata{}
	emptyRequest := &evmcappb.FilterLogTriggerRequest{}

	t.Run("missing triggerID", func(t *testing.T) {
		err := service.UnregisterLogTrigger(ctx, "", emptyMetadata, emptyRequest)
		require.Error(t, err)
		require.Equal(t, err.Error(), "no triggerID provided")
	})

	t.Run("no active trigger found", func(t *testing.T) {
		service := &LogTriggerService{
			triggers: NewLogTriggerStore(),
		}
		err := service.UnregisterLogTrigger(ctx, triggerID, emptyMetadata, emptyRequest)
		require.Error(t, err)
		require.Equal(t, err.Error(), "no active trigger found for triggerID: trigger-1")
	})

	t.Run("fail to unregister log-tracking", func(t *testing.T) {
		breakingTriggerID := "breaking-logTriggerUnregister"
		evmService := initMocks(t)
		evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(errors.New("mocking error, making unregister failing on purpose")).Once()
		service := NewLogTriggerService(evmService, NewLogTriggerStore(), lggr, pollInterval)

		service.triggers.Write(breakingTriggerID, logTriggerState{
			cancelFunc: func() {},
			lastBlock:  big.NewInt(0),
		})
		err := service.UnregisterLogTrigger(ctx, breakingTriggerID, emptyMetadata, emptyRequest)
		require.Error(t, err)
		require.Equal(t, err.Error(), "failed to unregister log-tracking: 'mocking error, making unregister failing on purpose' for triggerID: breaking-logTriggerUnregister")
	})
}

func TestGetTopics(t *testing.T) {
	t.Parallel()
	service := &LogTriggerService{}
	t.Run("only eventSigs provided", func(t *testing.T) {
		input := &evmcappb.FilterLogTriggerRequest{
			Topics: []*evmcappb.TopicValues{
				{Values: [][]byte{[]byte("eventSig1")}},
			},
		}
		eventSigs, topics2, topics3, topics4 := service.getTopics(input)
		require.Equal(t, [][]byte{[]byte("eventSig1")}, eventSigs)
		require.Nil(t, topics2)
		require.Nil(t, topics3)
		require.Nil(t, topics4)
	})

	t.Run("eventSigs and topic1 provided", func(t *testing.T) {
		input := &evmcappb.FilterLogTriggerRequest{
			Topics: []*evmcappb.TopicValues{
				{Values: [][]byte{[]byte("eventSig1")}},
				{Values: [][]byte{[]byte("topic2")}},
			},
		}
		eventSigs, topics2, topics3, topics4 := service.getTopics(input)
		require.Equal(t, [][]byte{[]byte("eventSig1")}, eventSigs)
		require.Equal(t, [][]byte{[]byte("topic2")}, topics2)
		require.Nil(t, topics3)
		require.Nil(t, topics4)
	})

	t.Run("eventSigs, topic1 and topic2 provided", func(t *testing.T) {
		input := &evmcappb.FilterLogTriggerRequest{
			Topics: []*evmcappb.TopicValues{
				{Values: [][]byte{[]byte("eventSig1")}},
				{Values: [][]byte{[]byte("topic2")}},
				{Values: [][]byte{[]byte("topic3")}},
			},
		}
		eventSigs, topics2, topics3, topics4 := service.getTopics(input)
		require.Equal(t, [][]byte{[]byte("eventSig1")}, eventSigs)
		require.Equal(t, [][]byte{[]byte("topic2")}, topics2)
		require.Equal(t, [][]byte{[]byte("topic3")}, topics3)
		require.Nil(t, topics4)
	})

	t.Run("all topics provided", func(t *testing.T) {
		input := &evmcappb.FilterLogTriggerRequest{
			Topics: []*evmcappb.TopicValues{
				{Values: [][]byte{[]byte("eventSig1")}},
				{Values: [][]byte{[]byte("topic2")}},
				{Values: [][]byte{[]byte("topic3")}},
				{Values: [][]byte{[]byte("topic4")}},
			},
		}
		eventSigs, topics2, topics3, topics4 := service.getTopics(input)
		require.Equal(t, [][]byte{[]byte("eventSig1")}, eventSigs)
		require.Equal(t, [][]byte{[]byte("topic2")}, topics2)
		require.Equal(t, [][]byte{[]byte("topic3")}, topics3)
		require.Equal(t, [][]byte{[]byte("topic4")}, topics4)
	})
}

func TestCreateLogRequest(t *testing.T) {
	service := NewLogTriggerService(nil, NewLogTriggerStore(), logger.Test(t), pollInterval)

	tests := []struct {
		name                                            string
		addresses, eventSigs, topics2, topics3, topics4 [][]byte
		confidence                                      evmcappb.ConfidenceLevel
		expectedConfidence                              primitives.ConfidenceLevel
		expectedExpressions                             []query.Expression
	}{
		{
			name:               "finalized confidence, single address and single eventSig and empty topics",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_FINALIZED,
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
			},
		},
		// TODO PLEX-1488: missing test for SAFE confidence level
		{
			name:               "latest confidence, single address and single eventSig and empty topics",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_LATEST,
			expectedConfidence: primitives.Unconfirmed,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
			},
		},
		{
			name:               "finalized confidence, single address and single eventSig and a topic for 2, 3, 4",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example},
			topics2:            [][]byte{eventSig0Example},
			topics3:            [][]byte{eventSig0Example},
			topics4:            [][]byte{eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_FINALIZED,
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				*service.makeEventByTopicFilter(1, [][]byte{eventSig0Example}),
				*service.makeEventByTopicFilter(2, [][]byte{eventSig0Example}),
				*service.makeEventByTopicFilter(3, [][]byte{eventSig0Example}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expressions, confidence := service.createLogRequest(context.Background(), tc.addresses,
				tc.eventSigs,
				tc.topics2,
				tc.topics3,
				tc.topics4,
				tc.confidence)
			require.NotNil(t, expressions)
			require.Len(t, expressions, len(tc.expectedExpressions))
			for i, expected := range tc.expectedExpressions {
				require.Equal(t, expected, expressions[i])
			}
			require.NotNil(t, service.limitAndSort)
			require.NotNil(t, service.limitAndSort.SortBy)
			require.Equal(t, query.NewSortByBlock(query.Asc), service.limitAndSort.SortBy[0])
			require.NotNil(t, confidence)
			require.Equal(t, tc.expectedConfidence, confidence)
		})
	}
}

func TestMakeEventByTopicFilter(t *testing.T) {
	service := &LogTriggerService{}
	type testCase struct {
		name            string
		topics          [][]byte
		isNilExpression bool
	}
	tests := []testCase{
		{
			name:            "zero topics",
			topics:          [][]byte{},
			isNilExpression: true,
		},
		{
			name:   "one topic",
			topics: [][]byte{eventSig0Example},
		},
		{
			name:   "two topics",
			topics: [][]byte{eventSig0Example, eventSig0Example},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr := service.makeEventByTopicFilter(10, tc.topics)
			if tc.isNilExpression {
				require.Nil(t, expr)
				return
			}
			ebt, ok := expr.Primitive.(*evm.EventByTopic)
			require.True(t, ok)
			require.Equal(t, ebt.Topic, uint64(10))
			require.Len(t, ebt.HashedValueComparers, 1)
			require.Len(t, ebt.HashedValueComparers[0].Values, len(tc.topics))
			require.Equal(t, primitives.Eq, ebt.HashedValueComparers[0].Operator)
		})
	}
}

func TestGetFinalizedBlockNumber(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lggr := logger.Test(t)
	t.Run("gets latest block number", func(t *testing.T) {
		evmService := initMocks(t)
		service := &LogTriggerService{
			lggr:       lggr,
			EVMService: evmService,
		}
		evmService.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{}, finalizedExpHead, nil)
		fromBlock, err := service.getFinalizedBlockNumber(ctx, triggerID)
		require.NoError(t, err)
		require.Equal(t, finalizedExpHead.Number, fromBlock)
	})
	t.Run("fails getting latest block number", func(t *testing.T) {
		evmService := initMocks(t)
		service := &LogTriggerService{
			lggr:       lggr,
			EVMService: evmService,
		}
		evmService.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{}, evmtypes.Head{}, errors.New("mocked failure error for LatestAndFinalizedHead"))
		_, err := service.getFinalizedBlockNumber(ctx, triggerID)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to register latest and finalized head: 'mocked failure error for LatestAndFinalizedHead' for triggerID: trigger-1")
	})
}

func TestGetLatestBlockNumber(t *testing.T) {
	t.Run("single log extracts value correctly", func(t *testing.T) {
		service := &LogTriggerService{}
		logs := []*evmtypes.Log{
			{
				BlockNumber: big.NewInt(5),
			},
		}
		currentBlock := big.NewInt(0)
		latestBlock := service.getLatestBlockNumber(logs, currentBlock, big.NewInt(10))
		require.Equal(t, big.NewInt(5), latestBlock)
	})

	t.Run("multiple logs with different block numbers mixed up", func(t *testing.T) {
		service := &LogTriggerService{}
		addr1 := stringToAddressBytes("addr1")
		addr2 := stringToAddressBytes("addr2")
		logs := []*evmtypes.Log{
			{
				Address:     addr1,
				BlockNumber: big.NewInt(2),
			},
			{
				Address:     addr1,
				BlockNumber: big.NewInt(3),
			},
			{
				Address:     addr2,
				BlockNumber: big.NewInt(2),
			},
		}
		currentBlock := big.NewInt(0)
		latestBlock := service.getLatestBlockNumber(logs, currentBlock, big.NewInt(10))
		require.Equal(t, big.NewInt(3), latestBlock)
	})

	t.Run("multiple logs with unfinalized blocks return highest one", func(t *testing.T) {
		service := &LogTriggerService{}
		addr1 := stringToAddressBytes("addr1")
		addr2 := stringToAddressBytes("addr2")
		logs := []*evmtypes.Log{
			{
				Address:     addr1,
				BlockNumber: big.NewInt(2),
			},
			{
				Address:     addr1,
				BlockNumber: big.NewInt(3),
			},
			{
				Address:     addr2,
				BlockNumber: big.NewInt(2),
			},
		}
		currentBlock := big.NewInt(0)
		latestBlock := service.getLatestBlockNumber(logs, currentBlock, big.NewInt(2))
		require.Equal(t, big.NewInt(2), latestBlock)
	})
}

func TestFetchLogsFromLogPoller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lggr := logger.Test(t)
	evmService := evmmock.NewEVMService(t)
	service := NewLogTriggerService(evmService, NewLogTriggerStore(), lggr, pollInterval)
	fromBlock := big.NewInt(10)
	state := logTriggerState{
		lastBlock: fromBlock,
		filter: filter{
			expressions: []query.Expression{},
			confidence:  primitives.Finalized,
		},
	}

	evmService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			filterQuery := args.Get(1).([]query.Expression)
			require.NotEmpty(t, filterQuery)
			require.Len(t, filterQuery, 1) // Expecting only the block number expression
			require.Equal(t, query.Block(fromBlock.String(), primitives.Gt), filterQuery[0])
			confidenceLevel := args.Get(3).(primitives.ConfidenceLevel)
			require.NotEmpty(t, confidenceLevel)
			require.Equal(t, state.confidence, confidenceLevel)
		}).
		Return([]*evmtypes.Log{
			{
				BlockNumber: big.NewInt(11),
			},
		}, nil).Once()

	logs, err := service.fetchLogsFromLogPoller(ctx, state)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, big.NewInt(11), logs[0].BlockNumber)

	require.Len(t, state.expressions, 0, "state expressions should not be modified by fetchLogsFromLogPoller")
}

func TestSendLogsToWorkflows(t *testing.T) {
	lggr := logger.Test(t)
	service := &LogTriggerService{
		lggr:     lggr,
		triggers: NewLogTriggerStore(),
	}

	finalizedBlockNumber := big.NewInt(1)
	expectedLog1 := &evmtypes.Log{
		TxHash:      stringToHashBytes("txhash1"),
		BlockHash:   stringToHashBytes("blockhash1"),
		LogIndex:    1,
		BlockNumber: big.NewInt(1),
	}
	expectedLog2 := &evmtypes.Log{
		TxHash:      stringToHashBytes("txhash2"),
		BlockHash:   stringToHashBytes("blockhash2"),
		LogIndex:    2,
		BlockNumber: big.NewInt(2),
	}
	expectedLogs := []*evmtypes.Log{expectedLog1, expectedLog2}

	t.Run("all logs are sent to the channel", func(t *testing.T) {
		service.triggers.Write(triggerID, logTriggerState{
			unfinalizedSentEventIDs: map[string]*big.Int{},
		})
		state, _ := service.triggers.Read(triggerID)
		logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], len(expectedLogs))

		err := service.sendLogsToWorkflows(expectedLogs, finalizedBlockNumber, triggerID, state, logCh)
		require.NoError(t, err)
		require.Len(t, logCh, len(expectedLogs))
		actualLog1 := <-logCh
		require.Equal(t, createTriggerResponse(expectedLog1, service), actualLog1)
		actualLog2 := <-logCh
		require.Equal(t, createTriggerResponse(expectedLog2, service), actualLog2)
		select {
		case msg := <-logCh:
			t.Fatalf("unexpected message received: %+v", msg)
		default:
			// no message received, as expected
		}
	})

	t.Run("first log sent to channel second log dropped out due to timeout", func(t *testing.T) {
		logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], 1) // buffer size of 1, so it can only hold one log at a time
		service.triggers.Write(triggerID, logTriggerState{
			unfinalizedSentEventIDs: map[string]*big.Int{},
		})
		state, _ := service.triggers.Read(triggerID)
		err := service.sendLogsToWorkflows(expectedLogs, big.NewInt(0), triggerID, state, logCh)
		require.NoError(t, err)
		require.Len(t, logCh, 1)
		actualLog1 := <-logCh
		require.Equal(t, createTriggerResponse(expectedLog1, service), actualLog1)
		select {
		case msg := <-logCh:
			t.Fatalf("unexpected message received: %+v", msg)
		default:
			// no message received, as expected
		}
		state, _ = service.triggers.Read(triggerID)
		require.Len(t, state.unfinalizedSentEventIDs, 1, "expected one unfinalized sent event ID to be stored, as the 2nd one overflowed the channel")
		logID1 := service.generateLogIdentifier(expectedLog1)
		require.Equal(t, expectedLog1.BlockNumber, state.unfinalizedSentEventIDs[logID1])
	})

	t.Run("store unfinalized logs in store and do not re-send them", func(t *testing.T) {
		logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], 1)
		service.triggers.Write(triggerID, logTriggerState{
			unfinalizedSentEventIDs: map[string]*big.Int{},
		})
		triggerState, _ := service.triggers.Read(triggerID)
		err := service.sendLogsToWorkflows([]*evmtypes.Log{expectedLog2}, finalizedBlockNumber, triggerID, triggerState, logCh)
		require.NoError(t, err)
		require.Len(t, logCh, 1)
		actualLog2 := <-logCh
		require.Equal(t, createTriggerResponse(expectedLog2, service), actualLog2)
		select {
		case msg := <-logCh:
			t.Fatalf("unexpected message received: %+v", msg)
		default:
			// no message received, as expected
		}
		// Verify that the unfinalized log is stored in the trigger state
		triggerState, _ = service.triggers.Read(triggerID)
		require.Len(t, triggerState.unfinalizedSentEventIDs, 1, "expected one unfinalized sent event ID to be stored")
		require.Contains(t, triggerState.unfinalizedSentEventIDs, service.generateLogIdentifier(expectedLog2), "expected the unfinalized log to be stored in the trigger state")
		// Verify that the unfinalized log is not sent again
		err = service.sendLogsToWorkflows([]*evmtypes.Log{expectedLog2}, finalizedBlockNumber, triggerID, triggerState, logCh)
		require.NoError(t, err)
		require.Len(t, logCh, 0)
		select {
		case msg := <-logCh:
			t.Fatalf("unexpected message received: %+v, log was stored already nothing should be received", msg)
		default:
			// no message received, as expected
		}
	})

	t.Run("prune logs that went fron unfinalized to finalized", func(t *testing.T) {
		service.triggers.Write(triggerID, logTriggerState{
			unfinalizedSentEventIDs: map[string]*big.Int{
				"fakeId":  big.NewInt(0),
				"fakeId2": finalizedBlockNumber,
				"fakeId3": big.NewInt(2),
			},
		})
		triggerState, _ := service.triggers.Read(triggerID)
		logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], len(expectedLogs))

		err := service.sendLogsToWorkflows([]*evmtypes.Log{}, finalizedBlockNumber, triggerID, triggerState, logCh)
		require.NoError(t, err)
		require.Len(t, logCh, 0)
		select {
		case msg := <-logCh:
			t.Fatalf("unexpected message received: %+v", msg)
		default:
			// no message received, as expected
		}
		triggerState, _ = service.triggers.Read(triggerID)
		require.Len(t, triggerState.unfinalizedSentEventIDs, 1, "expected only one unfinalized sent event ID to remain after pruning")
		require.Equal(t, big.NewInt(2), triggerState.unfinalizedSentEventIDs["fakeId3"], "expected only the unfinalized log to remain in the state after pruning")
	})
	t.Run("failing to update state", func(t *testing.T) {
		service := &LogTriggerService{
			lggr:     lggr,
			triggers: NewLogTriggerStore(),
		}
		state := logTriggerState{
			unfinalizedSentEventIDs: map[string]*big.Int{},
		}
		logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], len(expectedLogs))
		err := service.sendLogsToWorkflows(expectedLogs, finalizedBlockNumber, triggerID, state, logCh)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to update unfinalized sent event IDs for triggerID: trigger-1: cannot find trigger with ID \"trigger-1\"")
	})
}

func TestIntegration_RegisterAndUnregisterLogTrigger(t *testing.T) {
	lggr := logger.Test(t)
	evmService := initMocks(t)
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()

	// two calls, one for the starting offset and a second one for the next block
	evmService.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{}, evmtypes.Head{Number: big.NewInt(25)}, nil).Twice()
	// single call, for fetching the latest finalized head and check if the offset has to be adjusted
	evmService.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{}, evmtypes.Head{Number: big.NewInt(26)}, nil).Once()
	// Mocking the QueryTrackedLogs method to return logs for the test (1st call) and then a second log for the next block (2nd call)
	nextBlockNumber := new(big.Int).Add(finalizedExpHead.Number, big.NewInt(1))
	message := []byte(assemblyDataMessage(evmtypes.Address(expectedAddress), nextBlockNumber))
	nextBlockNumber2 := new(big.Int).Add(nextBlockNumber, big.NewInt(1))
	message2 := []byte(assemblyDataMessage(evmtypes.Address(expectedAddress), nextBlockNumber2))
	log2 := createLog(0, nextBlockNumber2, evmtypes.Address(expectedAddress), message2)

	evmService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*evmtypes.Log{
		createLog(1, nextBlockNumber, evmtypes.Address(expectedAddress), message),
		log2,
	}, nil).Once()
	nextBlockNumber3 := new(big.Int).Add(nextBlockNumber2, big.NewInt(1))
	message = []byte(assemblyDataMessage(evmtypes.Address(expectedAddress), nextBlockNumber3))
	evmService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*evmtypes.Log{
		createLog(2, nextBlockNumber3, evmtypes.Address(expectedAddress), message),
	}, nil).Once()

	service := NewLogTriggerService(evmService, NewLogTriggerStore(), lggr, pollInterval)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	triggerID := "trigger-integration"

	tickCh := make(chan time.Time)
	defaultTickerFactory = &mockTickerFactory{C1: tickCh}
	require.Empty(t, service.triggers.ReadAll())

	ch, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
		Addresses: addresses,
		Topics:    topicsWithEventSig0,
	})
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond) // let it run a bit more
	triggerState, exists := service.triggers.Read(triggerID)
	require.True(t, exists, "expected trigger to be registered")
	require.Len(t, service.triggers.ReadAll(), 1, "expected one and only one trigger to be registered")
	require.Len(t, triggerState.unfinalizedSentEventIDs, 0, "expected no unfinalized sent event IDs stored in trigger state before registration")

	validateLog := func(msg *capabilities.TriggerAndId[*evmservice.Log], expectedBlock *big.Int) {
		logConverted := &evmtypes.Log{
			TxHash:    evmtypes.Hash(msg.Trigger.TxHash),
			BlockHash: evmtypes.Hash(msg.Trigger.BlockHash),
			LogIndex:  msg.Trigger.Index,
		}
		require.Equal(t, service.generateLogIdentifier(logConverted), msg.Id)
		log0 := msg.Trigger
		require.Equal(t, expectedAddress, log0.Address)
		expectedMessage := assemblyDataMessage(evmtypes.Address(expectedAddress), expectedBlock)
		require.Equal(t, expectedMessage, string(log0.GetData()), "expected log data to match the expected message: %s", expectedMessage)
	}

	tickCh <- time.Now()
	time.Sleep(20 * time.Millisecond) // let it run a bit more

	select {
	case msg := <-ch:
		validateLog(&msg, big.NewInt(int64(26))) // 26 = 25 (latest block) + 1 of the next block mocked service method QueryTrackedLogs
	default:
		t.Fatal("expected at least one log after registration")
	}

	select {
	case msg := <-ch:
		validateLog(&msg, big.NewInt(int64(27)))
	default:
		t.Fatal("expected second log after registration")
	}

	triggerState, exists = service.triggers.Read(triggerID)
	require.True(t, exists)
	require.Len(t, triggerState.unfinalizedSentEventIDs, 2, "expected two unfinalized sent event IDs stored in trigger state")

	tickCh <- time.Now()
	time.Sleep(5 * time.Millisecond) // let it run a bit more

	select {
	case msg := <-ch:
		validateLog(&msg, big.NewInt(int64(28))) // 28 = 27 (latest block) + 1 of the next block mocked service method QueryTrackedLogs
	default:
		t.Fatal("expected a third log")
	}

	triggerState, exists = service.triggers.Read(triggerID)
	require.True(t, exists)
	require.Len(t, triggerState.unfinalizedSentEventIDs, 2, "expected two unfinalized sent event IDs stored in trigger state")
	logID2 := service.generateLogIdentifier(log2)
	require.Equal(t, log2.BlockNumber, triggerState.unfinalizedSentEventIDs[logID2])

	err = service.UnregisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{})
	require.NoError(t, err)
	_, exists = service.triggers.Read(triggerID)
	require.False(t, exists, "no trigger should come up with that ID after unregistering")
	require.Len(t, service.triggers.ReadAll(), 0, "no triggers should be registered at this point")

	// Wait to confirm no more messages after unregister
	msg := <-ch
	lggr.Debugf("msg: %+v", msg)
	require.Equal(t, msg, capabilities.TriggerAndId[*evmservice.Log]{Trigger: nil, Id: ""})
}

func createLog(index uint32, number *big.Int, address evmtypes.Address, message []byte) *evmtypes.Log {
	return &evmtypes.Log{
		LogIndex:    index,
		BlockHash:   [32]byte{},
		BlockNumber: number,
		Topics:      []evmtypes.Hash{},
		EventSig:    [32]byte{},
		Address:     address,
		TxHash:      [32]byte{},
		Data:        message,
		Removed:     false,
	}
}

func TestGenerateLogIdentifier_DifferentLogsProduceDifferentIDs(t *testing.T) {
	service := &LogTriggerService{}
	createLog := func(txHash, blockHash string, logIndex uint32) *evmtypes.Log {
		return &evmtypes.Log{
			TxHash:    stringToHashBytes(txHash),
			BlockHash: stringToHashBytes(blockHash),
			LogIndex:  logIndex,
		}
	}
	t.Run("same log generates same identifier", func(t *testing.T) {
		baseLog := createLog("txhashA", "blockhashB", 0)
		id1 := service.generateLogIdentifier(baseLog)
		id2 := service.generateLogIdentifier(baseLog)
		require.Equal(t, id1, id2)
	})
	t.Run("logs differ only by TxHash", func(t *testing.T) {
		log1 := createLog("txhashA", "blockhashB", 0)
		log2 := createLog("txhashB", "blockhashB", 0)
		id1 := service.generateLogIdentifier(log1)
		id2 := service.generateLogIdentifier(log2)
		require.NotEqual(t, id1, id2)
	})
	t.Run("logs differ only by BlockHash", func(t *testing.T) {
		log1 := createLog("txhashA", "blockhashB", 0)
		log2 := createLog("txhashA", "blockhashC", 0)
		id1 := service.generateLogIdentifier(log1)
		id2 := service.generateLogIdentifier(log2)
		require.NotEqual(t, id1, id2)
	})
	t.Run("logs differ only by LogIndex", func(t *testing.T) {
		log1 := createLog("txhashA", "blockhashB", 0)
		log2 := createLog("txhashA", "blockhashB", 1)
		id1 := service.generateLogIdentifier(log1)
		id2 := service.generateLogIdentifier(log2)
		require.NotEqual(t, id1, id2)
	})
	t.Run("completely different logs", func(t *testing.T) {
		log1 := createLog("txhashA", "blockhashB", 0)
		log2 := createLog("txhashX", "blockhashZ", 99)
		id1 := service.generateLogIdentifier(log1)
		id2 := service.generateLogIdentifier(log2)
		require.NotEqual(t, id1, id2)
	})
}

func stringToHashBytes(s string) [evmtypes.HashLength]byte {
	var arr [evmtypes.HashLength]byte
	copy(arr[:], s)
	return arr
}

func stringToAddressBytes(s string) [evmtypes.AddressLength]byte {
	var arr [evmtypes.AddressLength]byte
	copy(arr[:], s)
	return arr
}

// Mocked structs

func assemblyDataMessage(address evmtypes.Address, blockNumber *big.Int) string {
	message := fmt.Sprintf("Message from address: %x, current block number: %s", address, blockNumber.String())
	return message
}

func createTriggerResponse(log *evmtypes.Log, service *LogTriggerService) capabilities.TriggerAndId[*evmservice.Log] {
	protoLog := evmservice.ConvertLogToProto(log)
	return capabilities.TriggerAndId[*evmservice.Log]{
		Id:      service.generateLogIdentifier(log),
		Trigger: protoLog,
	}
}

// Mocked ticker factory
type mockTickerFactory struct {
	C1 chan time.Time
}

func (m *mockTickerFactory) NewTicker(_ time.Duration) tickerWrapper {
	return &mockTicker{C1: m.C1}
}

type mockTicker struct {
	C1 chan time.Time
}

func (m *mockTicker) Channel() <-chan time.Time {
	return m.C1
}

func (m *mockTicker) Stop() {
	// do nothing, mocked ticker doesn't have to do any clean up
}
