package capability

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// TestRunServesTheDebugUIWhenAsked covers the opt-in: with --capabilities.http-debug the page is
// served under /debug/capabilities.
func TestRunServesTheDebugUIWhenAsked(t *testing.T) {
	cfg, c, port := runArgs(t, newFake)
	cfg.capabilities.HTTPDebug = true

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, logger.Test(t), "cron", cfg, c) }()

	require.Eventually(t, func() bool { return serving(int(port)) }, 30*time.Second, 5*time.Millisecond,
		"the run should be serving while it is up")

	res, err := http.Get(fmt.Sprintf("http://localhost:%d/debug/capabilities/ui/", port))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusOK, res.StatusCode)

	cancel()
	require.NoError(t, <-done)
}

// TestRunServesNoDebugUIByDefault pins the default: the UI invokes capabilities, so a run that
// was not asked for it does not expose it.
func TestRunServesNoDebugUIByDefault(t *testing.T) {
	cfg, c, port := runArgs(t, newFake)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, logger.Test(t), "cron", cfg, c) }()

	require.Eventually(t, func() bool { return serving(int(port)) }, 30*time.Second, 5*time.Millisecond,
		"the run should be serving while it is up")

	res, err := http.Get(fmt.Sprintf("http://localhost:%d/debug/capabilities/ui/", port))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusNotFound, res.StatusCode)

	cancel()
	require.NoError(t, <-done)
}
