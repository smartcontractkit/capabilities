package height

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
)

type testHeadProvider struct {
	mu    sync.Mutex
	heads []types.Head
	next  int
}

func (p *testHeadProvider) LatestHead(context.Context) (types.Head, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.heads) {
		return p.heads[len(p.heads)-1], nil
	}
	h := p.heads[p.next]
	p.next++
	return h, nil
}

func TestProviderPollsAndPublishesLatestVersion(t *testing.T) {
	t.Parallel()

	p := NewProvider(
		logger.Test(t),
		10*time.Millisecond,
		&testHeadProvider{heads: []types.Head{{Height: "100"}, {Height: "101"}}},
	)

	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, p.Close())
	})

	require.Eventually(t, func() bool {
		return p.GetLatest() >= 100 && p.GetSafe() >= 100 && p.GetFinalized() >= 100
	}, time.Second, 10*time.Millisecond)
}
