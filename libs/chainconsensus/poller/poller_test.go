package poller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics/mocks"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/test"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func TestPoller_ObservesRequestUntilCanceled(t *testing.T) {
	// Setup
	lggr, observedLogs := logger.TestObserved(t, zapcore.DebugLevel)

	const requestID = "request-1"
	const requestObservation = "request-observation"

	// Create poller with short poll period for faster testing
	pollPeriod := 10 * time.Millisecond
	poller := NewPoller(lggr, test.GetConsensusMetrics(t), 1, pollPeriod)

	// Start the poller
	require.NoError(t, poller.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, poller.Close())
	})

	// Create a request that will fail multiple times before succeeding
	var observationsCount int
	requestCtx, requestCancel := context.WithCancel(t.Context())
	request := types.NewEventuallyConsistentRequest(requestID, func(ctx context.Context) ([]byte, error) {
		// cancel request
		const maxCalls = 3
		if observationsCount == maxCalls {
			requestCancel()
		} else if observationsCount > maxCalls {
			require.FailNow(t, "expected request to be removed from the poling queue")
		}
		observationsCount++
		if observationsCount%2 == 0 {
			return []byte(requestObservation), nil
		}
		return nil, assert.AnError
	})

	// Handle the request
	require.NoError(t, poller.Enqueue(requestCtx, request))

	tests.AssertLogEventually(t, observedLogs, "request was canceled - removing from queue")
}

func TestPoller_RecordRetryQueueSizeAfterProcessing(t *testing.T) {
	metricsMock := mocks.NewConsensusMetrics(t)
	// Retry queue holds one item after processing; requests queue still has two.
	metricsMock.EXPECT().RecordRetryQueueSize(mock.Anything, 1).Once()

	poller := NewPoller(logger.Test(t), metricsMock, 1, time.Hour)
	require.NoError(t, poller.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, poller.Close())
	})

	ctx := t.Context()
	queuedReq := types.NewEventuallyConsistentRequest("queued", func(context.Context) ([]byte, error) {
		return nil, assert.AnError
	})

	poller.mutex.Lock()
	poller.requests.PushBack(requestToPoll{ObservableRequest: queuedReq, Ctx: ctx})
	poller.requests.PushBack(requestToPoll{ObservableRequest: queuedReq, Ctx: ctx})
	poller.mutex.Unlock()

	require.Equal(t, 2, poller.requests.Len(), "requests queue prefilled for test")
	require.Equal(t, 0, poller.retryQueue.Len())

	processingReq := types.NewEventuallyConsistentRequest("processing", func(context.Context) ([]byte, error) {
		return []byte("observation"), nil
	})
	poller.processRequest(requestToPoll{ObservableRequest: processingReq, Ctx: ctx})

	require.Equal(t, 1, poller.retryQueue.Len())
	require.Equal(t, 2, poller.requests.Len(),
		"requests queue size must differ from retry queue size for this assertion to be meaningful")
}

func TestPoller_EnqueueRejectsWhenFull(t *testing.T) {
	metricsMock := mocks.NewConsensusMetrics(t)
	metricsMock.EXPECT().RecordQueueSize(mock.Anything, mock.Anything).Maybe()
	metricsMock.EXPECT().IncQueueRejected(mock.Anything).Once()

	// Not started: everything stays in the input queue, so the cap is exercised directly.
	poller := NewPoller(logger.Test(t), metricsMock, 1, time.Hour, WithMaxQueuedRequests(2))
	newRequest := func(id string) *types.EventuallyConsistentRequest {
		return types.NewEventuallyConsistentRequest(id, func(context.Context) ([]byte, error) { return nil, nil })
	}

	require.NoError(t, poller.Enqueue(t.Context(), newRequest("1")))
	require.NoError(t, poller.Enqueue(t.Context(), newRequest("2")))
	require.ErrorIs(t, poller.Enqueue(t.Context(), newRequest("3")), ErrQueueFull)
	require.Equal(t, 2, poller.requests.Len())
}

func TestPoller_CanceledRequestsReleaseCapacity(t *testing.T) {
	metricsMock := mocks.NewConsensusMetrics(t)
	metricsMock.EXPECT().RecordQueueSize(mock.Anything, mock.Anything).Maybe()
	metricsMock.EXPECT().IncQueueRejected(mock.Anything).Maybe()
	metricsMock.EXPECT().RecordRetryQueueSize(mock.Anything, mock.Anything).Maybe()

	poller := NewPoller(logger.Test(t), metricsMock, 1, time.Hour, WithMaxQueuedRequests(1))
	observe := func(context.Context) ([]byte, error) { return []byte("observation"), nil }

	t.Run("canceled before a worker picks it up", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		require.NoError(t, poller.Enqueue(ctx, types.NewEventuallyConsistentRequest("a", observe)))
		require.ErrorIs(t, poller.Enqueue(t.Context(), types.NewEventuallyConsistentRequest("b", observe)), ErrQueueFull)

		cancel()
		request := poller.popFirst(t.Context())
		require.NotNil(t, request)
		poller.processRequest(*request)

		require.Equal(t, 0, poller.retryQueue.Len(), "canceled request must not be scheduled for retry")
		require.NoError(t, poller.Enqueue(t.Context(), types.NewEventuallyConsistentRequest("b", observe)), "slot is free again")
		require.NotNil(t, poller.popFirst(t.Context()))
		poller.mutex.Lock()
		poller.tracked = 0
		poller.mutex.Unlock()
	})

	t.Run("canceled while waiting in the retry queue", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		require.NoError(t, poller.Enqueue(ctx, types.NewEventuallyConsistentRequest("c", observe)))
		request := poller.popFirst(t.Context())
		require.NotNil(t, request)
		poller.processRequest(*request)
		require.Equal(t, 1, poller.retryQueue.Len())

		cancel()
		poller.scheduleReadyForReprocessing(t.Context(), time.Now().Add(2*time.Hour))

		require.Equal(t, 0, poller.retryQueue.Len())
		require.Equal(t, 0, poller.requests.Len(), "canceled request must not be re-enqueued")
		require.NoError(t, poller.Enqueue(t.Context(), types.NewEventuallyConsistentRequest("d", observe)), "slot is free again")
	})
}

func TestPoller_ObservationAttemptHasDeadline(t *testing.T) {
	metricsMock := mocks.NewConsensusMetrics(t)
	metricsMock.EXPECT().RecordRetryQueueSize(mock.Anything, mock.Anything).Once()

	poller := NewPoller(logger.Test(t), metricsMock, 1, time.Hour, WithObservationTimeout(10*time.Millisecond))
	var observed context.Context
	request := types.NewEventuallyConsistentRequest("slow", func(ctx context.Context) ([]byte, error) {
		observed = ctx
		<-ctx.Done()
		return nil, ctx.Err()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		poller.processRequest(requestToPoll{ObservableRequest: request, Ctx: t.Context()})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "observation attempt was not bounded by the observation timeout")
	}
	require.ErrorIs(t, observed.Err(), context.DeadlineExceeded)
	require.Equal(t, 1, poller.retryQueue.Len(), "a timed-out attempt is retried on the next poll")
}
