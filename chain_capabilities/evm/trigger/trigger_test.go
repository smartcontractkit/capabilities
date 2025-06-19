package trigger

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
	eventSignatures = [][]byte{eventSig0Example}

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
			EventSigs: eventSignatures,
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
		//we simulate a RegisterLogTrigger() by tampering the store
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

	t.Run("missing eventSig", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
		})
		require.Error(t, err)
		require.Equal(t, err.Error(), "no valid event sig provided (at least one event sig is required)")
	})

	t.Run("fail to get latest head", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{}, evmtypes.Head{}, errors.New("mocked failure error"))
		service := NewLogTriggerService(evmService, NewLogTriggerStore(), lggr, pollInterval)
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			EventSigs: eventSignatures,
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
			EventSigs: eventSignatures,
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

func TestCreateLogRequest(t *testing.T) {
	service := &LogTriggerService{
		lggr: logger.Test(t),
	}
	tests := []struct {
		name                string
		input               *evmcappb.FilterLogTriggerRequest
		fromBlock           *big.Int
		expectedConfidence  primitives.ConfidenceLevel
		expectedExpressions []query.Expression
	}{
		{
			name: "finalized confidence, single address and single eventSig and empty topics",
			input: &evmcappb.FilterLogTriggerRequest{
				Addresses:  addresses,
				EventSigs:  eventSignatures,
				Confidence: evmcappb.ConfidenceLevel_FINALIZED,
			},
			fromBlock:          big.NewInt(10),
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				query.Block("10", primitives.Gt),
			},
		},
		//TODO PLEX-1488: missing test for SAFE confidence level
		{
			name: "latest confidence, single address and single eventSig and empty topics",
			input: &evmcappb.FilterLogTriggerRequest{
				Addresses:  addresses,
				EventSigs:  eventSignatures,
				Confidence: evmcappb.ConfidenceLevel_LATEST,
			},
			fromBlock:          big.NewInt(10),
			expectedConfidence: primitives.Unconfirmed,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				query.Block("10", primitives.Gt),
			},
		},
		{
			name: "finalized confidence, single address and single eventSig and a topic for 2, 3, 4",
			input: &evmcappb.FilterLogTriggerRequest{
				Addresses:  addresses,
				EventSigs:  eventSignatures,
				Topic2:     eventSignatures,
				Topic3:     eventSignatures,
				Topic4:     eventSignatures,
				Confidence: evmcappb.ConfidenceLevel_FINALIZED,
			},
			fromBlock:          big.NewInt(10),
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				*service.makeEventByTopicFilter(1, eventSignatures),
				*service.makeEventByTopicFilter(2, eventSignatures),
				*service.makeEventByTopicFilter(3, eventSignatures),
				query.Block("10", primitives.Gt),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expressions, limitAndSort, confidence, err := service.createLogRequest(context.Background(), tc.input, tc.fromBlock)
			require.NoError(t, err)
			require.NotNil(t, expressions)
			require.Len(t, expressions, len(tc.expectedExpressions))
			for i, expected := range tc.expectedExpressions {
				require.Equal(t, expected, expressions[i])
			}
			require.NotNil(t, limitAndSort)
			require.NotNil(t, limitAndSort.SortBy)
			require.Equal(t, query.NewSortByBlock(query.Asc), limitAndSort.SortBy[0])
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

func TestCalculateFromBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lggr := logger.Test(t)

	evmService := initMocks(t)
	evmService.On("LatestAndFinalizedHead", mock.Anything).Return(latestExpHead, finalizedExpHead, nil)
	service := &LogTriggerService{
		lggr:       lggr,
		EVMService: evmService,
	}

	//TODO PLEX-1488: missing test for SAFE here

	t.Run("Confidence value ConfidenceLevel_FINALIZED", func(t *testing.T) {
		input := &evmcappb.FilterLogTriggerRequest{
			Confidence: evmcappb.ConfidenceLevel_FINALIZED,
		}
		fromBlock, err := service.calculateFromBlock(ctx, triggerID, input)
		require.NoError(t, err)
		require.Equal(t, finalizedExpHead.Number, fromBlock)
	})

	t.Run("Confidence value ConfidenceLevel_UNFINALIZED", func(t *testing.T) {
		input := &evmcappb.FilterLogTriggerRequest{
			Confidence: evmcappb.ConfidenceLevel_LATEST,
		}
		fromBlock, err := service.calculateFromBlock(ctx, triggerID, input)
		require.NoError(t, err)
		require.Equal(t, latestExpHead.Number, fromBlock)
	})
}

