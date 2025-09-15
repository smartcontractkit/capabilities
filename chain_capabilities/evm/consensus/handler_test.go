package consensus

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

func TestGetRequestIDs(t *testing.T) {
	poller := mocks.NewPoller(t)
	poller.EXPECT().Enqueue(mock.Anything, mock.Anything)
	handler := NewHandler(logger.Test(t), poller, time.Second)
	addRequestToHandler := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewEventuallyConsistentRequest(id, nil)
		_, err := handler.Handle(ctx, request)
		require.NoError(t, err)
	}

	// Empty queue
	ids, err := handler.GetRequestIDs(1)
	require.NoError(t, err)
	require.Empty(t, ids)

	// Single request in the queue
	addRequestToHandler(t, t.Context(), "req-1")
	ids, err = handler.GetRequestIDs(1)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1"}, ids)

	// Limit greater than available requests
	addRequestToHandler(t, t.Context(), "req-2")
	ids, err = handler.GetRequestIDs(5)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1", "req-2"}, ids)

	// Limit less than available requests
	ids, err = handler.GetRequestIDs(1)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1"}, ids)

	// Add another request with a canceled context
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	addRequestToHandler(t, canceledCtx, "req-3")

	addRequestToHandler(t, t.Context(), "req-4")

	// Request context canceled
	ids, err = handler.GetRequestIDs(5)
	require.NoError(t, err)
	// 'req-3' is ignored due to canceled context
	require.Equal(t, []string{"req-1", "req-2", "req-4"}, ids)
}

func TestGetRequest(t *testing.T) {
	poller := mocks.NewPoller(t)
	poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Maybe()
	handler := NewHandler(logger.Test(t), poller, time.Second)
	addRequestToHandler := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewAggregatableRequest(id, nil)
		_, err := handler.Handle(ctx, request)
		require.NoError(t, err)
	}

	// Test GetRequest for a non-existing request
	t.Run("Non Existing Request", func(t *testing.T) {
		_, ok := handler.GetRequest("non-existing-id")
		require.False(t, ok)
	})

	// Test GetRequest after adding and retrieving multiple requests
	t.Run("Multiple Requests", func(t *testing.T) {
		addRequestToHandler(t, t.Context(), "req-2")
		addRequestToHandler(t, t.Context(), "req-3")

		request, ok := handler.GetRequest("req-2")
		require.True(t, ok)
		require.Equal(t, "req-2", request.ID())

		request, ok = handler.GetRequest("req-3")
		require.True(t, ok)
		require.Equal(t, "req-3", request.ID())
	})
}

