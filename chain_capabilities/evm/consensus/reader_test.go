package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

func TestGetRequestIDs(t *testing.T) {
	poller := mocks.NewPoller(t)
	poller.EXPECT().Enqueue(mock.Anything, mock.Anything)
	reader := NewReader(logger.Test(t), poller, time.Second)
	addRequestToReader := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewEventuallyConsistentRequest(id, nil)
		_, err := reader.Read(ctx, request)
		require.NoError(t, err)
	}

	// Empty queue
	ids, err := reader.GetRequestIDs(1)
	require.NoError(t, err)
	require.Empty(t, ids)

	// Single request in the queue
	addRequestToReader(t, t.Context(), "req-1")
	ids, err = reader.GetRequestIDs(1)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1"}, ids)

	// Limit greater than available requests
	addRequestToReader(t, t.Context(), "req-2")
	ids, err = reader.GetRequestIDs(5)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1", "req-2"}, ids)

	// Limit less than available requests
	ids, err = reader.GetRequestIDs(1)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1"}, ids)

	// Add another request with a canceled context
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	addRequestToReader(t, canceledCtx, "req-3")

	addRequestToReader(t, t.Context(), "req-4")

	// Request context canceled
	ids, err = reader.GetRequestIDs(5)
	require.NoError(t, err)
	// 'req-3' is ignored due to canceled context
	require.Equal(t, []string{"req-2", "req-4", "req-1"}, ids) // order changes as heap does not stable sorting for equal values
}

func TestMarkAttempted(t *testing.T) {
	reader := NewReader(logger.Test(t), nil, time.Second)
	addRequestToReader := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewAggregatableRequest(id, nil)
		_, err := reader.Read(ctx, request)
		require.NoError(t, err)
	}

	// Non existing
	reader.MarkAttempted("non existing")
	ids, err := reader.GetRequestIDs(1)
	require.NoError(t, err)
	require.Empty(t, ids)

	// Single request in the queue
	addRequestToReader(t, t.Context(), "req-1")
	addRequestToReader(t, t.Context(), "req-2")
	ids, err = reader.GetRequestIDs(2)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1", "req-2"}, ids)

	// MarkAttempted lower request priority
	reader.MarkAttempted("req-1")
	ids, err = reader.GetRequestIDs(2)
	require.NoError(t, err)
	require.Equal(t, []string{"req-2", "req-1"}, ids)
}

func TestGetRequest(t *testing.T) {
	reader := NewReader(logger.Test(t), nil, time.Second)
	addRequestToReader := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewAggregatableRequest(id, nil)
		_, err := reader.Read(ctx, request)
		require.NoError(t, err)
	}

	// Test GetRequest for a non-existing request
	t.Run("Non Existing Request", func(t *testing.T) {
		_, ok := reader.GetRequest("non-existing-id")
		require.False(t, ok)
	})

	// Test GetRequest after adding and retrieving multiple requests
	t.Run("Multiple Requests", func(t *testing.T) {
		addRequestToReader(t, t.Context(), "req-2")
		addRequestToReader(t, t.Context(), "req-3")

		request, ok := reader.GetRequest("req-2")
		require.True(t, ok)
		require.Equal(t, "req-2", request.ID())

		request, ok = reader.GetRequest("req-3")
		require.True(t, ok)
		require.Equal(t, "req-3", request.ID())
	})
}

