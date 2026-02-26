package height

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
)

type HeadProvider interface {
	LatestHead(ctx context.Context) (types.Head, error)
}

// Provider polls Aptos latest ledger version and exposes it as chain heights for consensus.
// Aptos has single-shot finality, so latest/safe/finalized are treated the same.
type Provider struct {
	services.Service
	engine *services.Engine

	lggr         logger.SugaredLogger
	pollPeriod   time.Duration
	HeadProvider HeadProvider

	mutex         sync.RWMutex
	latestVersion int64
	safeVersion   int64
	finalizedVer  int64
}

func NewProvider(lggr logger.Logger, pollPeriod time.Duration, headProvider HeadProvider) *Provider {
	p := &Provider{
		pollPeriod:   pollPeriod,
		HeadProvider: headProvider,
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
	head, err := p.HeadProvider.LatestHead(ctx)
	if err != nil {
		p.lggr.Errorw("failed to get latest head", "error", err)
		return
	}

	parsed, err := strconv.ParseUint(head.Height, 10, 64)
	if err != nil {
		p.lggr.Errorw("failed to parse latest head height", "height", head.Height, "error", err)
		return
	}
	if parsed > uint64(^uint64(0)>>1) {
		p.lggr.Errorw("latest head height overflows int64", "height", parsed)
		return
	}

	latest := int64(parsed)
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.latestVersion = max(p.latestVersion, latest)
	p.safeVersion = max(p.safeVersion, p.latestVersion)
	p.finalizedVer = max(p.finalizedVer, p.safeVersion)
}

func (p *Provider) GetLatest() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.latestVersion
}

func (p *Provider) GetSafe() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.safeVersion
}

func (p *Provider) GetFinalized() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.finalizedVer
}

func (p *Provider) String() string {
	return fmt.Sprintf("latest=%d safe=%d finalized=%d", p.GetLatest(), p.GetSafe(), p.GetFinalized())
}
