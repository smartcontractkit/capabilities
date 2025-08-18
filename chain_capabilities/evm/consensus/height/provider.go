package height

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
)

type HeaderProvider interface {
	HeaderByNumber(ctx context.Context, request evm.HeaderByNumberRequest) (*evm.HeaderByNumberReply, error)
}

// Provider is a service that polls the latest, safe, and finalized blocks from the EVM chain.
// It ensures that the heights respect the following constraints:
// finalized <= safe <= latest
// if it receives safe > latest or finalized > safe, then it will bump the latest and safe to the higher value
type Provider struct {
	services.Service
	engine *services.Engine

	lggr       logger.SugaredLogger
	pollPeriod time.Duration
	EVMService HeaderProvider

	mutex          sync.RWMutex
	latestBlock    int64
	safeBlock      int64
	finalizedBlock int64
}

func NewProvider(lggr logger.Logger, pollPeriod time.Duration, evmService HeaderProvider) *Provider {
	b := &Provider{
		pollPeriod: pollPeriod,
		EVMService: evmService,
	}

	b.Service, b.engine = services.Config{
		Name: "HeightProvider",
		// should i read first and then poll ?
		Start: b.start,
		Close: b.close,
	}.NewServiceEngine(lggr)

	b.lggr = b.engine.SugaredLogger
	return b
}

func (b *Provider) start(_ context.Context) error {
	b.engine.Go(b.poll)
	return nil
}

func (b *Provider) close() error {
	return nil
}

func (b *Provider) poll(ctx context.Context) {
	ticker := time.NewTicker(b.pollPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.pollBlocks(ctx)
		}
	}
}

func (b *Provider) pollBlocks(ctx context.Context) {
	b.lggr.Debug("polling block")

	latestBlockReply, err := b.EVMService.HeaderByNumber(ctx, evm.HeaderByNumberRequest{Number: nil})
	if err != nil || latestBlockReply.Header == nil {
		b.lggr.Errorw("failed to get latest block", "error", err)
		return
	}
	b.lggr.Debugw("latest block", "blockHeight", latestBlockReply.Header.Number)
	safeBlockReply, err := b.EVMService.HeaderByNumber(ctx, evm.HeaderByNumberRequest{Number: big.NewInt(rpc.SafeBlockNumber.Int64())})
	if err != nil || safeBlockReply.Header == nil {
		b.lggr.Errorw("failed to get safe block", "error", err)
		return
	}
	b.lggr.Debugw("safe block", "blockHeight", safeBlockReply.Header.Number)
	finalizedBlockReply, err := b.EVMService.HeaderByNumber(ctx, evm.HeaderByNumberRequest{Number: big.NewInt(rpc.FinalizedBlockNumber.Int64())})
	if err != nil || finalizedBlockReply.Header == nil {
		b.lggr.Errorw("failed to get finalized block", "error", err)
		return
	}
	b.lggr.Debugw("finalized block", "blockHeight", finalizedBlockReply.Header.Number)

	b.mutex.Lock()
	defer b.mutex.Unlock()

	if latestBlockReply.Header.Number != nil {
		b.latestBlock = latestBlockReply.Header.Number.Int64()
	}
	if safeBlockReply.Header.Number != nil {
		b.safeBlock = safeBlockReply.Header.Number.Int64()
	}
	if finalizedBlockReply.Header.Number != nil {
		// for finalized, we should retain the max value
		b.finalizedBlock = max(b.finalizedBlock, finalizedBlockReply.Header.Number.Int64())
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

func (b *Provider) GetLatest() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.latestBlock
}

func (b *Provider) GetSafe() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.safeBlock
}

func (b *Provider) GetFinalized() int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.finalizedBlock
}
