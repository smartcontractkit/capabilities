package height

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type LedgerVersionProvider interface {
	LedgerVersion(ctx context.Context) (uint64, error)
}

// Provider polls Aptos latest ledger version and exposes it as chain heights for consensus.
// Aptos has single-shot finality, so latest/safe/finalized are treated the same.
type Provider struct {
	services.Service
	engine *services.Engine

	lggr                  logger.SugaredLogger
	pollPeriod            time.Duration
	ledgerVersionProvider LedgerVersionProvider

	mutex           sync.RWMutex
	latestHeight    int64
	safeHeight      int64
	finalizedHeight int64
}

func NewProvider(lggr logger.Logger, pollPeriod time.Duration, ledgerVersionProvider LedgerVersionProvider) *Provider {
	p := &Provider{
		pollPeriod:            pollPeriod,
		ledgerVersionProvider: ledgerVersionProvider,
	}

	p.Service, p.engine = services.Config{
		Name:  "AptosHeightProvider",
		Start: p.start,
		Close: p.close,
	}.NewServiceEngine(lggr)

	p.lggr = p.engine.SugaredLogger
	return p
}

func (p *Provider) start(_ context.Context) error {
	p.engine.Go(p.poll)
	return nil
}

func (p *Provider) close() error {
	return nil
}

func (p *Provider) poll(ctx context.Context) {
	ticker := time.NewTicker(p.pollPeriod)
	defer ticker.Stop()

	// Prime heights on startup to reduce chance of locking reads to zero.
	p.pollHead(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollHead(ctx)
		}
	}
}

func (p *Provider) pollHead(ctx context.Context) {
	ledgerVersion, err := p.ledgerVersionProvider.LedgerVersion(ctx)
	if err != nil {
		p.lggr.Errorw("failed to get latest ledger version", "error", err)
		return
	}
	if ledgerVersion > uint64(math.MaxInt64) {
		p.lggr.Errorw("latest ledger version overflows int64", "ledgerVersion", ledgerVersion)
		return
	}

	latest := int64(ledgerVersion)

	p.mutex.Lock()
	defer p.mutex.Unlock()

	prevLatest := p.latestHeight
	prevSafe := p.safeHeight
	prevFinalized := p.finalizedHeight

	// Aptos has single-shot finality; latest/safe/finalized should track together.
	next := max(p.latestHeight, latest)
	p.latestHeight = next
	p.safeHeight = next
	p.finalizedHeight = next

	if p.latestHeight != prevLatest || p.safeHeight != prevSafe || p.finalizedHeight != prevFinalized {
		p.lggr.Debugw(
			"updated Aptos consensus heights",
			"prev_latest", prevLatest,
			"prev_safe", prevSafe,
			"prev_finalized", prevFinalized,
			"new_latest", p.latestHeight,
			"new_safe", p.safeHeight,
			"new_finalized", p.finalizedHeight,
		)
	}
}

func (p *Provider) GetLatest() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.latestHeight
}

func (p *Provider) GetSafe() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.safeHeight
}

func (p *Provider) GetFinalized() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.finalizedHeight
}

func (p *Provider) String() string {
	return fmt.Sprintf("latest=%d safe=%d finalized=%d", p.GetLatest(), p.GetSafe(), p.GetFinalized())
}
