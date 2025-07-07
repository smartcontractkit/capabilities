package poller

import (
	"context"
	"testing"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

func TestPoller_ObservesRequestUntilCanceled(t *testing.T) {
	// Setup
	lggr, observedLogs := logger.TestObserved(t, zapcore.DebugLevel)

	const requestID = "request-1"
	const requestObservation = "request-observation"

	// Create poller with short poll period for faster testing
	pollPeriod := 10 * time.Millisecond
	poller := NewPoller(lggr, 1, pollPeriod)

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
