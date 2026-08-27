package height

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
)

// LedgerProvider is the subset of the Stellar chain service used to poll the latest closed ledger sequence.
type LedgerProvider interface {
	GetLatestLedger(ctx context.Context) (stellartypes.GetLatestLedgerResponse, error)
}

// Provider is a service that polls the latest closed Stellar ledger sequence.
type Provider struct {
	services.Service
	engine *services.Engine

	lggr       logger.SugaredLogger
	pollPeriod time.Duration
	service    LedgerProvider

	mutex    sync.RWMutex
	sequence int64
}

// NewProvider builds a Stellar height provider
func NewProvider(lggr logger.Logger, pollPeriod time.Duration, service LedgerProvider) (*Provider, error) {
	if pollPeriod <= 0 {
		return nil, fmt.Errorf("height provider poll period must be positive, got %s", pollPeriod)
	}
	if service == nil {
		return nil, fmt.Errorf("height provider requires a non-nil ledger service")
	}
	b := &Provider{
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

func (b *Provider) start(_ context.Context) error {
	b.engine.Go(b.poll)
	return nil
}

func (b *Provider) close() error { return nil }

func (b *Provider) poll(ctx context.Context) {
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

func (b *Provider) pollLedger(ctx context.Context) {
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

// GetLatest returns the latest observed ledger sequence.
func (b *Provider) GetLatest() int64 { return b.get() }

// GetSafe returns the safe ledger sequence. Stellar ledgers are final on close, so
// this equals GetLatest.
func (b *Provider) GetSafe() int64 { return b.get() }

// GetFinalized returns the finalized ledger sequence. Stellar ledgers are final on
// close, so this equals GetLatest.
func (b *Provider) GetFinalized() int64 { return b.get() }

func (b *Provider) get() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.sequence
}
