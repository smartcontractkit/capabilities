package trigger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"

	_ "github.com/smartcontractkit/chainlink-common/pkg/beholder"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/evm"
)

// testLogTriggerCapabilityID matches the shape of chain_capabilities/evm main.go (evm:ChainSelector:<selector>@1.0.0 (<chainID>).
const testLogTriggerCapabilityID = "evm:ChainSelector:5009297550715157269@1.0.0 (1)"

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

	triggerID         = "trigger-1"
	finalizedExpBlock = evmtypes.LPBlock{FinalizedBlockNumber: 25}
	pollInterval      = 10 * time.Millisecond
)

// Build a LogTriggerService with BaseTriggerCapability wired to an inbox channel.
func newLTSWithBase(t *testing.T) (*LogTriggerService, chan capabilities.TriggerAndId[*evmcappb.Log]) {
	lts := newLogTriggerService(t)
	es := capabilities.NewMemEventStore()

	lts.baseTrigger = capabilities.NewBaseTriggerCapability(es, func() *evmcappb.Log { return &evmcappb.Log{} },
		lts.lggr, "testCap", 500*time.Millisecond, 0, 0)

	require.NoError(t, lts.baseTrigger.Start(t.Context()))
	t.Cleanup(func() {
		lts.baseTrigger.Stop()
	})

	sendCh := make(chan capabilities.TriggerAndId[*evmcappb.Log], 1)
	lts.baseTrigger.RegisterTrigger(triggerID, sendCh)
	return lts, sendCh
}

func initMocks(t *testing.T) *evmmock.EVMService {
	t.Helper()
	evmSvc := evmmock.NewEVMService(t)
	return evmSvc
}

func TestLogTriggerService_Close_WaitsForPollingGoroutine(t *testing.T) {
	t.Run("close awaits on syncGroup to finalize", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		evmService := initMocks(t)
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil)
		evmService.On("QueryTrackedLogs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*evmtypes.Log{}, nil).Maybe()
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
		evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
		evmService.EXPECT().GetFiltersNames(mock.Anything).Return([]string{}, nil).Maybe()
		store := NewLogTriggerStore()
		service := createTriggerObject(t, evmService, store)

		service.baseTrigger = capabilities.NewBaseTriggerCapability(capabilities.NewMemEventStore(),
			func() *evmcappb.Log { return &evmcappb.Log{} }, logger.Test(t), "testCap", 200*time.Millisecond, 0, 0)
		require.NoError(t, service.baseTrigger.Start(ctx))
		defer service.baseTrigger.Stop()

		err := service.Start(ctx)
		require.NoError(t, err)
		ch, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
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
	service := createTriggerObject(t, nil, NewLogTriggerStore())

	t.Run("[2]Unknown: no triggerID provided", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(t.Context(), "", capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
		})
		expectedError := "[13]Internal: no triggerID provided"
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)
	})

	t.Run("[2]Unknown: triggerID \\\"trigger-1\\\" is already registered", func(t *testing.T) {
		store := NewLogTriggerStore()
		service := createTriggerObject(t, nil, store)
		// we simulate a RegisterLogTrigger() by tampering the store
		store.Write(triggerID, logTriggerState{})
		_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
		})
		expectedError := "[13]Internal: triggerID \"trigger-1\" is already registered"
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)
	})

	t.Run("missing addresses", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: [][]byte{},
		})
		expectedError := "[2]Unknown: no valid addresses provided (at least one address is required)"
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)
	})

	t.Run("too many topics", func(t *testing.T) {
		_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			Topics: []*evmcappb.TopicValues{
				{Values: [][]byte{}},
				{Values: [][]byte{}},
				{Values: [][]byte{}},
				{Values: [][]byte{}},
				{Values: [][]byte{}}, // 5th topic, should fail
			},
		})
		expectedError := "[2]Unknown: there can be at most 4 topics provided, got 5 instead"
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)
	})

	t.Run("missing eventSig", func(t *testing.T) {
		expectedError := "[2]Unknown: no valid event sig provided (at least one event sig is required in topics)"
		_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
		})
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)

		_, err = service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			Topics:    []*evmcappb.TopicValues{},
		})
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)
	})

	t.Run("fail to get latest head", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(nil, errors.New("mocked failure error"))
		service := createTriggerObject(t, evmService, NewLogTriggerStore())
		ctx := t.Context()
		_, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: addresses,
			Topics:    topicsWithEventSig0,
		})
		expectedError := "[14]Unavailable: failed to register latest and finalized log pollers block: 'mocked failure error' for triggerID: trigger-1"
		assertCapError(t, err, caperrors.VisibilityPrivate, expectedError)
	})

	t.Run("fail to register log-tracking internal error", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil)
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(errors.New("mocking error, making register failing on purpose")).Once()
		service := createTriggerObject(t, evmService, NewLogTriggerStore())
		ctx := t.Context()
		_, err := service.RegisterLogTrigger(ctx, triggerID+"-logtracking", capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: brokenAddresses,
			Topics:    topicsWithEventSig0,
		})
		expectedError := "[2]Unknown: failed to register log-tracking: 'mocking error, making register failing on purpose' for triggerID: trigger-1-logtracking, addresses: [[173 173 190 239 202 254 186 190 18 52 86 120 154 188 222 240 17 34 51 68]], eventSig: [[221 242 82 173 27 226 200 155 105 194 176 104 252 55 141 170 149 43 167 241 99 196 161 22 40 245 90 77 245 35 179 239]], topic2: [], topic3: [], topic4: []"
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)
	})

	t.Run("fail to register log-tracking user error", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil)
		canceled := caperrors.Canceled // error code here will likely be InvalidArgument, putting a different value to ensure it's properly propagated
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(caperrors.NewPublicUserError(fmt.Errorf("mocking error, address is not an EOA"), canceled)).Once()
		service := createTriggerObject(t, evmService, NewLogTriggerStore())
		ctx := t.Context()
		_, err := service.RegisterLogTrigger(ctx, triggerID+"-logtracking", capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
			Addresses: brokenAddresses,
			Topics:    topicsWithEventSig0,
		})
		expectedError := "[1]Canceled: failed to register log-tracking: '[1]Canceled: mocking error, address is not an EOA' for triggerID: trigger-1-logtracking, addresses: [[173 173 190 239 202 254 186 190 18 52 86 120 154 188 222 240 17 34 51 68]], eventSig: [[221 242 82 173 27 226 200 155 105 194 176 104 252 55 141 170 149 43 167 241 99 196 161 22 40 245 90 77 245 35 179 239]], topic2: [], topic3: [], topic4: []"
		assertCapError(t, err, caperrors.VisibilityPublic, expectedError)
	})
}

