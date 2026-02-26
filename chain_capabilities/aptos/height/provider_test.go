package height

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

type testLedgerVersionProvider struct {
	mu       sync.Mutex
	versions []uint64
	next     int
}

func (p *testLedgerVersionProvider) LedgerVersion(context.Context) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.versions) {
		return p.versions[len(p.versions)-1], nil
	}
	v := p.versions[p.next]
	p.next++
	return v, nil
}

func TestProviderPollsAndPublishesLatestVersion(t *testing.T) {
	t.Parallel()

	p := NewProvider(
		logger.Test(t),
		10*time.Millisecond,
		&testLedgerVersionProvider{versions: []uint64{100, 101}},
	)

	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, p.Close())
	})

	require.Eventually(t, func() bool {
		return p.GetLatest() >= 100 && p.GetSafe() >= 100 && p.GetFinalized() >= 100
	}, time.Second, 10*time.Millisecond)
}
