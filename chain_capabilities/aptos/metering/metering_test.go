package metering

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetResponseMetadataWriteReport(t *testing.T) {
	tests := []struct {
		name              string
		fee               *big.Float
		chainSelector     uint64
		expectedSpendUnit string
		expectedValue     string
	}{
		{
			name:              "Standard Aptos fee (500 octas = 0.000005 APT)",
			fee:               new(big.Float).Quo(new(big.Float).SetUint64(500), big.NewFloat(1e8)),
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.000005",
		},
		{
			name:              "Large fee (1 APT)",
			fee:               new(big.Float).SetFloat64(1.0),
			chainSelector:     42,
			expectedSpendUnit: "GAS.42",
			expectedValue:     "1",
		},
		{
			name:              "Zero fee",
			fee:               new(big.Float).SetFloat64(0),
			chainSelector:     100,
			expectedSpendUnit: "GAS.100",
			expectedValue:     "0",
		},
		{
			name:              "Typical fee (gasUsed=500 * gasUnitPrice=100 = 50000 octas = 0.0005 APT)",
			fee:               new(big.Float).Quo(new(big.Float).SetUint64(50_000), big.NewFloat(1e8)),
			chainSelector:     1,
			expectedSpendUnit: "GAS.1",
			expectedValue:     "0.0005",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetResponseMetadataWriteReport(test.fee, test.chainSelector)
			require.Len(t, result.Metering, 1)
			assert.Equal(t, test.expectedSpendUnit, result.Metering[0].SpendUnit)
			assert.Equal(t, test.expectedValue, result.Metering[0].SpendValue)
			assert.Empty(t, result.Metering[0].Peer2PeerID, "Peer2PeerID should be empty")
		})
	}
}