func assertCapError(t *testing.T, err caperrors.Error, visibility caperrors.Visibility, expectedError string) {
	require.Error(t, err, "expected error but got nil")
	var capError caperrors.Error
	require.True(t, errors.As(err, &capError), "expected error to be of type caperrors.Error")
	require.Equal(t, expectedError, err.Error())
	require.Equal(t, visibility, capError.Visibility())
}

func TestUnregisterLogTrigger_InputValidation(t *testing.T) {
	t.Parallel()

	service := &LogTriggerService{}

	emptyMetadata := capabilities.RequestMetadata{}
	emptyRequest := &evmcappb.FilterLogTriggerRequest{}

	t.Run("missing triggerID", func(t *testing.T) {
		err := service.UnregisterLogTrigger(t.Context(), "", emptyMetadata, emptyRequest)
		assertCapError(t, err, caperrors.VisibilityPublic, "[13]Internal: no triggerID provided")
	})

	t.Run("no active trigger found", func(t *testing.T) {
		service := &LogTriggerService{
			triggers: NewLogTriggerStore(),
		}
		err := service.UnregisterLogTrigger(t.Context(), triggerID, emptyMetadata, emptyRequest)
		assertCapError(t, err, caperrors.VisibilityPublic, "[13]Internal: no active trigger found for triggerID: trigger-1")
	})

	t.Run("fail to unregister log-tracking", func(t *testing.T) {
		breakingTriggerID := "breaking-logTriggerUnregister"
		evmService := initMocks(t)
		evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(errors.New("mocking error, making unregister failing on purpose")).Once()
		service := createTriggerObject(t, evmService, NewLogTriggerStore())

		service.triggers.Write(breakingTriggerID, logTriggerState{
			cancelFunc: func() {},
			lastBlock:  big.NewInt(0),
		})
		err := service.UnregisterLogTrigger(t.Context(), breakingTriggerID, emptyMetadata, emptyRequest)
		assertCapError(t, err, caperrors.VisibilityPrivate, "[2]Unknown: failed to unregister log-tracking: 'mocking error, making unregister failing on purpose' for triggerID: breaking-logTriggerUnregister")
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

	t.Run("eventSigs, topic2 and topic4 provided (no topic3)", func(t *testing.T) {
		input := &evmcappb.FilterLogTriggerRequest{
			Topics: []*evmcappb.TopicValues{
				{Values: [][]byte{[]byte("eventSig1")}},
				{Values: [][]byte{[]byte("topic2")}},
				{},
				{Values: [][]byte{[]byte("topic4")}},
			},
		}
		eventSigs, topics2, topics3, topics4 := service.getTopics(input)
		require.Equal(t, [][]byte{[]byte("eventSig1")}, eventSigs)
		require.Equal(t, [][]byte{[]byte("topic2")}, topics2)
		require.Nil(t, topics3)
		require.Equal(t, [][]byte{[]byte("topic4")}, topics4)
	})
}

func TestCreateLogRequest(t *testing.T) {
	service := createTriggerObject(t, nil, NewLogTriggerStore())

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
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_FINALIZED,
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
			},
		},
		{
			name:               "safe confidence, single address and single eventSig and empty topics",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_SAFE,
			expectedConfidence: primitives.Safe,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
			},
		},
		{
			name:               "safe confidence, single address and 2 eventSig and empty topics",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example, eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_SAFE,
			expectedConfidence: primitives.Safe,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				query.Or(
					evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
					evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				),
			},
		},
		{
			name:               "safe confidence, 2 address and single eventSig and empty topics",
			addresses:          [][]byte{expectedAddress, expectedAddress},
			eventSigs:          [][]byte{eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_SAFE,
			expectedConfidence: primitives.Safe,
			expectedExpressions: []query.Expression{
				query.Or(
					evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
					evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
			},
		},
		{
			name:               "latest confidence, single address and single eventSig and empty topics",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_LATEST,
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
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_FINALIZED,
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				*service.makeEventByTopicFilter(1, []evmtypes.Hash{evmtypes.Hash(eventSig0Example)}),
				*service.makeEventByTopicFilter(2, []evmtypes.Hash{evmtypes.Hash(eventSig0Example)}),
				*service.makeEventByTopicFilter(3, []evmtypes.Hash{evmtypes.Hash(eventSig0Example)}),
			},
		},
		{
			name:               "finalized confidence, single address and single eventSig and a topic for 1, 3 (omitting 2)",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example},
			topics2:            [][]byte{eventSig0Example},
			topics4:            [][]byte{eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_FINALIZED,
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				*service.makeEventByTopicFilter(1, []evmtypes.Hash{evmtypes.Hash(eventSig0Example)}),
				*service.makeEventByTopicFilter(3, []evmtypes.Hash{evmtypes.Hash(eventSig0Example)}),
			},
		},
		{
			name:               "finalized confidence, single address and single eventSig and a topic for 2 (multiple topics)",
			addresses:          addresses,
			eventSigs:          [][]byte{eventSig0Example},
			topics2:            [][]byte{eventSig0Example, eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_FINALIZED,
			expectedConfidence: primitives.Finalized,
			expectedExpressions: []query.Expression{
				evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				*service.makeEventByTopicFilter(1, []evmtypes.Hash{evmtypes.Hash(eventSig0Example), evmtypes.Hash(eventSig0Example)}),
			},
		},
		{
			name:               "safe confidence, multiple address, eventSig and topics 1, 2, 3",
			addresses:          [][]byte{expectedAddress, expectedAddress},
			eventSigs:          [][]byte{eventSig0Example, eventSig0Example},
			topics2:            [][]byte{eventSig0Example, eventSig0Example},
			topics3:            [][]byte{eventSig0Example, eventSig0Example},
			topics4:            [][]byte{eventSig0Example, eventSig0Example},
			confidence:         evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_SAFE,
			expectedConfidence: primitives.Safe,
			expectedExpressions: []query.Expression{
				query.Or(
					evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
					evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				),
				query.Or(
					evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
					evm.NewEventSigFilter(evmtypes.Hash(eventSig0Example)),
				),
				*service.makeEventByTopicFilter(1, []evmtypes.Hash{evmtypes.Hash(eventSig0Example), evmtypes.Hash(eventSig0Example)}),
				*service.makeEventByTopicFilter(2, []evmtypes.Hash{evmtypes.Hash(eventSig0Example), evmtypes.Hash(eventSig0Example)}),
				*service.makeEventByTopicFilter(3, []evmtypes.Hash{evmtypes.Hash(eventSig0Example), evmtypes.Hash(eventSig0Example)}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addrs, err := evmservice.ConvertAddressesFromProto(tc.addresses)
			require.NoError(t, err)

			eventSigs, err := evmservice.ConvertHashesFromProto(tc.eventSigs)
			require.NoError(t, err)

			topics2, err := evmservice.ConvertHashesFromProto(tc.topics2)
			require.NoError(t, err)

			topics3, err := evmservice.ConvertHashesFromProto(tc.topics3)
			require.NoError(t, err)

			topics4, err := evmservice.ConvertHashesFromProto(tc.topics4)
			require.NoError(t, err)

			expressions, confidence := service.createLogRequest(t.Context(), addrs,
				eventSigs,
				topics2,
				topics3,
				topics4,
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
		topics          []evmtypes.Hash
		expected        query.Expression
		isNilExpression bool
	}
	tests := []testCase{
		{
			name:            "zero topics",
			topics:          []evmtypes.Hash{},
			isNilExpression: true,
		},
		{
			name:   "one topic",
			topics: []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
			expected: evm.NewEventByTopicFilter(10, []evm.HashedValueComparator{{
				Values:   []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
				Operator: primitives.Eq,
			}}),
		},
		{
			name:   "two topics",
			topics: []evmtypes.Hash{evmtypes.Hash(eventSig0Example), evmtypes.Hash(eventSig0Example)},
			expected: query.Or(
				evm.NewEventByTopicFilter(10, []evm.HashedValueComparator{{
					Values:   []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
					Operator: primitives.Eq,
				}}),
				evm.NewEventByTopicFilter(10, []evm.HashedValueComparator{{
					Values:   []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
					Operator: primitives.Eq,
				}})),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr := service.makeEventByTopicFilter(10, tc.topics)
			if tc.isNilExpression {
				require.Nil(t, expr)
				return
			}
			require.NotNil(t, expr)
			require.Equal(t, tc.expected, *expr)
		})
	}
}

func TestGetFinalizedBlockNumber(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	t.Run("gets latest block number", func(t *testing.T) {
		evmService := initMocks(t)
		service := &LogTriggerService{
			lggr:       logger.Test(t),
			EVMService: evmService,
		}
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil)
		fromBlock, err := service.getFinalizedBlockNumber(ctx, triggerID)
		require.NoError(t, err)
		require.Equal(t, big.NewInt(finalizedExpBlock.FinalizedBlockNumber), fromBlock)
	})
	t.Run("fails getting latest block number", func(t *testing.T) {
		evmService := initMocks(t)
		service := &LogTriggerService{
			lggr:       logger.Test(t),
			EVMService: evmService,
		}
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(nil, errors.New("mocked failure error for LatestAndFinalizedHead"))
		_, err := service.getFinalizedBlockNumber(ctx, triggerID)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to register latest and finalized log pollers block: 'mocked failure error for LatestAndFinalizedHead' for triggerID: trigger-1")
	})
}

func TestGetLatestBlockNumber(t *testing.T) {
	t.Run("single log extracts value correctly", func(t *testing.T) {
		service := &LogTriggerService{lggr: logger.Test(t)}
		logs := []*evmtypes.Log{
			{
				BlockNumber: big.NewInt(5),
			},
		}
		currentBlock := big.NewInt(0)
		latestBlock := service.getLatestBlockNumber(logs, currentBlock, big.NewInt(10), "triggerID")
		require.Equal(t, big.NewInt(5), latestBlock)
	})

	t.Run("multiple logs with different block numbers mixed up", func(t *testing.T) {
		service := &LogTriggerService{lggr: logger.Test(t)}
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
		latestBlock := service.getLatestBlockNumber(logs, currentBlock, big.NewInt(10), "triggerID")
		require.Equal(t, big.NewInt(3), latestBlock)
	})

	t.Run("multiple logs with unfinalized blocks return highest one", func(t *testing.T) {
		service := &LogTriggerService{lggr: logger.Test(t)}
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
		latestBlock := service.getLatestBlockNumber(logs, currentBlock, big.NewInt(2), "triggerID")
		require.Equal(t, big.NewInt(2), latestBlock)
	})
}

func TestFetchLogsFromLogPoller(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	evmService := evmmock.NewEVMService(t)
	service := createTriggerObject(t, evmService, NewLogTriggerStore())
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
	service, sendCh := newLTSWithBase(t)

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
			lastBlock:               finalizedBlockNumber,
			filter: filter{
				expressions: []query.Expression{
					evm.NewAddressFilter(evmtypes.Address(expectedAddress)),
				},
				confidence: primitives.Finalized,
			},
		})
		state, _ := service.triggers.Read(triggerID)
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		err := service.sendLogsToWorkflows(ctx, monitoring.TelemetryContext{}, expectedLogs, finalizedBlockNumber, triggerID, state)
		require.NoError(t, err)
		actualLog1 := <-sendCh
		require.NoError(t,
			service.baseTrigger.AckEvent(t.Context(), triggerID, actualLog1.Id),
		)
		expectedResponse1 := createTriggerResponse(expectedLog1, service)
		require.Equal(t, expectedResponse1.Id, actualLog1.Id)
		require.True(t, proto.Equal(expectedResponse1.Trigger, actualLog1.Trigger), "proto logs differ for 1st log")

		actualLog2 := <-sendCh
		require.NoError(t,
			service.baseTrigger.AckEvent(t.Context(), triggerID, actualLog2.Id),
		)
		expectedResponse2 := createTriggerResponse(expectedLog2, service)
		require.Equal(t, expectedResponse2.Id, actualLog2.Id)
		require.True(t, proto.Equal(expectedResponse2.Trigger, actualLog2.Trigger), "proto logs differ for 2nd log")
		require.Len(t, sendCh, 0)
		// Verify that the unfinalized logs are stored in the trigger state and all other fields are preserved
		state2, _ := service.triggers.Read(triggerID)
		require.Len(t, state2.unfinalizedSentEventIDs, 1)
		require.Equal(t, state.lastBlock, state2.lastBlock)
		require.Equal(t, state.expressions, state2.expressions)
		require.Equal(t, state.confidence, state2.confidence)
	})

	t.Run("first delivered immediately; second delivered after retry when inbox initially full", func(t *testing.T) {
		service.triggers.Write(triggerID, logTriggerState{
			unfinalizedSentEventIDs: map[string]*big.Int{},
		})
		state, _ := service.triggers.Read(triggerID)

		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		// Send 2 logs to workflow, with space for only a single log in the sendCh
		err := service.sendLogsToWorkflows(ctx, monitoring.TelemetryContext{}, expectedLogs, big.NewInt(0), triggerID, state)
		require.NoError(t, err)
		actualLog1 := <-sendCh
		require.NoError(t, service.baseTrigger.AckEvent(t.Context(), triggerID, actualLog1.Id))
		expectedResponse1 := createTriggerResponse(expectedLog1, service)
		require.Equal(t, expectedResponse1.Id, actualLog1.Id)
		require.True(t, proto.Equal(expectedResponse1.Trigger, actualLog1.Trigger), "proto logs differ for 1st log")

		require.Eventually(t, func() bool {
			select {
			case actualLog2 := <-sendCh:
				return actualLog2.Id == createTriggerResponse(expectedLog2, service).Id
			default:
				return false
			}
		}, 30*time.Second, 10*time.Millisecond)

		state, _ = service.triggers.Read(triggerID)
		require.Len(t, state.unfinalizedSentEventIDs, 2, "expected two unfinalized sent event ID to be stored")
		logID1 := service.generateLogIdentifier(expectedLog1)
		require.Equal(t, expectedLog1.BlockNumber, state.unfinalizedSentEventIDs[logID1])
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
		logCh := make(chan capabilities.TriggerAndId[*evmcappb.Log], len(expectedLogs))
		err := service.sendLogsToWorkflows(t.Context(), monitoring.TelemetryContext{}, []*evmtypes.Log{}, finalizedBlockNumber, triggerID, triggerState)
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
		service, _ := newLTSWithBase(t)
		state := logTriggerState{
			unfinalizedSentEventIDs: map[string]*big.Int{},
		}
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
		err := service.sendLogsToWorkflows(ctx, monitoring.TelemetryContext{}, expectedLogs, finalizedBlockNumber, triggerID, state)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to update unfinalized sent event IDs for triggerID: trigger-1: cannot find trigger with ID \"trigger-1\"")
	})
}

