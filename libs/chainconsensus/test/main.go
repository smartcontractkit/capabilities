package test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commontTypes "github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
)

func GetConsensusMetrics(t *testing.T) metrics.ConsensusMetrics {
	m, err := metrics.NewConsensusMetrics(commontTypes.ChainInfo{ChainID: "fake-chain-id"})
	require.NoError(t, err, "failed to create metrics")
	return m
}
