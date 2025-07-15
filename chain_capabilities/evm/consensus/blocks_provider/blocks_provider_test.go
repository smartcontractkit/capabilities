package blocks_provider

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/blocks_provider/mocks"
)

func setHeaderByNumber(evmSvc *mocks.HeaderByNumberProvider, finalized, safe, latest int64) {
	evmSvc.On("HeaderByNumber", mock.Anything, evm.HeaderByNumberRequest{Number: big.NewInt(rpc.FinalizedBlockNumber.Int64())}).
		Return(&evm.HeaderByNumberReply{Header: &evm.Header{Number: big.NewInt(finalized)}}, nil)
	evmSvc.On("HeaderByNumber", mock.Anything, evm.HeaderByNumberRequest{Number: big.NewInt(rpc.SafeBlockNumber.Int64())}).
		Return(&evm.HeaderByNumberReply{Header: &evm.Header{Number: big.NewInt(safe)}}, nil)
	evmSvc.On("HeaderByNumber", mock.Anything, evm.HeaderByNumberRequest{Number: nil}).
		Return(&evm.HeaderByNumberReply{Header: &evm.Header{Number: big.NewInt(latest)}}, nil)
}

func TestBlocksProvider(t *testing.T) {
	lggr, _ := logger.TestObserved(t, zapcore.DebugLevel)
	t.Run("latest > safe > finalized", func(t *testing.T) {
		evmSvc := mocks.NewHeaderByNumberProvider(t)
		setHeaderByNumber(evmSvc, 1, 2, 3)

		bp := NewBlocksProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		time.Sleep(2 * time.Second)

		latestBlock, safeBlock, finalizedBlock := bp.GetLatest(), bp.GetSafe(), bp.GetFinalized()

		assert.Equal(t, int64(1), finalizedBlock)
		assert.Equal(t, int64(2), safeBlock)
		assert.Equal(t, int64(3), latestBlock)
	})

	t.Run("latest = safe = finalized", func(t *testing.T) {
		evmSvc := mocks.NewHeaderByNumberProvider(t)
		setHeaderByNumber(evmSvc, 1, 1, 1)
		bp := NewBlocksProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		time.Sleep(2 * time.Second)

		latestBlock, safeBlock, finalizedBlock := bp.GetLatest(), bp.GetSafe(), bp.GetFinalized()

		assert.Equal(t, int64(1), latestBlock)
		assert.Equal(t, int64(1), safeBlock)
		assert.Equal(t, int64(1), finalizedBlock)
	})

	t.Run("latest < safe < finalized", func(t *testing.T) {
		evmSvc := mocks.NewHeaderByNumberProvider(t)
		setHeaderByNumber(evmSvc, 3, 2, 1)
		bp := NewBlocksProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		time.Sleep(2 * time.Second)

		latestBlock, safeBlock, finalizedBlock := bp.GetLatest(), bp.GetSafe(), bp.GetFinalized()

		assert.Equal(t, int64(3), latestBlock)
		assert.Equal(t, int64(3), safeBlock)
		assert.Equal(t, int64(3), finalizedBlock)
	})

	t.Run("latest < safe > finalized", func(t *testing.T) {
		evmSvc := mocks.NewHeaderByNumberProvider(t)
		setHeaderByNumber(evmSvc, 1, 3, 2)
		bp := NewBlocksProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		time.Sleep(2 * time.Second)

		latestBlock, safeBlock, finalizedBlock := bp.GetLatest(), bp.GetSafe(), bp.GetFinalized()

		assert.Equal(t, int64(3), latestBlock)
		assert.Equal(t, int64(3), safeBlock)
		assert.Equal(t, int64(1), finalizedBlock)
	})

	t.Run("safe < finalized < latest", func(t *testing.T) {
		evmSvc := mocks.NewHeaderByNumberProvider(t)
		setHeaderByNumber(evmSvc, 2, 1, 3)
		bp := NewBlocksProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		time.Sleep(2 * time.Second)

		latestBlock, safeBlock, finalizedBlock := bp.GetLatest(), bp.GetSafe(), bp.GetFinalized()

		assert.Equal(t, int64(3), latestBlock)
		assert.Equal(t, int64(2), safeBlock)
		assert.Equal(t, int64(2), finalizedBlock)
	})
}
