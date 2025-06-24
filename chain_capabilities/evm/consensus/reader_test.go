package consensus

import (
	"context"
	"testing"
	"time"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chain_capabilities/evm/consensus/mocks"
	"github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

func TestGetRequestIDs(t *testing.T) {
	store := NewReader(logger.Test(t), time.Second)
	addRequestToStore := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
		_, err := store.Read(ctx, request)
		require.NoError(t, err)
	}

	// Empty queue
	ids, err := store.GetRequestIDs(1)
	require.NoError(t, err)
	require.Empty(t, ids)

	// Single request in the queue
	addRequestToStore(t, t.Context(), "req-1")
	ids, err = store.GetRequestIDs(1)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1"}, ids)

	// Limit greater than available requests
	addRequestToStore(t, t.Context(), "req-2")
	ids, err = store.GetRequestIDs(5)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1", "req-2"}, ids)

	// Limit less than available requests
	ids, err = store.GetRequestIDs(1)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1"}, ids)

	// Add another request with a canceled context
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	addRequestToStore(t, canceledCtx, "req-3")

	addRequestToStore(t, t.Context(), "req-4")

	// Request context canceled
	ids, err = store.GetRequestIDs(5)
	require.NoError(t, err)
	// 'req-3' is ignored due to canceled context
	require.Equal(t, []string{"req-2", "req-4", "req-1"}, ids) // order changes as heap does not stable sorting for equal values
}

func TestMarkAttempted(t *testing.T) {
	store := NewReader(logger.Test(t), time.Second)
	addRequestToStore := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
		_, err := store.Read(ctx, request)
		require.NoError(t, err)
	}

	// Non existing
	store.MarkAttempted("non existing")
	ids, err := store.GetRequestIDs(1)
	require.NoError(t, err)
	require.Empty(t, ids)

	// Single request in the queue
	addRequestToStore(t, t.Context(), "req-1")
	addRequestToStore(t, t.Context(), "req-2")
	ids, err = store.GetRequestIDs(2)
	require.NoError(t, err)
	require.Equal(t, []string{"req-1", "req-2"}, ids)

	// MarkAttempted lower request priority
	store.MarkAttempted("req-1")
	ids, err = store.GetRequestIDs(2)
	require.NoError(t, err)
	require.Equal(t, []string{"req-2", "req-1"}, ids)
}

func TestGetRequest(t *testing.T) {
	store := NewReader(logger.Test(t), time.Second)
	addRequestToStore := func(t *testing.T, ctx context.Context, id string) {
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
		_, err := store.Read(ctx, request)
		require.NoError(t, err)
	}

	// Test GetRequest for a non-existing request
	t.Run("Non Existing Request", func(t *testing.T) {
		_, ok := store.GetRequest("non-existing-id")
		require.False(t, ok)
	})

	// Test GetRequest after adding and retrieving multiple requests
	t.Run("Multiple Requests", func(t *testing.T) {
		addRequestToStore(t, t.Context(), "req-2")
		addRequestToStore(t, t.Context(), "req-3")

		request, ok := store.GetRequest("req-2")
		require.True(t, ok)
		require.Equal(t, "req-2", request.ID())

		request, ok = store.GetRequest("req-3")
		require.True(t, ok)
		require.Equal(t, "req-3", request.ID())
	})
}

func TestRequestObservations(t *testing.T) {
	store := NewReader(logger.Test(t), time.Second)
	const id = "req-1"
	request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
	_, err := store.Read(t.Context(), request)
	require.NoError(t, err)

	//non existing request
	const invalidID = "non-existing-request"
	observation, ok := store.GetObservation(invalidID)
	require.False(t, ok)
	require.Nil(t, observation)

	// request without observation
	observation, ok = store.GetObservation(id)
	require.False(t, ok)
	require.Nil(t, observation)

	// set observations for non existing request
	store.SetObservation(invalidID, []byte("observation"))
	observation, ok = store.GetObservation(invalidID)
	require.False(t, ok)
	require.Nil(t, observation)

	// set observation
	store.SetObservation(id, []byte("observation"))
	observation, ok = store.GetObservation(id)
	require.True(t, ok)
	require.Equal(t, []byte("observation"), observation)
}

func TestCompleteRequest(t *testing.T) {
	store := NewReader(logger.Test(t), time.Second)
	require.NoError(t, store.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	t.Run("Complete existing request", func(t *testing.T) {
		const id = "req-1"
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
		ch, err := store.Read(t.Context(), request)
		require.NoError(t, err)

		outcome := []byte("result-data")
		store.CompleteRequest(id, outcome)

		actualOutcome := <-ch
		require.Equal(t, outcome, actualOutcome)
		// ensure request was removed
		ids, err := store.GetRequestIDs(5)
		require.NoError(t, err)
		require.NotContains(t, ids, id)
	})

	t.Run("Complete non-existing request", func(t *testing.T) {
		outcome := []byte("non-existing-result")
		const id = "non-existing-req"
		store.CompleteRequest(id, outcome)

		// enqueue non existing result to get saved outcome
		request := types.NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT)
		ch, err := store.Read(t.Context(), request)
		require.NoError(t, err)
		actualOutcome := <-ch
		require.Equal(t, outcome, actualOutcome)
		// ensure unknown requests is cleaned
		store.lock.Lock()
		defer store.lock.Unlock()
		require.Empty(t, store.unknownRequestsResultByID)
	})

	t.Run("Expire unknown requests", func(t *testing.T) {
		store.CompleteRequest("expired-req", []byte("expired-result"))

		assert.Eventually(t, func() bool {
			store.lock.RLock()
			defer store.lock.RUnlock()
			return store.unknownRequestsOrderedByTimeout.Len() == 0
		}, time.Second*10, time.Second)
	})
}

func TestRead(t *testing.T) {
	store := NewReader(logger.Test(t), time.Second)
	require.NoError(t, store.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	poller := mocks.NewPoller(t)
	store.SetPoller(poller)

	t.Run("Eventually consistent request is added to poller", func(t *testing.T) {
		r := types.NewEventuallyConsistentRequest("id", nil)
		poller.EXPECT().Enqueue(mock.Anything, r).Once()
		_, err := store.Read(t.Context(), r)
		require.NoError(t, err)
	})
	t.Run("Lockable to a block request added to the poller after update", func(t *testing.T) {
		r := types.NewLockableToABlockRequest("id2", nil)
		_, err := store.Read(t.Context(), r)
		require.NoError(t, err)
		eventuallyConsistentRequest := r.ToEventuallyConsistent(&evmservice.ChainHeight{})
		poller.EXPECT().Enqueue(mock.Anything, eventuallyConsistentRequest).Once()
		store.Update(eventuallyConsistentRequest)
	})
	t.Run("Read return an error if same request is added twice", func(t *testing.T) {
		r := types.NewRequest("id3", evmservice.RequestType_REQUEST_TYPE_UNKNOWN)
		_, err := store.Read(t.Context(), r)
		require.NoError(t, err)
		_, err = store.Read(t.Context(), r)
		require.Error(t, err)
	})
}
