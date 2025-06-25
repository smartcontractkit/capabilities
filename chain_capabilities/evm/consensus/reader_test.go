package consensus

import (
	"context"
	"testing"
	"time"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chain_capabilities/evm/consensus/mocks"
	"github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

func TestGetRequestIDs(t *testing.T) {
	reader := NewReader(logger.Test(t), time.Second)
	addRequestToReader := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
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
	reader := NewReader(logger.Test(t), time.Second)
	addRequestToReader := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
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
	reader := NewReader(logger.Test(t), time.Second)
	addRequestToReader := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
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

func TestRequestObservations(t *testing.T) {
	reader := NewReader(logger.Test(t), time.Second)
	const id = "req-1"
	request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
	_, err := reader.Read(t.Context(), request)
	require.NoError(t, err)

	//non existing request
	const invalidID = "non-existing-request"
	observation, ok := reader.GetObservation(invalidID)
	require.False(t, ok)
	require.Nil(t, observation)

	// request without observation
	observation, ok = reader.GetObservation(id)
	require.False(t, ok)
	require.Nil(t, observation)

	// set observations for non existing request
	reader.SetObservation(invalidID, []byte("observation"))
	observation, ok = reader.GetObservation(invalidID)
	require.False(t, ok)
	require.Nil(t, observation)

	// set observation
	reader.SetObservation(id, []byte("observation"))
	observation, ok = reader.GetObservation(id)
	require.True(t, ok)
	require.Equal(t, []byte("observation"), observation)
}

func TestCompleteRequest(t *testing.T) {
	newReader := func(t *testing.T, lggr logger.Logger) *Reader {
		reader := NewReader(lggr, time.Second)
		require.NoError(t, reader.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, reader.Close())
		})
		return reader
	}

	t.Run("Eventually consistent request: complete existing request", func(t *testing.T) {
		const id = "req-1"
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
		reader := newReader(t, logger.Test(t))
		ch, err := reader.Read(t.Context(), request)
		require.NoError(t, err)

		report := []byte("result-data")
		require.NoError(t, reader.CompleteRequest(id, &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT,
			Payload:     &evmservice.RequestReport_Value{Value: report},
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
		reader := newReader(t, logger.Test(t))
		require.NoError(t, reader.CompleteRequest(id, &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT,
			Payload:     &evmservice.RequestReport_Value{Value: report},
		}))

		// enqueue non existing result to get saved outcome
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
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
		reader := newReader(t, logger.Test(t))
		require.NoError(t, reader.CompleteRequest("request_to_expire", &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT,
			Payload:     &evmservice.RequestReport_Value{Value: []byte("report")},
		}))

		assert.Eventually(t, func() bool {
			reader.lock.RLock()
			defer reader.lock.RUnlock()
			return reader.unknownRequestsOrderedByTimeout.Len() == 0
		}, time.Second*10, time.Second)
	})
	t.Run("Returns error for unknown request type", func(t *testing.T) {
		reader := newReader(t, logger.Test(t))
		err := reader.CompleteRequest("id", &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_UNKNOWN,
		})
		require.ErrorContains(t, err, "unknown request type REQUEST_TYPE_UNKNOWN")
	})
	t.Run("Lockable Request: returns error if height is nil", func(t *testing.T) {
		reader := newReader(t, logger.Test(t))
		err := reader.CompleteRequest("req-1", &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK,
		})
		require.ErrorContains(t, err, "chain height is nil for report with requestID req-1")
	})
	t.Run("Lockable Request: emits log if request does not exist", func(t *testing.T) {
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		reader := newReader(t, lggr)
		err := reader.CompleteRequest("req-1", &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK,
			Payload:     &evmservice.RequestReport_Height{Height: &evmservice.ChainHeight{}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "lockable to a block request req-1 not found")
	})
	t.Run("Lockable Request: emits log if request is of a wrong type", func(t *testing.T) {
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		reader := newReader(t, lggr)
		poller := mocks.NewPoller(t)
		poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once() // one call during setup
		reader.SetPoller(poller)

		request := types.NewEventuallyConsistentRequest("req-1", nil)
		_, err := reader.Read(t.Context(), request)
		require.NoError(t, err)

		err = reader.CompleteRequest("req-1", &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK,
			Payload:     &evmservice.RequestReport_Height{Height: &evmservice.ChainHeight{}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "lockable to a block request req-1 is of a different type *types.eventuallyConsistentRequest")
	})
	t.Run("Lockable Request is converted to eventually consistent and added to the poller", func(t *testing.T) {
		lggr, observed := logger.TestObserved(t, zapcore.InfoLevel)
		reader := newReader(t, lggr)
		poller := mocks.NewPoller(t)
		poller.EXPECT().Enqueue(mock.Anything, mock.Anything).Once() // one during conversion
		reader.SetPoller(poller)

		request := types.NewLockableToABlockRequest("req-1", nil)
		_, err := reader.Read(t.Context(), request)
		require.NoError(t, err)

		err = reader.CompleteRequest("req-1", &evmservice.RequestReport{
			RequestType: evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK,
			Payload:     &evmservice.RequestReport_Height{Height: &evmservice.ChainHeight{Latest: 100}},
		})
		require.NoError(t, err)
		tests.RequireLogMessage(t, observed, "locked request req-1 to height latest:100")
	})
}

func TestRead(t *testing.T) {
	reader := NewReader(logger.Test(t), time.Second)
	require.NoError(t, reader.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
	})
	poller := mocks.NewPoller(t)
	reader.SetPoller(poller)

	t.Run("Eventually consistent request is added to poller", func(t *testing.T) {
		r := types.NewEventuallyConsistentRequest("id", nil)
		poller.EXPECT().Enqueue(mock.Anything, r).Once()
		_, err := reader.Read(t.Context(), r)
		require.NoError(t, err)
	})
	t.Run("Read return an error if same request is added twice", func(t *testing.T) {
		r := types.NewRequest("id3", evmservice.RequestType_REQUEST_TYPE_UNKNOWN)
		_, err := reader.Read(t.Context(), r)
		require.NoError(t, err)
		_, err = reader.Read(t.Context(), r)
		require.Error(t, err)
	})
}
