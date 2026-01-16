package test

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/metering"
)

func RandomBytes(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return b
}

type NopBeholderProcessor struct{}

func (NopBeholderProcessor) Process(_ context.Context, _ proto.Message, _ ...any) error { return nil }

func ValidateMetering(t *testing.T, metadata capabilities.ResponseMetadata, expectedValue string) {
	require.Len(t, metadata.Metering, 1)
	meteringNodeDetail := metadata.Metering[0]
	require.Equal(t, metering.ActionSpendUnit, meteringNodeDetail.SpendUnit)
	require.Equal(t, expectedValue, meteringNodeDetail.SpendValue)
	require.Empty(t, meteringNodeDetail.Peer2PeerID, "Peer2PeerID should be empty as it will be assigned by the engine")
}

func ValidateMeteringWriteReport(t *testing.T, metadata capabilities.ResponseMetadata, chainSelector int, expectedValue string) {
	require.Len(t, metadata.Metering, 1)
	meteringNodeDetail := metadata.Metering[0]
	require.Equal(t, fmt.Sprintf(metering.WriteReportSpendUnitFormat, chainSelector), meteringNodeDetail.SpendUnit)
	require.Equal(t, expectedValue, meteringNodeDetail.SpendValue)
	require.Empty(t, meteringNodeDetail.Peer2PeerID, "Peer2PeerID should be empty as it will be assigned by the engine")
}

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