func TestGetLatestBlockNumber(t *testing.T) {
	t.Run("single log extracts value correctly", func(t *testing.T) {
		service := &LogTriggerService{}
		logs := []*evmservice.Log{
			{
				BlockNumber: &valuespb.BigInt{AbsVal: big.NewInt(5).Bytes()},
			},
		}
		currentBlock := big.NewInt(0)
		latestBlock := service.getLatestBlockNumber(logs, currentBlock)
		require.Equal(t, big.NewInt(5), latestBlock)
	})

	t.Run("multiple logs with different block numbers mixed up", func(t *testing.T) {
		service := &LogTriggerService{}
		addr1 := []byte{0xde, 0xad}
		addr2 := []byte{0xad, 0xde}
		logs := []*evmservice.Log{
			{
				Address:     addr1,
				BlockNumber: &valuespb.BigInt{AbsVal: big.NewInt(2).Bytes()},
			},
			{
				Address:     addr1,
				BlockNumber: &valuespb.BigInt{AbsVal: big.NewInt(3).Bytes()},
			},
			{
				Address:     addr2,
				BlockNumber: &valuespb.BigInt{AbsVal: big.NewInt(2).Bytes()},
			},
		}
		currentBlock := big.NewInt(0)
		latestBlock := service.getLatestBlockNumber(logs, currentBlock)
		require.Equal(t, big.NewInt(3), latestBlock)
	})
}

func TestSendLogsToWorkflows(t *testing.T) {
	lggr := logger.Test(t)
	service := &LogTriggerService{
		lggr: lggr,
	}
	expectedLog1 := &evmservice.Log{
		TxHash:    []byte("txhash1"),
		BlockHash: []byte("blockhash1"),
		Index:     1,
	}
	expectedLog2 := &evmservice.Log{
		TxHash:    []byte("txhash2"),
		BlockHash: []byte("blockhash2"),
		Index:     2,
	}
	expectedLogs := []*evmservice.Log{expectedLog1, expectedLog2}

	t.Run("all logs are sent to the channel", func(t *testing.T) {
		logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], len(expectedLogs))

		service.sendLogsToWorkflows(expectedLogs, triggerID, logCh)
		require.Len(t, logCh, len(expectedLogs))
		actualLog1 := <-logCh
		require.Equal(t, service.createTriggerResponse(expectedLog1), actualLog1)
		actualLog2 := <-logCh
		require.Equal(t, service.createTriggerResponse(expectedLog2), actualLog2)
		select {
		case msg := <-logCh:
			t.Fatalf("unexpected message received: %+v", msg)
		default:
			// no message received, as expected
		}
	})

	t.Run("first log sent to channel second log dropped out due to timeout", func(t *testing.T) {
		logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], 1) // buffer size of 1, so it can only hold one log at a time

		service.sendLogsToWorkflows(expectedLogs, triggerID, logCh)
		require.Len(t, logCh, 1)

		actualLog1 := <-logCh
		require.Equal(t, service.createTriggerResponse(expectedLog1), actualLog1)
		select {
		case msg := <-logCh:
			t.Fatalf("unexpected message received: %+v", msg)
		default:
			// no message received, as expected
		}
	})
}

