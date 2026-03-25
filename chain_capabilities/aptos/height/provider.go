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
// Aptos has single-shot finality, so latest, safe, and finalized are the same height.
type Provider struct {
	services.Service
	engine *services.Engine

	lggr                  logger.SugaredLogger
	pollPeriod            time.Duration
	ledgerVersionProvider LedgerVersionProvider

	mutex        sync.RWMutex
	latestHeight int64
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

func (p *Provider) start(ctx context.Context) error {
	p.engine.Go(p.poll)
	return nil
}

func (p *Provider) close() error {
	return nil
}

func (p *Provider) poll(ctx context.Context) {
	_ = p.pollHead(ctx)

	ticker := time.NewTicker(p.pollPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.pollHead(ctx)
		}
	}
}

func (p *Provider) pollHead(ctx context.Context) error {
	p.lggr.Debug("polling ledger version")

	ledgerVersion, err := p.ledgerVersionProvider.LedgerVersion(ctx)
	if err != nil {
		p.lggr.Errorw("failed to get latest ledger version", "error", err)
		return err
	}
	p.lggr.Debugw("fetched ledger version", "ledgerVersion", ledgerVersion)
	if ledgerVersion > uint64(math.MaxInt64) {
		err = fmt.Errorf("latest ledger version overflows int64: %d", ledgerVersion)
		p.lggr.Errorw("latest ledger version overflows int64", "ledgerVersion", ledgerVersion)
		return err
	}

	next := int64(ledgerVersion)

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if next < p.latestHeight {
		p.lggr.Warnw("ledger version moved backwards, keeping latest height",
			"ledgerVersion", next,
			"latestHeight", p.latestHeight,
		)
		next = p.latestHeight
	}

	// The chainconsensus height provider interface requires latest, safe, and finalized heights.
	// Aptos has single-shot finality, so a single stored ledger version backs all three getters.
	p.latestHeight = next
	p.lggr.Debugw("latest ledger version",
		"latestHeight", p.latestHeight,
		"safeHeight", p.latestHeight,
		"finalizedHeight", p.latestHeight,
	)
	return nil
}

func (p *Provider) GetLatest() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.latestHeight
}

func (p *Provider) GetSafe() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.latestHeight
}

func (p *Provider) GetFinalized() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.latestHeight
}

func (p *Provider) String() string {
	return fmt.Sprintf("latest=%d safe=%d finalized=%d", p.GetLatest(), p.GetSafe(), p.GetFinalized())
}
