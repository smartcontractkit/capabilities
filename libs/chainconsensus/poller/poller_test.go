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
	poller.Enqueue(requestCtx, request)

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
