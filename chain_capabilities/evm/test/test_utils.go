package test

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/metering"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
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

func ValidateMetering(t *testing.T, metadata capabilities.ResponseMetadata, value string) {
	require.Len(t, metadata.Metering, 1)
	meteringNodeDetail := metadata.Metering[0]
	require.Equal(t, metering.SpendUnit, meteringNodeDetail.SpendUnit)
	require.Equal(t, value, meteringNodeDetail.SpendValue)
	require.Empty(t, meteringNodeDetail.Peer2PeerID, "Peer2PeerID should be empty as it will be assigned by the engine")
}
