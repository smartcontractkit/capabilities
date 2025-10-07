package poller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"

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
		observationsCount++
		switch observationsCount {
		case 1:
			return nil, assert.AnError
		case 2:
			return []byte(requestObservation), nil
		case 3:
			panic("request panicked")
		case 4: // signal that request was processed
			requestCancel()
			return []byte(requestObservation), nil
		default:
			require.FailNow(t, "expected request to be removed from the poling queue")
			return nil, nil
		}
	})

	// Handle the request
	poller.Enqueue(requestCtx, request)

	tests.AssertLogEventually(t, observedLogs, "request panicked")
	tests.AssertLogEventually(t, observedLogs, "request was canceled - removing from queue")
}