func TestCreateTriggerResponse(t *testing.T) {
	service := &LogTriggerService{}
	log := &evmservice.Log{
		TxHash:    []byte("txhash"),
		BlockHash: []byte("blockhash"),
		Index:     1,
	}
	expectedID := service.generateLogIdentifier(log)
	actual := service.createTriggerResponse(log)
	require.Equal(t, expectedID, actual.Id)
	require.Equal(t, log, actual.Trigger)
}

func TestIntegration_RegisterAndUnregisterLogTrigger(t *testing.T) {
	lggr := logger.Test(t)
	evmService := initMocks(t)
	evmService.On("LatestAndFinalizedHead", mock.Anything).Return(latestExpHead, finalizedExpHead, nil)
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	// Mocking the QueryTrackedLogs method to return logs for the test (1st call) and then a second log for the next block (2nd call)
	nextBlockNumber := new(big.Int).Add(latestExpHead.Number, big.NewInt(1))
	message := []byte(assemblyDataMessage(evmtypes.Address(expectedAddress), nextBlockNumber))
	evmService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*evmtypes.Log{
		createLog(nextBlockNumber, evmtypes.Address(expectedAddress), message),
	}, nil).Once()
	nextBlockNumber = new(big.Int).Add(nextBlockNumber, big.NewInt(1))
	message = []byte(assemblyDataMessage(evmtypes.Address(expectedAddress), nextBlockNumber))
	evmService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*evmtypes.Log{
		createLog(nextBlockNumber, evmtypes.Address(expectedAddress), message),
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
		EventSigs: eventSignatures,
	})
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond) // let it run a bit more
	_, exists := service.triggers.Read(triggerID)
	require.True(t, exists, "expected trigger to be registered")
	require.Len(t, service.triggers.ReadAll(), 1, "expected one and only one trigger to be registered")

	validateLog := func(msg *capabilities.TriggerAndId[*evmservice.Log], expectedBlock *big.Int) {
		require.Equal(t, service.generateLogIdentifier(msg.Trigger), msg.Id)
		log0 := msg.Trigger
		require.Equal(t, expectedAddress, log0.Address)
		expectedMessage := assemblyDataMessage(evmtypes.Address(expectedAddress), expectedBlock)
		require.Equal(t, expectedMessage, string(log0.GetData()), "expected log data to match the expected message: %s", expectedMessage)
	}

	tickCh <- time.Now()
	time.Sleep(20 * time.Millisecond) // let it run a bit more

	select {
	case msg := <-ch:
		validateLog(&msg, big.NewInt(int64(31))) // 31 = 30 (latest block) + 1 of the next block mocked service method QueryTrackedLogs
	default:
		t.Fatal("expected at least one log after registration")
	}

	tickCh <- time.Now()
	time.Sleep(5 * time.Millisecond) // let it run a bit more

	select {
	case msg := <-ch:
		validateLog(&msg, big.NewInt(int64(32))) // 32 = 31 (latest block) + 1 of the next block mocked service method QueryTrackedLogs
	default:
		t.Fatal("expected a second log")
	}

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

func createLog(number *big.Int, address evmtypes.Address, message []byte) *evmtypes.Log {
	return &evmtypes.Log{
		LogIndex:    0,
		BlockHash:   evmtypes.Hash{22, 33, 44},
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
	log1 := &evmservice.Log{
		TxHash:    []byte("txhashA"),
		BlockHash: []byte("blockhashB"),
		Index:     0,
	}
	log2 := &evmservice.Log{
		TxHash:    []byte("txhashA'"),
		BlockHash: []byte("blockhashB'"),
		Index:     1,
	}
	id1 := service.generateLogIdentifier(log1)
	require.NotNil(t, id1)
	id2 := service.generateLogIdentifier(log2)
	require.NotNil(t, id2)
	require.NotEqual(t, id1, id2)
}

// Mocked structs

func assemblyDataMessage(address evmtypes.Address, blockNumber *big.Int) string {
	message := fmt.Sprintf("Message from address: %x, current block number: %s", address, blockNumber.String())
	return message
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
	//do nothing, mocked ticker doesn't have to do any clean up
}
