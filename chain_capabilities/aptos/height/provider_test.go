package height

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

type testLedgerVersionProvider struct {
	mu      sync.Mutex
	results []ledgerVersionResult
	next    int
}

type ledgerVersionResult struct {
	version uint64
	err     error
}

func (p *testLedgerVersionProvider) LedgerVersion(context.Context) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.next >= len(p.results) {
		last := p.results[len(p.results)-1]
		return last.version, last.err
	}

	result := p.results[p.next]
	p.next++
	return result.version, result.err
}

func TestProviderPollsAndPublishesLatestVersion(t *testing.T) {
	t.Parallel()

	p := NewProvider(
		logger.Test(t),
		10*time.Millisecond,
		&testLedgerVersionProvider{results: []ledgerVersionResult{{version: 100}, {version: 101}}},
	)

	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, p.Close())
	})

	require.Eventually(t, func() bool {
		return p.GetLatest() >= 100 && p.GetSafe() >= 100 && p.GetFinalized() >= 100
	}, time.Second, 10*time.Millisecond)
}

func TestProviderPollHeadDoesNotRegressHeight(t *testing.T) {
	t.Parallel()

	p := NewProvider(
		logger.Test(t),
		time.Second,
		&testLedgerVersionProvider{results: []ledgerVersionResult{{version: 100}, {version: 99}}},
	)

	require.NoError(t, p.pollHead(context.Background()))
	require.NoError(t, p.pollHead(context.Background()))

	require.Equal(t, int64(100), p.GetLatest())
	require.Equal(t, int64(100), p.GetSafe())
	require.Equal(t, int64(100), p.GetFinalized())
}

func TestProviderPollHeadRetainsLastHeightOnError(t *testing.T) {
	t.Parallel()

	p := NewProvider(
		logger.Test(t),
		time.Second,
		&testLedgerVersionProvider{results: []ledgerVersionResult{{version: 100}, {err: errors.New("boom")}}},
	)

	require.NoError(t, p.pollHead(context.Background()))
	require.Error(t, p.pollHead(context.Background()))

	require.Equal(t, int64(100), p.GetLatest())
	require.Equal(t, int64(100), p.GetSafe())
	require.Equal(t, int64(100), p.GetFinalized())
}

func TestProviderPollHeadIgnoresOverflow(t *testing.T) {
	t.Parallel()

	p := NewProvider(
		logger.Test(t),
		time.Second,
		&testLedgerVersionProvider{results: []ledgerVersionResult{{version: uint64(math.MaxInt64) + 1}}},
	)

	require.Error(t, p.pollHead(context.Background()))

	require.Equal(t, int64(0), p.GetLatest())
	require.Equal(t, int64(0), p.GetSafe())
	require.Equal(t, int64(0), p.GetFinalized())
}

func TestProviderStartDoesNotFailWhenInitialPollFails(t *testing.T) {
	t.Parallel()

	p := NewProvider(
		logger.Test(t),
		10*time.Millisecond,
		&testLedgerVersionProvider{results: []ledgerVersionResult{{err: errors.New("boom")}, {version: 100}}},
	)

	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, p.Close())
	})

	require.Eventually(t, func() bool {
		return p.GetLatest() == 100 && p.GetSafe() == 100 && p.GetFinalized() == 100
	}, time.Second, 10*time.Millisecond)
}