func TestIntegration_RegisterAndUnregisterLogTrigger(t *testing.T) {
	t.Run("register and unregister log trigger integration", func(t *testing.T) {
		topicsInput := []*evmcappb.TopicValues{
			{Values: [][]byte{eventSig0Example}},
		}
		expectedFilter := evmtypes.LPFilterQuery{
			Name:      "trigger-integration-evm-log-trigger",
			Addresses: []evmtypes.Address{evmtypes.Address(expectedAddress)},
			EventSigs: []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
			Topic2:    []evmtypes.Hash{},
			Topic3:    []evmtypes.Hash{},
			Topic4:    []evmtypes.Hash{},
		}
		registerAndUnregisterLogTriggerIntegration(t, topicsInput, expectedFilter)
	})
	t.Run("register and unregister log trigger integration with empty topics 2-4", func(t *testing.T) {
		topicsInput := []*evmcappb.TopicValues{
			{Values: [][]byte{eventSig0Example}},
			{},
			{},
			{},
		}
		expectedFilter := evmtypes.LPFilterQuery{
			Name:      "trigger-integration-evm-log-trigger",
			Addresses: []evmtypes.Address{evmtypes.Address(expectedAddress)},
			EventSigs: []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
			Topic2:    []evmtypes.Hash{},
			Topic3:    []evmtypes.Hash{},
			Topic4:    []evmtypes.Hash{},
		}
		registerAndUnregisterLogTriggerIntegration(t, topicsInput, expectedFilter)
	})
	t.Run("register and unregister log trigger integration with empty topics 2,3", func(t *testing.T) {
		topicsInput := []*evmcappb.TopicValues{
			{Values: [][]byte{eventSig0Example}},
			{},
			{},
			{Values: [][]byte{eventSig0Example}},
		}
		expectedFilter := evmtypes.LPFilterQuery{
			Name:      "trigger-integration-evm-log-trigger",
			Addresses: []evmtypes.Address{evmtypes.Address(expectedAddress)},
			EventSigs: []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
			Topic2:    []evmtypes.Hash{},
			Topic3:    []evmtypes.Hash{},
			Topic4:    []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
		}
		registerAndUnregisterLogTriggerIntegration(t, topicsInput, expectedFilter)
	})
	t.Run("register and unregister log trigger integration with single topic 2, empty topic 3, and multiple topic 4", func(t *testing.T) {
		topicsInput := []*evmcappb.TopicValues{
			{Values: [][]byte{eventSig0Example}},
			{Values: [][]byte{eventSig0Example}},
			{},
			{Values: [][]byte{eventSig0Example, eventSig0Example}},
		}
		expectedFilter := evmtypes.LPFilterQuery{
			Name:      "trigger-integration-evm-log-trigger",
			Addresses: []evmtypes.Address{evmtypes.Address(expectedAddress)},
			EventSigs: []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
			Topic2:    []evmtypes.Hash{evmtypes.Hash(eventSig0Example)},
			Topic3:    []evmtypes.Hash{},
			Topic4:    []evmtypes.Hash{evmtypes.Hash(eventSig0Example), evmtypes.Hash(eventSig0Example)},
		}
		registerAndUnregisterLogTriggerIntegration(t, topicsInput, expectedFilter)
	})
}

