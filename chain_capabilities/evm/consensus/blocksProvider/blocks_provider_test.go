package blocksProvider

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
)

type mockEVMService struct {
	mock.Mock
}

func (m *mockEVMService) HeaderByNumber(ctx context.Context, blockNumber *big.Int, confidenceLevel primitives.ConfidenceLevel) (evm.Head, error) {
	args := m.Called(ctx, blockNumber, confidenceLevel)
	return args.Get(0).(evm.Head), args.Error(1)
}

func setHeaderByNumber(evmSvc *mockEVMService, finalized, safe, latest int64) {
	evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Finalized).Return(evm.Head{Number: big.NewInt(finalized)}, nil)
	evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Safe).Return(evm.Head{Number: big.NewInt(safe)}, nil)
	evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Unconfirmed).Return(evm.Head{Number: big.NewInt(latest)}, nil)
}

func TestBlocksProvider(t *testing.T) {
	lggr, _ := logger.TestObserved(t, zapcore.DebugLevel)
	t.Run("latest > safe > finalized", func(t *testing.T) {
		evmSvc := new(mockEVMService)
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
		evmSvc := new(mockEVMService)
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
		evmSvc := new(mockEVMService)
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
		evmSvc := new(mockEVMService)
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
		evmSvc := new(mockEVMService)
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
