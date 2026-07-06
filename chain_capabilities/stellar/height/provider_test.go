package height

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/height/mocks"
)

func ledgerResp(seq uint32) stellartypes.GetLatestLedgerResponse {
	return stellartypes.GetLatestLedgerResponse{Sequence: seq}
}

func TestProvider(t *testing.T) {
	lggr, _ := logger.TestObserved(t, zapcore.DebugLevel)

	t.Run("reports latest ledger sequence for all tags", func(t *testing.T) {
		svc := mocks.NewLedgerProvider(t)
		svc.EXPECT().GetLatestLedger(mock.Anything).Return(ledgerResp(42), nil)

		p, err := NewProvider(lggr, 50*time.Millisecond, svc)
		require.NoError(t, err)
		require.NoError(t, p.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, p.Close()) })

		// Stellar ledgers are final on close, so latest == safe == finalized.
		tests.AssertEventually(t, func() bool { return p.GetLatest() == 42 })
		require.Equal(t, int64(42), p.GetSafe())
		require.Equal(t, int64(42), p.GetFinalized())
	})

	t.Run("retains max sequence (monotonic, ignores a lagging RPC)", func(t *testing.T) {
		svc := mocks.NewLedgerProvider(t)
		var calls int
		svc.EXPECT().GetLatestLedger(mock.Anything).
			RunAndReturn(func(context.Context) (stellartypes.GetLatestLedgerResponse, error) {
				calls++
				if calls == 1 {
					return ledgerResp(100), nil
				}
				return ledgerResp(90), nil // lagging RPC reports a lower sequence
			})

		p, err := NewProvider(lggr, time.Hour, svc)
		require.NoError(t, err)

		// Drive the poll deterministically (no ticker/timing) via the in-package method.
		p.pollLedger(t.Context())
		require.Equal(t, int64(100), p.GetLatest())
		p.pollLedger(t.Context())
		require.Equal(t, int64(100), p.GetLatest(), "must not drop below the max observed sequence")
	})

	t.Run("service error leaves height unchanged", func(t *testing.T) {
		svc := mocks.NewLedgerProvider(t)
		svc.EXPECT().GetLatestLedger(mock.Anything).
			Return(stellartypes.GetLatestLedgerResponse{}, errors.New("node unavailable"))

		p, err := NewProvider(lggr, time.Hour, svc)
		require.NoError(t, err)

		p.pollLedger(t.Context())
		require.Equal(t, int64(0), p.GetLatest())
	})
}

func TestNewProvider_Validation(t *testing.T) {
	lggr, _ := logger.TestObserved(t, zapcore.DebugLevel)

	t.Run("rejects non-positive poll period", func(t *testing.T) {
		svc := mocks.NewLedgerProvider(t)
		_, err := NewProvider(lggr, 0, svc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "poll period must be positive")
	})

	t.Run("rejects nil service", func(t *testing.T) {
		_, err := NewProvider(lggr, time.Second, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "non-nil ledger service")
	})
}
