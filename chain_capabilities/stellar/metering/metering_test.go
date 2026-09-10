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
		feeStroops        uint64
		chainSelector     uint64
		expectedSpendUnit string
		expectedValue     string
	}{
		{
			name:              "Standard Stellar fee (5000 stroops = 0.0005 XLM)",
			feeStroops:        5000,
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.0005",
		},
		{
			name:              "Large fee (1 XLM)",
			feeStroops:        10_000_000,
			chainSelector:     42,
			expectedSpendUnit: "GAS.42",
			expectedValue:     "1",
		},
		{
			name:              "Zero fee",
			feeStroops:        0,
			chainSelector:     100,
			expectedSpendUnit: "GAS.100",
			expectedValue:     "0",
		},
		{
			name:              "Minimum fee (1 stroop = 0.0000001 XLM)",
			feeStroops:        1,
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.0000001",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetResponseMetadataWriteReport(test.feeStroops, test.chainSelector)
			require.Len(t, result.Metering, 1)
			assert.Equal(t, test.expectedSpendUnit, result.Metering[0].SpendUnit)
			assert.Equal(t, test.expectedValue, result.Metering[0].SpendValue)
			assert.Equal(t, strconv.FormatUint(test.feeStroops, 10), result.Metering[0].SpendValueInGasUnits)
			assert.Empty(t, result.Metering[0].Peer2PeerID, "Peer2PeerID should be empty")
		})
	}
}

func TestStellarStroopsToXLM(t *testing.T) {
	assert.Equal(t, "0.0005", new(big.Float).Quo(new(big.Float).SetUint64(5000), big.NewFloat(1e7)).Text('f', -1))
	assert.Equal(t, "1", new(big.Float).Quo(new(big.Float).SetUint64(10_000_000), big.NewFloat(1e7)).Text('f', -1))
}
