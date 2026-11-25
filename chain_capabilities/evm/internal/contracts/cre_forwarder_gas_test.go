package contracts_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
)

func TestForwarderRecordedReceiverGas(t *testing.T) {
	overhead := contracts.ForwarderGasOverhead(0)

	t.Run("production case is not overestimated", func(t *testing.T) {
		// 5M gas limit; the forwarder recorded 4,831,510 onchain. The estimate must not
		// exceed the recorded value, otherwise a sufficiently funded attempt looks underfunded.
		got := contracts.ForwarderRecordedReceiverGas(5_000_000, overhead)
		require.LessOrEqual(t, got, uint64(4_831_510))
		require.Greater(t, got, uint64(4_800_000))
	})

	t.Run("matches the EIP-150 formula", func(t *testing.T) {
		preRoute := overhead - contracts.InternalGasRequirements
		for _, limit := range []uint64{200_000, 1_000_000, 5_000_000} {
			atRoute := limit - preRoute
			want := atRoute - atRoute/64 - contracts.InternalGasRequirements
			require.Equal(t, want, contracts.ForwarderRecordedReceiverGas(limit, overhead))
		}
	})

	t.Run("withheld gas grows with the gas limit", func(t *testing.T) {
		withheld := func(limit uint64) uint64 {
			return limit - contracts.ForwarderRecordedReceiverGas(limit, overhead)
		}
		require.Greater(t, withheld(5_000_000), withheld(1_000_000))
		require.Greater(t, withheld(1_000_000), withheld(200_000))
	})

	t.Run("never underflows", func(t *testing.T) {
		require.Zero(t, contracts.ForwarderRecordedReceiverGas(0, overhead))
		require.Zero(t, contracts.ForwarderRecordedReceiverGas(overhead, overhead))
		require.Zero(t, contracts.ForwarderRecordedReceiverGas(overhead-contracts.InternalGasRequirements+1, overhead))
		require.Zero(t, contracts.ForwarderRecordedReceiverGas(100, 0))
	})
}
