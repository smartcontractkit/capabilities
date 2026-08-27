package metering

import (
	"math/big"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetResponseMetadataWriteReport(t *testing.T) {
	tests := []struct {
		name              string
		feeInLamports     uint64
		chainSelector     uint64
		expectedSpendUnit string
		expectedValue     string
	}{
		{
			name:              "Standard Solana fee (5000 lamports)",
			feeInLamports:     5000,
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.000005",
		},
		{
			name:              "Large fee (1 SOL)",
			feeInLamports:     1_000_000_000,
			chainSelector:     42,
			expectedSpendUnit: "GAS.42",
			expectedValue:     "1",
		},
		{
			name:              "Zero fee",
			feeInLamports:     0,
			chainSelector:     100,
			expectedSpendUnit: "GAS.100",
			expectedValue:     "0",
		},
		{
			name:              "Sub-lamport precision fee",
			feeInLamports:     1,
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.000000001",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetResponseMetadataWriteReport(test.feeInLamports, test.chainSelector)
			require.Len(t, result.Metering, 1)
			assert.Equal(t, test.expectedSpendUnit, result.Metering[0].SpendUnit)
			assert.Equal(t, test.expectedValue, result.Metering[0].SpendValue)
			assert.Equal(t, strconv.FormatUint(test.feeInLamports, 10), result.Metering[0].SpendValueInGasUnits)
			assert.Empty(t, result.Metering[0].Peer2PeerID, "Peer2PeerID should be empty")
		})
	}
}

func TestSolanaLamportsToSol(t *testing.T) {
	assert.Equal(t, "0.000005", new(big.Float).Quo(new(big.Float).SetUint64(5000), big.NewFloat(1e9)).Text('f', -1))
	assert.Equal(t, "1", new(big.Float).Quo(new(big.Float).SetUint64(1_000_000_000), big.NewFloat(1e9)).Text('f', -1))
}
