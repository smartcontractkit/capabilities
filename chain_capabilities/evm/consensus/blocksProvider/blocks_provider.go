package blocksprovider

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
)

type HeaderByNumberProvider interface {
	HeaderByNumber(ctx context.Context, blockNumber *big.Int, confidenceLevel primitives.ConfidenceLevel) (evm.Head, error)
}

// BlocksProvider is a service that polls the latest, safe, and finalized blocks from the EVM chain.
// It ensures that the heights respect the following constraints:
// finalized <= safe <= latest
// if it receives safe > latest or finalized > safe, then it will bump the latest and safe to the higher value
type BlocksProvider struct {
	services.Service
	engine *services.Engine

	lggr       logger.SugaredLogger
	pollPeriod time.Duration
	EVMService HeaderByNumberProvider

	mutex          sync.RWMutex
	latestBlock    int64
	safeBlock      int64
	finalizedBlock int64
}

func NewBlocksProvider(lggr logger.Logger, pollPeriod time.Duration, evmService HeaderByNumberProvider) *BlocksProvider {
	b := &BlocksProvider{
		pollPeriod: pollPeriod,
		EVMService: evmService,
	}

	b.Service, b.engine = services.Config{
		Name: "BlocksProvider",
		// should i read first and then poll ?
		Start: b.start,
		Close: b.close,
	}.NewServiceEngine(lggr)

	b.lggr = b.engine.SugaredLogger
	return b
}

func (b *BlocksProvider) start(_ context.Context) error {
	b.engine.Go(b.poll)
	return nil
}

func (b *BlocksProvider) close() error {
	return nil
}

func (b *BlocksProvider) poll(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.pollPeriod):
			b.pollBlocks(ctx)
		}
	}
}

func (b *BlocksProvider) pollBlocks(ctx context.Context) {
	b.lggr.Debug("polling block")

	latestBlock, err := b.EVMService.HeaderByNumber(ctx, nil, primitives.Unconfirmed)
	if err != nil {
		b.lggr.Error("failed to get latest block", "error", err)
		return
	}
	b.lggr.Debugw("latest block", "blockHeight", latestBlock.Number)
	safeBlock, err := b.EVMService.HeaderByNumber(ctx, nil, primitives.Safe)
	if err != nil {
		b.lggr.Error("failed to get safe block", "error", err)
		return
	}
	b.lggr.Debugw("safe block", "blockHeight", safeBlock.Number)
	finalizedBlock, err := b.EVMService.HeaderByNumber(ctx, nil, primitives.Finalized)
	if err != nil {
		b.lggr.Error("failed to get finalized block", "error", err)
		return
	}
	b.lggr.Debugw("finalized block", "blockHeight", finalizedBlock.Number)

	b.mutex.Lock()
	defer b.mutex.Unlock()

	if latestBlock.Number != nil {
		b.latestBlock = latestBlock.Number.Int64()
	}
	if safeBlock.Number != nil {
		b.safeBlock = safeBlock.Number.Int64()
	}
	if finalizedBlock.Number != nil {
		// for finalized, we should retain the max value
		b.finalizedBlock = max(b.finalizedBlock, finalizedBlock.Number.Int64())
	}

	// sanitation
	if b.finalizedBlock > b.safeBlock {
		b.lggr.Warnw("sanitizing: finalized block > safe block",
			"finalized", b.finalizedBlock, "safe", b.safeBlock)
		b.safeBlock = b.finalizedBlock
	}

	if b.safeBlock > b.latestBlock {
		b.lggr.Warnw("sanitizing: safe block > latest block",
			"safe", b.safeBlock, "latest", b.latestBlock)
		b.latestBlock = b.safeBlock
	}
}

func (b *BlocksProvider) GetLatest() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.latestBlock
}

func (b *BlocksProvider) GetSafe() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.safeBlock
}

func (b *BlocksProvider) GetFinalized() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.finalizedBlock
}
