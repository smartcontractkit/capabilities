package height

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/height/mocks"
)

func setHeaderByNumber(evmSvc *mocks.HeaderProvider, finalized, safe, latest int64) {
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
		evmSvc := mocks.NewHeaderProvider(t)
		setHeaderByNumber(evmSvc, 1, 2, 3)

		bp := NewProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		tests.AssertEventually(t, func() bool { return bp.GetFinalized() == 1 })
		tests.AssertEventually(t, func() bool { return bp.GetSafe() == 2 })
		tests.AssertEventually(t, func() bool { return bp.GetLatest() == 3 })
	})

	t.Run("latest = safe = finalized", func(t *testing.T) {
		evmSvc := mocks.NewHeaderProvider(t)
		setHeaderByNumber(evmSvc, 1, 1, 1)
		bp := NewProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		tests.AssertEventually(t, func() bool { return bp.GetFinalized() == 1 })
		tests.AssertEventually(t, func() bool { return bp.GetSafe() == 1 })
		tests.AssertEventually(t, func() bool { return bp.GetLatest() == 1 })
	})

	t.Run("latest < safe < finalized", func(t *testing.T) {
		evmSvc := mocks.NewHeaderProvider(t)
		setHeaderByNumber(evmSvc, 3, 2, 1)
		bp := NewProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		time.Sleep(2 * time.Second)

		tests.AssertEventually(t, func() bool { return bp.GetFinalized() == 3 })
		tests.AssertEventually(t, func() bool { return bp.GetSafe() == 3 })
		tests.AssertEventually(t, func() bool { return bp.GetLatest() == 3 })
	})

	t.Run("latest < safe > finalized", func(t *testing.T) {
		evmSvc := mocks.NewHeaderProvider(t)
		setHeaderByNumber(evmSvc, 1, 3, 2)
		bp := NewProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		tests.AssertEventually(t, func() bool { return bp.GetFinalized() == 1 })
		tests.AssertEventually(t, func() bool { return bp.GetSafe() == 3 })
		tests.AssertEventually(t, func() bool { return bp.GetLatest() == 3 })
	})

	t.Run("safe < finalized < latest", func(t *testing.T) {
		evmSvc := mocks.NewHeaderProvider(t)
		setHeaderByNumber(evmSvc, 2, 1, 3)
		bp := NewProvider(lggr, 1*time.Second, evmSvc)
		require.NoError(t, bp.Start(t.Context()))
		t.Cleanup(func() {
			require.NoError(t, bp.Close())
		})

		tests.AssertEventually(t, func() bool { return bp.GetFinalized() == 2 })
		tests.AssertEventually(t, func() bool { return bp.GetSafe() == 2 })
		tests.AssertEventually(t, func() bool { return bp.GetLatest() == 3 })
	})
}
