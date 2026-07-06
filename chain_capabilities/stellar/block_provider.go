package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
)

// latestLedgerProvider is the subset of the Stellar chain service used to poll the
// latest closed ledger sequence.
type latestLedgerProvider interface {
	GetLatestLedger(ctx context.Context) (stellartypes.GetLatestLedgerResponse, error)
}

// blocksProvider polls the latest closed Stellar ledger sequence and exposes it as the DON's common chain height for consensus reads.
// Stellar ledgers are final once closed (no reorgs), so latest == safe == finalized == the latest ledger sequence.
type blocksProvider struct {
	services.Service
	engine *services.Engine

	lggr       logger.SugaredLogger
	pollPeriod time.Duration
	service    latestLedgerProvider

	mutex    sync.RWMutex
	sequence int64
}

func newBlocksProvider(lggr logger.Logger, pollPeriod time.Duration, service latestLedgerProvider) (*blocksProvider, error) {
	if pollPeriod <= 0 {
		return nil, fmt.Errorf("block provider poll period must be positive, got %s", pollPeriod)
	}
	if service == nil {
		return nil, fmt.Errorf("block provider requires a non-nil ledger service")
	}
	b := &blocksProvider{
		pollPeriod: pollPeriod,
		service:    service,
	}
	b.Service, b.engine = services.Config{
		Name:  "StellarHeightProvider",
		Start: b.start,
		Close: b.close,
	}.NewServiceEngine(lggr)
	b.lggr = b.engine.SugaredLogger
	return b, nil
}

func (b *blocksProvider) start(_ context.Context) error {
	b.engine.Go(b.poll)
	return nil
}

func (b *blocksProvider) close() error { return nil }

func (b *blocksProvider) poll(ctx context.Context) {
	b.pollLedger(ctx)
	ticker := time.NewTicker(b.pollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.pollLedger(ctx)
		}
	}
}

func (b *blocksProvider) pollLedger(ctx context.Context) {
	resp, err := b.service.GetLatestLedger(ctx)
	if err != nil {
		b.lggr.Errorw("failed to poll latest ledger", "error", err)
		return
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	// Ledger sequence is monotonically increasing; retain the max so a lagging RPC
	// can't lower the agreed height across rounds.
	if seq := int64(resp.Sequence); seq > b.sequence {
		b.sequence = seq
	}
}

func (b *blocksProvider) GetLatest() int64    { return b.get() }
func (b *blocksProvider) GetSafe() int64      { return b.get() }
func (b *blocksProvider) GetFinalized() int64 { return b.get() }

func (b *blocksProvider) get() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.sequence
}
