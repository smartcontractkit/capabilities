package test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/metering"
)

// RandomBytes generates n random bytes for testing.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 256)
	}
	return b
}

// ValidateMetering validates that the response metadata has correct metering info.
func ValidateMetering(t *testing.T, metadata capabilities.ResponseMetadata, expectedValue string) {
	require.Len(t, metadata.Metering, 1)
	meteringNodeDetail := metadata.Metering[0]
	require.Equal(t, metering.ActionSpendUnit, meteringNodeDetail.SpendUnit)
	require.Equal(t, expectedValue, meteringNodeDetail.SpendValue)
	require.Empty(t, meteringNodeDetail.Peer2PeerID, "Peer2PeerID should be empty as it will be assigned by the engine")
}

// GetMetadataWithFunds returns metadata with sufficient funds for testing.
func GetMetadataWithFunds() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		SpendLimits: []capabilities.SpendLimit{
			{
				SpendType: metering.ActionSpendUnit,
				Limit:     "100_000",
			},
		},
	}
}

// GetMetadataWithNoFunds returns metadata with no funds for testing.
func GetMetadataWithNoFunds() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		SpendLimits: []capabilities.SpendLimit{
			{
				SpendType: metering.ActionSpendUnit,
				Limit:     "0",
			},
		},
	}
}
