package actions

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	capevm "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
)

// TestReceiptL1FeeDoesNotSurviveTheLoopWire proves why the EVM capability
// meters gas without the L1 data fee: the host's chainlink-evm parses the
// receipt's l1Fee, but the loop wire's Receipt message has no l1_fee field, so
// the capability's copy of the receipt loses it in transit. The gate
// (FeatureEVMWriteReportL1FeeActivePeriod) then adds a nil fee, and the
// metered value is gasUsed × effectiveGasPrice — short by exactly the l1Fee
// the customer paid.
func TestReceiptL1FeeDoesNotSurviveTheLoopWire(t *testing.T) {
	in := &evmtypes.Receipt{
		Status:            1,
		GasUsed:           93471,
		EffectiveGasPrice: big.NewInt(6_000_000),
		L1Fee:             big.NewInt(449), // what the customer paid on top, per the RPC receipt
		TxHash:            evmtypes.Hash{1},
		BlockHash:         evmtypes.Hash{2},
		BlockNumber:       big.NewInt(61297384),
		ContractAddress:   evmtypes.Address{3},
	}
	require.NotNil(t, in.L1Fee, "the host's receipt carries the L1 fee")

	pb, err := capevm.ConvertReceiptToProto(in)
	require.NoError(t, err)

	out, err := capevm.ConvertReceiptFromProto(pb)
	require.NoError(t, err)

	require.Nil(t, out.L1Fee, "the wire's Receipt message has no l1_fee field: the L1 fee cannot cross the loop boundary")
	require.Equal(t, in.GasUsed, out.GasUsed)
	require.Equal(t, in.EffectiveGasPrice, out.EffectiveGasPrice)

	// The fee the capability meters, with the gate active: gasUsed × effectiveGasPrice
	// plus the (nil) L1 fee — exactly the round value the reconciler sees.
	gasFee := new(big.Int).Mul(new(big.Int).SetUint64(out.GasUsed), out.EffectiveGasPrice)
	require.Equal(t, int64(560_826_000_000), gasFee.Int64())
	require.Equal(t, int64(560_826_000_449), gasFee.Int64()+in.L1Fee.Int64(),
		"the on-chain total the reconciler computes — the metered fee is short by exactly the L1 fee")
}