func TestCompleteRequest(t *testing.T) {
	newHandler := func(t *testing.T, lggr logger.Logger, poller Poller) *Handler {
		handler := NewHandler(lggr, poller, time.Second)
		require.NoError(t, handler.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, handler.Close())
		})
		return handler
	}

	t.Run("Happy Path", func(t *testing.T) {
		type testCase struct {
			Name          string
			Request       types.Request
			Report        *types.RequestReport
			ExpectedReply types.Reply
		}
		runTestCase := func(t *testing.T, tc testCase, requestAddedBeforeCompletion bool) {
			poller := mocks.NewPoller(t)
			handler := newHandler(t, logger.Test(t), poller)
			var ch <-chan types.Reply
			var err error
			if requestAddedBeforeCompletion {
				poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once()
				ch, err = handler.Handle(t.Context(), tc.Request)
				require.NoError(t, err)
			}

			require.NoError(t, handler.CompleteRequest(tc.Request.ID(), tc.Report))
			if !requestAddedBeforeCompletion {
				ch, err = handler.Handle(t.Context(), tc.Request)
				require.NoError(t, err)
			}

			actualReport := <-ch
			if tc.ExpectedReply.Err != nil {
				require.Contains(t, actualReport.Err.Error(), tc.ExpectedReply.Err.Error())
			} else {
				require.Equal(t, tc.ExpectedReply, actualReport)
			}
			// ensure request was removed
			ids, err := handler.GetRequestIDs(5)
			require.NoError(t, err)
			require.NotContains(t, ids, tc.Request.ID())
			// ensure unknown requests is cleaned
			handler.lock.Lock()
			defer handler.lock.Unlock()
			require.Empty(t, handler.unknownRequestsResultByID)
		}
		testCases := []testCase{
			{
				Name:    "Eventually Consistent Request",
				Request: types.NewEventuallyConsistentRequest("req-1", nil),
				Report: &types.RequestReport{
					Report: &types.RequestReport_EventuallyConsistent{EventuallyConsistent: []byte("result-data")},
				},
				ExpectedReply: types.Reply{Value: []byte("result-data")},
			},
			{
				Name:    "Eventually Consistent Request with error",
				Request: types.NewEventuallyConsistentRequest("req-1", nil),
				Report: &types.RequestReport{
					Report: &types.RequestReport_Error{Error: &types.RequestError{Errors: [][]byte{mustMarshalProto(t, status.Convert(assert.AnError).Proto())}}},
				},
				ExpectedReply: types.Reply{Err: assert.AnError},
			},
			{
				Name:    "Aggregatable Request",
				Request: types.NewAggregatableRequest("req-1", nil),
				Report: &types.RequestReport{
					Report: &types.RequestReport_Aggregatable{Aggregatable: &valuespb.Decimal{Coefficient: valuespb.NewBigIntFromInt(big.NewInt(100))}},
				},
				ExpectedReply: types.Reply{Value: &valuespb.Decimal{Coefficient: valuespb.NewBigIntFromInt(big.NewInt(100))}},
			},
			{
				Name:    "Aggregatable Request with error",
				Request: types.NewAggregatableRequest("req-1", nil),
				Report: &types.RequestReport{
					Report: &types.RequestReport_Error{Error: &types.RequestError{Errors: [][]byte{mustMarshalProto(t, status.Convert(assert.AnError).Proto())}}},
				},
				ExpectedReply: types.Reply{Err: assert.AnError},
			},
			{
				Name:    "Aggregatable Request with two errors",
				Request: types.NewAggregatableRequest("req-1", nil),
				Report: &types.RequestReport{
					Report: &types.RequestReport_Error{Error: &types.RequestError{
						Errors: [][]byte{
							mustMarshalProto(t, status.Convert(errors.New("error-1")).Proto()),
							mustMarshalProto(t, status.Convert(errors.New("error-2")).Proto()),
						}}},
				},
				ExpectedReply: types.Reply{
					Err: errors.Join(errors.New("rpc error: code = Unknown desc = error-1"),
						errors.New("rpc error: code = Unknown desc = error-2")),
				},
			},
		}
		for _, tc := range testCases {
			t.Run(tc.Name+": complete existing request", func(t *testing.T) {
				runTestCase(t, tc, true)
			})
			t.Run(tc.Name+": complete non-existing", func(t *testing.T) {
				runTestCase(t, tc, false)
			})
		}
	})
	t.Run("Eventually consistent request: expire unknown", func(t *testing.T) {
		handler := newHandler(t, logger.Test(t), nil)
		require.NoError(t, handler.CompleteRequest("request_to_expire", &types.RequestReport{
			Report: &types.RequestReport_EventuallyConsistent{EventuallyConsistent: []byte("report")},
		}))

		assert.Eventually(t, func() bool {
			handler.lock.RLock()
			defer handler.lock.RUnlock()
			return handler.unknownRequestsOrderedByTimeout.Len() == 0
		}, time.Second*10, time.Second)
	})
	t.Run("Returns error for unknown request type", func(t *testing.T) {
		handler := newHandler(t, logger.Test(t), nil)
		err := handler.CompleteRequest("id", &types.RequestReport{Report: nil})
		require.ErrorContains(t, err, "unknown request type <nil>")
	})
	t.Run("Lockable Request: returns error if height is nil", func(t *testing.T) {
		handler := newHandler(t, logger.Test(t), nil)
		err := handler.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{},
		})
		require.ErrorContains(t, err, "chain height is nil for report with requestID req-1")
	})
	t.Run("Lockable Request: emits log if request does not exist", func(t *testing.T) {
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		handler := newHandler(t, lggr, nil)
		err := handler.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "lockable to a block request req-1 not found")
	})
	t.Run("Lockable Request: emits log if request is of a wrong type", func(t *testing.T) {
		poller := mocks.NewPoller(t)
		poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once() // one call during setup
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		handler := newHandler(t, lggr, poller)

		request := types.NewEventuallyConsistentRequest("req-1", nil)
		_, err := handler.Handle(t.Context(), request)
		require.NoError(t, err)

		err = handler.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "lockable to a block request req-1 is of a different type *types.EventuallyConsistentRequest")
	})
	t.Run("Lockable Request is converted to eventually consistent and added to the poller", func(t *testing.T) {
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		poller := mocks.NewPoller(t)
		poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once() // one during conversion
		handler := newHandler(t, lggr, poller)

		request := types.NewLockableToBlockRequest("req-1", nil)
		_, err := handler.Handle(t.Context(), request)
		require.NoError(t, err)

		err = handler.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{Latest: 100}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "locked request req-1 to height latest:100")
	})
}

func TestHandle(t *testing.T) {
	poller := mocks.NewPoller(t)
	handler := NewHandler(logger.Test(t), poller, time.Second)
	require.NoError(t, handler.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, handler.Close())
	})

	t.Run("Eventually consistent request is added to poller", func(t *testing.T) {
		r := types.NewEventuallyConsistentRequest("eventually_consistent", nil)
		poller.EXPECT().Enqueue(mock.Anything, r).Once()
		_, err := handler.Handle(t.Context(), r)
		require.NoError(t, err)
	})
	t.Run("Aggregatable request is added to poller", func(t *testing.T) {
		r := types.NewAggregatableRequest("aggr_request", nil)
		poller.EXPECT().Enqueue(mock.Anything, r).Once()
		_, err := handler.Handle(t.Context(), r)
		require.NoError(t, err)
	})
	t.Run("Handle return an error if same request is added twice", func(t *testing.T) {
		r := types.NewLockableToBlockRequest("id3", nil)
		_, err := handler.Handle(t.Context(), r)
		require.NoError(t, err)
		_, err = handler.Handle(t.Context(), r)
		require.Error(t, err)
	})
	t.Run("Handle return an error if request type is not known", func(t *testing.T) {
		_, err := handler.Handle(t.Context(), &unknownRequestType{id: "unknown-request-type"})
		require.EqualError(t, err, "unknown request type *consensus.unknownRequestType")
	})
}

func mustMarshalProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	require.NoError(t, err)
	return data
}

type unknownRequestType struct {
	id string
}

func (r *unknownRequestType) ID() string {
	return r.id
}

func (r *unknownRequestType) Copy() types.Request {
	return &unknownRequestType{id: r.id}
}
