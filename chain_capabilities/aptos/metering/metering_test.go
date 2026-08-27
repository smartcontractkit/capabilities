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
		feeInOctas        uint64
		chainSelector     uint64
		expectedSpendUnit string
		expectedValue     string
	}{
		{
			name:              "Standard Aptos fee (500 octas = 0.000005 APT)",
			feeInOctas:        500,
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.000005",
		},
		{
			name:              "Large fee (1 APT)",
			feeInOctas:        100_000_000,
			chainSelector:     42,
			expectedSpendUnit: "GAS.42",
			expectedValue:     "1",
		},
		{
			name:              "Zero fee",
			feeInOctas:        0,
			chainSelector:     100,
			expectedSpendUnit: "GAS.100",
			expectedValue:     "0",
		},
		{
			name:              "Typical fee (gasUsed=500 * gasUnitPrice=100 = 50000 octas = 0.0005 APT)",
			feeInOctas:        50_000,
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.0005",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetResponseMetadataWriteReport(test.feeInOctas, test.chainSelector)
			require.Len(t, result.Metering, 1)
			assert.Equal(t, test.expectedSpendUnit, result.Metering[0].SpendUnit)
			assert.Equal(t, test.expectedValue, result.Metering[0].SpendValue)
			assert.Equal(t, strconv.FormatUint(test.feeInOctas, 10), result.Metering[0].SpendValueInGasUnits)
			assert.Empty(t, result.Metering[0].Peer2PeerID, "Peer2PeerID should be empty")
		})
	}
}

func TestAptosOctasToAPT(t *testing.T) {
	assert.Equal(t, "0.000005", new(big.Float).Quo(new(big.Float).SetUint64(500), big.NewFloat(1e8)).Text('f', -1))
	assert.Equal(t, "1", new(big.Float).Quo(new(big.Float).SetUint64(100_000_000), big.NewFloat(1e8)).Text('f', -1))
}