func TestCompleteRequest(t *testing.T) {
	newReader := func(t *testing.T, lggr logger.Logger, poller Poller) *Reader {
		reader := NewReader(lggr, poller, time.Second)
		require.NoError(t, reader.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, reader.Close())
		})
		return reader
	}

	t.Run("Eventually consistent request: complete existing request", func(t *testing.T) {
		const id = "req-1"
		request := types.NewEventuallyConsistentRequest(id, nil)
		poller := mocks.NewPoller(t)
		poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once()
		reader := newReader(t, logger.Test(t), poller)
		ch, err := reader.Read(t.Context(), request)
		require.NoError(t, err)

		report := []byte("result-data")
		require.NoError(t, reader.CompleteRequest(id, &types.RequestReport{
			Report: &types.RequestReport_EventuallyConsistent{EventuallyConsistent: report},
		}))

		actualReport := <-ch
		require.Equal(t, report, actualReport)
		// ensure request was removed
		ids, err := reader.GetRequestIDs(5)
		require.NoError(t, err)
		require.NotContains(t, ids, id)
	})

	t.Run("Eventually consistent request: complete non-existing", func(t *testing.T) {
		const id = "non-existing-req"
		report := []byte("non-existing-result")
		reader := newReader(t, logger.Test(t), nil)
		require.NoError(t, reader.CompleteRequest(id, &types.RequestReport{
			Report: &types.RequestReport_EventuallyConsistent{EventuallyConsistent: report},
		}))

		// enqueue non existing result to get saved outcome
		request := types.NewEventuallyConsistentRequest(id, nil)
		ch, err := reader.Read(t.Context(), request)
		require.NoError(t, err)
		actualReport := <-ch
		require.Equal(t, report, actualReport)
		// ensure unknown requests is cleaned
		reader.lock.Lock()
		defer reader.lock.Unlock()
		require.Empty(t, reader.unknownRequestsResultByID)
	})

	t.Run("Eventually consistent request: expire unknown", func(t *testing.T) {
		reader := newReader(t, logger.Test(t), nil)
		require.NoError(t, reader.CompleteRequest("request_to_expire", &types.RequestReport{
			Report: &types.RequestReport_EventuallyConsistent{EventuallyConsistent: []byte("report")},
		}))

		assert.Eventually(t, func() bool {
			reader.lock.RLock()
			defer reader.lock.RUnlock()
			return reader.unknownRequestsOrderedByTimeout.Len() == 0
		}, time.Second*10, time.Second)
	})
	t.Run("Returns error for unknown request type", func(t *testing.T) {
		reader := newReader(t, logger.Test(t), nil)
		err := reader.CompleteRequest("id", &types.RequestReport{Report: nil})
		require.ErrorContains(t, err, "unknown request type <nil>")
	})
	t.Run("Lockable Request: returns error if height is nil", func(t *testing.T) {
		reader := newReader(t, logger.Test(t), nil)
		err := reader.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{},
		})
		require.ErrorContains(t, err, "chain height is nil for report with requestID req-1")
	})
	t.Run("Lockable Request: emits log if request does not exist", func(t *testing.T) {
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		reader := newReader(t, lggr, nil)
		err := reader.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "lockable to a block request req-1 not found")
	})
	t.Run("Lockable Request: emits log if request is of a wrong type", func(t *testing.T) {
		poller := mocks.NewPoller(t)
		poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once() // one call during setup
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		reader := newReader(t, lggr, poller)

		request := types.NewEventuallyConsistentRequest("req-1", nil)
		_, err := reader.Read(t.Context(), request)
		require.NoError(t, err)

		err = reader.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "lockable to a block request req-1 is of a different type *types.EventuallyConsistentRequest")
	})
	t.Run("Lockable Request is converted to eventually consistent and added to the poller", func(t *testing.T) {
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		poller := mocks.NewPoller(t)
		poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once() // one during conversion
		reader := newReader(t, lggr, poller)

		request := types.NewLockableToBlockRequest("req-1", nil)
		_, err := reader.Read(t.Context(), request)
		require.NoError(t, err)

		err = reader.CompleteRequest("req-1", &types.RequestReport{
			Report: &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{Latest: 100}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "locked request req-1 to height latest:100")
	})
}

func TestRead(t *testing.T) {
	poller := mocks.NewPoller(t)
	reader := NewReader(logger.Test(t), poller, time.Second)
	require.NoError(t, reader.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
	})

	t.Run("Eventually consistent request is added to poller", func(t *testing.T) {
		r := types.NewEventuallyConsistentRequest("id", nil)
		poller.EXPECT().Enqueue(mock.Anything, r).Once()
		_, err := reader.Read(t.Context(), r)
		require.NoError(t, err)
	})
	t.Run("Read return an error if same request is added twice", func(t *testing.T) {
		r := types.NewLockableToBlockRequest("id3", nil)
		_, err := reader.Read(t.Context(), r)
		require.NoError(t, err)
		_, err = reader.Read(t.Context(), r)
		require.Error(t, err)
	})
}
