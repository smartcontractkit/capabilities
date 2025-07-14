package blocksProvider

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
)

func TestBlocksProvider(t *testing.T) {
	lggr, _ := logger.TestObserved(t, zapcore.DebugLevel)
	evmSvc := evmmock.NewEVMService(t)

	t.Run("ascending heights", func(t *testing.T) {
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Finalized).Return(evm.Head{Number: big.NewInt(1)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Safe).Return(evm.Head{Number: big.NewInt(2)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Unconfirmed).Return(evm.Head{Number: big.NewInt(3)}, nil).Once()

		bp := NewBlocksProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		time.Sleep(2 * time.Second)

		latestBlock, safeBlock, finalizedBlock := bp.GetLatest(), bp.GetSafe(), bp.GetFinalized()

		assert.Equal(t, int64(3), latestBlock)
		assert.Equal(t, int64(2), safeBlock)
		assert.Equal(t, int64(1), finalizedBlock)
	})

	t.Run("equal heights", func(t *testing.T) {
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Finalized).Return(evm.Head{Number: big.NewInt(1)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Safe).Return(evm.Head{Number: big.NewInt(1)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Unconfirmed).Return(evm.Head{Number: big.NewInt(1)}, nil).Once()
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

	t.Run("descending heights", func(t *testing.T) {
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Finalized).Return(evm.Head{Number: big.NewInt(3)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Safe).Return(evm.Head{Number: big.NewInt(2)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Unconfirmed).Return(evm.Head{Number: big.NewInt(1)}, nil).Once()
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

	t.Run("latest < safe", func(t *testing.T) {
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Finalized).Return(evm.Head{Number: big.NewInt(1)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Safe).Return(evm.Head{Number: big.NewInt(3)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Unconfirmed).Return(evm.Head{Number: big.NewInt(2)}, nil).Once()
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

	t.Run("safe < finalized", func(t *testing.T) {
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Finalized).Return(evm.Head{Number: big.NewInt(2)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Safe).Return(evm.Head{Number: big.NewInt(1)}, nil).Once()
		evmSvc.On("HeaderByNumber", mock.Anything, mock.Anything, primitives.Unconfirmed).Return(evm.Head{Number: big.NewInt(3)}, nil).Once()
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