func registerAndUnregisterLogTriggerIntegration(t *testing.T, topicsInput []*evmcappb.TopicValues, expectedFilter evmtypes.LPFilterQuery) {
	evmService := initMocks(t)
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			filter := args.Get(1)
			require.NotNil(t, filter, "expected filter to be not nil")
			filterTyped, ok := filter.(evmtypes.LPFilterQuery)
			require.True(t, ok, "expected filter to be of type evmtypes.LPFilterQuery")
			require.Equal(t, expectedFilter, filterTyped, "expected filter to match the provided topics input")
		}).Return(nil).Once()
	evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()

	// two calls, one for the starting offset and a second one for the next block
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&evmtypes.LPBlock{FinalizedBlockNumber: 25}, nil).Twice()
	// single call, for fetching the latest finalized head and check if the offset has to be adjusted
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&evmtypes.LPBlock{FinalizedBlockNumber: 26}, nil).Once()
	// Mocking the QueryTrackedLogs method to return logs for the test (1st call) and then a second log for the next block (2nd call)
	nextBlockNumber := new(big.Int).Add(big.NewInt(finalizedExpBlock.FinalizedBlockNumber), big.NewInt(1))
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

	service := createTriggerObject(t, evmService, NewLogTriggerStore())

	service.baseTrigger = capabilities.NewBaseTriggerCapability(capabilities.NewMemEventStore(),
		func() *evmcappb.Log { return &evmcappb.Log{} }, logger.Test(t), "testCap", 200*time.Millisecond, 0, 0)

	triggerID := "trigger-integration"

	tickCh := make(chan time.Time)
	defaultTickerFactory = &mockTickerFactory{C1: tickCh}
	require.Empty(t, service.triggers.ReadAll())
	ctx := t.Context()
	ch, err := service.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &evmcappb.FilterLogTriggerRequest{
		Addresses: addresses,
		Topics:    topicsInput,
	})
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond) // let it run a bit more
	triggerState, exists := service.triggers.Read(triggerID)
	require.True(t, exists, "expected trigger to be registered")
	require.Len(t, service.triggers.ReadAll(), 1, "expected one and only one trigger to be registered")
	require.Len(t, triggerState.unfinalizedSentEventIDs, 0, "expected no unfinalized sent event IDs stored in trigger state before registration")

	validateLog := func(msg *capabilities.TriggerAndId[*evmcappb.Log], expectedBlock *big.Int) {
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
	//lggr.Debugf("msg: %+v", msg)
	require.Equal(t, msg, capabilities.TriggerAndId[*evmcappb.Log]{Trigger: nil, Id: ""})
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

func TestRegisterLogTrigger_ConversionFailures_Compact(t *testing.T) {
	t.Parallel()
	evmService := initMocks(t)

	evmService.EXPECT().
		GetLatestLPBlock(mock.Anything).
		Return(&finalizedExpBlock, nil).
		Maybe()

	svc := createTriggerObject(t, evmService, NewLogTriggerStore())

	makeBytes := func(n int) []byte { return bytes.Repeat([]byte{0xAB}, n) }
	validAddr := makeBytes(evmtypes.AddressLength)
	validSig := makeBytes(evmtypes.HashLength)
	invalidAddr := makeBytes(evmtypes.AddressLength - 1)
	invalidHash := makeBytes(evmtypes.HashLength - 1)

	cases := []struct {
		name string
		req  *evmcappb.FilterLogTriggerRequest
		want string
	}{
		{
			name: "addresses",
			req: &evmcappb.FilterLogTriggerRequest{
				Addresses: [][]byte{invalidAddr},
				Topics:    []*evmcappb.TopicValues{{Values: [][]byte{validSig}}},
			},
			want: "failed to convert addresses:",
		},
		{
			name: "eventSigs",
			req: &evmcappb.FilterLogTriggerRequest{
				Addresses: [][]byte{validAddr},
				Topics:    []*evmcappb.TopicValues{{Values: [][]byte{invalidHash}}},
			},
			want: "failed to convert eventSigs:",
		},
		{
			name: "topics2",
			req: &evmcappb.FilterLogTriggerRequest{
				Addresses: [][]byte{validAddr},
				Topics: []*evmcappb.TopicValues{
					{Values: [][]byte{validSig}},
					{Values: [][]byte{invalidHash}},
				},
			},
			want: "failed to convert topics2:",
		},
		{
			name: "topics3",
			req: &evmcappb.FilterLogTriggerRequest{
				Addresses: [][]byte{validAddr},
				Topics: []*evmcappb.TopicValues{
					{Values: [][]byte{validSig}},
					{Values: [][]byte{validSig}},
					{Values: [][]byte{invalidHash}},
				},
			},
			want: "failed to convert topics3:",
		},
		{
			name: "topics4",
			req: &evmcappb.FilterLogTriggerRequest{
				Addresses: [][]byte{validAddr},
				Topics: []*evmcappb.TopicValues{
					{Values: [][]byte{validSig}},
					{Values: [][]byte{validSig}},
					{Values: [][]byte{validSig}},
					{Values: [][]byte{invalidHash}},
				},
			},
			want: "failed to convert topics4:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			_, err := svc.RegisterLogTrigger(
				ctx,
				"conv-fail-"+tc.name,
				capabilities.RequestMetadata{WorkflowID: "wf-id"},
				tc.req,
			)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
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
	t.Run("log generates valid string identifier", func(t *testing.T) {
		rx := `^[0-9a-f]+:[0-9a-f]+:\d+$`
		log1 := &evmtypes.Log{
			TxHash:    stringToHashBytes("91056b6ac7e64dd15ffaa011ec8026596cd9d05ba96fc138948620ca58a44167"),
			BlockHash: stringToHashBytes("7e96f4eecec1e72b761298d57a66e660f2a8df2e29dd1cc2bf2ded9fcce8fc14"),
			LogIndex:  42,
		}
		log2 := createLog("txhashX", "blockhashZ", 99)

		id1 := service.generateLogIdentifier(log1)
		id2 := service.generateLogIdentifier(log2)
		require.Regexp(t, rx, id1, "expected log identifier to be a valid string in the format 'hex(txhash):hex(blockhash):logindex'")
		require.Regexp(t, rx, id2, "expected log identifier to be a valid string in the format 'hex(txhash):hex(blockhash):logindex'")
	})
}

func TestCleanUpStaleFilters(t *testing.T) {
	t.Parallel()
	t.Run("fails to get filter names", func(t *testing.T) {
		mockEVM := evmmock.NewEVMService(t)
		service := createTriggerObject(t, mockEVM, NewLogTriggerStore())

		mockEVM.On("GetFiltersNames", mock.Anything).Return(nil, fmt.Errorf("some error")).Once()

		service.cleanUpStaleFilters(t.Context())
		mockEVM.AssertNotCalled(t, "UnregisterLogTracking", mock.Anything, mock.Anything)
	})

	t.Run("removes stale filters (happy path)", func(t *testing.T) {
		mockEVM := evmmock.NewEVMService(t)
		store := NewLogTriggerStore()
		service := createTriggerObject(t, mockEVM, store)

		liveFilterID := service.generateFilterID("live-filter")
		staleFilterID := service.generateFilterID("stale-filter")
		otherFilterID := "other-filter-defined-outside-log-trigger-service-wont-be-removed"
		mockEVM.On("GetFiltersNames", mock.Anything).Return([]string{liveFilterID, staleFilterID, otherFilterID}, nil).Once()
		mockEVM.On("UnregisterLogTracking", mock.Anything, staleFilterID).Return(nil).Once()

		// mimicking there's a live trigger with the filter registered to log poller
		store.Write("living-trigger-1", logTriggerState{filter: filter{filterID: liveFilterID}})
		service.cleanUpStaleFilters(t.Context())
		// must call UnregisterLogTracking for staleFilterID
		mockEVM.AssertCalled(t, "UnregisterLogTracking", mock.Anything, staleFilterID)
	})

	t.Run("removes stale filters with retries (happy path)", func(t *testing.T) {
		mockEVM := evmmock.NewEVMService(t)
		store := NewLogTriggerStore()
		service := createTriggerObject(t, mockEVM, store)

		liveFilterID := service.generateFilterID("live-filter")
		staleFilterID := service.generateFilterID("stale-filter")
		otherFilterID := "other-filter-defined-outside-log-trigger-service-wont-be-removed"
		mockEVM.On("GetFiltersNames", mock.Anything).Return([]string{liveFilterID, staleFilterID, otherFilterID}, nil).Twice()
		mockEVM.On("UnregisterLogTracking", mock.Anything, staleFilterID).Return(fmt.Errorf("some error2")).Once()
		mockEVM.On("UnregisterLogTracking", mock.Anything, staleFilterID).Return(nil).Once()

		// mimicking there's a live trigger with the filter registered to log poller
		store.Write("living-trigger-1", logTriggerState{filter: filter{filterID: liveFilterID}})
		service.cleanUpStaleFilters(t.Context())
		mockEVM.AssertCalled(t, "UnregisterLogTracking", mock.Anything, staleFilterID)

		// eventually must call UnregisterLogTracking successfully for staleFilterID
		service.cleanUpStaleFilters(t.Context())
		mockEVM.AssertCalled(t, "UnregisterLogTracking", mock.Anything, staleFilterID)
	})
}

func TestNewLogTriggerService(t *testing.T) {
	evmService := initMocks(t)
	store := NewLogTriggerStore()
	beholderProcessor := test.NopBeholderProcessor{}
	messageBuilder := monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, "")

	t.Run("empty capability id", func(t *testing.T) {
		lggr := logger.Test(t)
		_, err := NewLogTriggerService(evmService, store, lggr, "", beholderProcessor, messageBuilder, time.Second, 0, 0, limits.Factory{Logger: lggr}, nil, capabilities.NewMemEventStore())
		require.Error(t, err)
		require.Contains(t, err.Error(), "capabilityID must be non-empty")
	})
	t.Run("ok initialize interval", func(t *testing.T) {
		lggr := logger.Test(t)
		trigger, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, 10*time.Second, 0, 0, limits.Factory{Logger: lggr}, nil, capabilities.NewMemEventStore())
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, trigger.logTriggerPollInterval)
		require.Equal(t, uint64(1000), trigger.logTriggerSendChannelBufferSize)
		require.Equal(t, uint64(1000), trigger.limitAndSort.Limit.Count)
	})
	t.Run("ok initialize all params", func(t *testing.T) {
		lggr := logger.Test(t)
		trigger, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, 10*time.Second, 100, 50, limits.Factory{Logger: lggr}, nil, capabilities.NewMemEventStore())
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, trigger.logTriggerPollInterval)
		require.Equal(t, uint64(100), trigger.logTriggerSendChannelBufferSize)
		require.Equal(t, uint64(50), trigger.limitAndSort.Limit.Count)
	})
	t.Run("ok initialize buffer only", func(t *testing.T) {
		lggr := logger.Test(t)
		trigger, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, 10*time.Second, 10000, 0, limits.Factory{Logger: lggr}, nil, capabilities.NewMemEventStore())
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, trigger.logTriggerPollInterval)
		require.Equal(t, uint64(10000), trigger.logTriggerSendChannelBufferSize)
		require.Equal(t, uint64(defaultLimitQueryLogSize), trigger.limitAndSort.Limit.Count) //default value for limit as 0 was provided
	})
	t.Run("ok initialize query limit only", func(t *testing.T) {
		lggr := logger.Test(t)
		trigger, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, 10*time.Second, 0, 100, limits.Factory{Logger: lggr}, nil, capabilities.NewMemEventStore())
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, trigger.logTriggerPollInterval)
		require.Equal(t, uint64(defaultSendChannelBufferSize), trigger.logTriggerSendChannelBufferSize) //default value for buffer size as 0 was provided
		require.Equal(t, uint64(100), trigger.limitAndSort.Limit.Count)
	})
	// negative tests
	t.Run("negative poll interval", func(t *testing.T) {
		lggr := logger.Test(t)
		_, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, -1*time.Second, 0, 0, limits.Factory{Logger: lggr}, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "logTriggerPollInterval must be positive, got: -1s")
	})
	t.Run("limit query log size >= send channel buffer size", func(t *testing.T) {
		lggr := logger.Test(t)
		_, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, time.Second, 5, 10, limits.Factory{Logger: lggr}, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "logTriggerLimitQueryLogSize (10) must be less than logTriggerSendChannelBufferSize (5)")
	})
	t.Run("limit query log size >= default send channel buffer size", func(t *testing.T) {
		lggr := logger.Test(t)
		_, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, time.Second, 0, defaultSendChannelBufferSize+1, limits.Factory{Logger: lggr}, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "logTriggerLimitQueryLogSize (1001) must be less than logTriggerSendChannelBufferSize (1000)")
	})
	t.Run("nil trigger event store", func(t *testing.T) {
		lggr := logger.Test(t)
		_, err := NewLogTriggerService(evmService, store, lggr, testLogTriggerCapabilityID, beholderProcessor, messageBuilder, time.Second, 0, 0, limits.Factory{Logger: lggr}, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no trigger event store provided")
	})
}

func createTriggerObject(t *testing.T, mockEVM *evmmock.EVMService, store LogTriggerStore) *LogTriggerService {
	t.Helper()
	trigger, err := NewLogTriggerService(mockEVM, store, logger.Test(t), testLogTriggerCapabilityID, test.NopBeholderProcessor{}, monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		pollInterval, 0, 0, limits.Factory{Logger: logger.Test(t)}, nil, capabilities.NewMemEventStore())
	require.NoError(t, err)
	return trigger
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

func createTriggerResponse(log *evmtypes.Log, service *LogTriggerService) capabilities.TriggerAndId[*evmcappb.Log] {
	return capabilities.TriggerAndId[*evmcappb.Log]{
		Id:      service.generateLogIdentifier(log),
		Trigger: evmcappb.ConvertLogToProto(*log),
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

func newLogTriggerService(t *testing.T) *LogTriggerService {
	lts := &LogTriggerService{
		lggr:              logger.Test(t),
		triggers:          NewLogTriggerStore(),
		beholderProcessor: test.NopBeholderProcessor{},
		messageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	}
	require.NoError(t, lts.initLimiters(limits.Factory{Logger: logger.Test(t)}))
	return lts
}
