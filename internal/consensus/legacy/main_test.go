package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/libs/testutils"
)

func TestNewConsensus(t *testing.T) {
	lggr := logger.Test(t)
	s := &loop.Server{Logger: logger.Sugared(lggr), LimitsFactory: limits.Factory{Logger: lggr}}

	c, err := newConsensus(s, core.StandardCapabilitiesDependencies{
		Config:        `{"MaxRequestSizeBytes": 2}`,
		OracleFactory: testutils.NewOracleFactory(t, lggr),
	})
	require.NoError(t, err)
	servicetest.Run(t, c)

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, protos.ConsensusID, info.ID)
}

func TestNewConsensus_BadConfig(t *testing.T) {
	lggr := logger.Test(t)
	s := &loop.Server{Logger: logger.Sugared(lggr), LimitsFactory: limits.Factory{Logger: lggr}}

	_, err := newConsensus(s, core.StandardCapabilitiesDependencies{Config: "{"})
	require.ErrorContains(t, err, "failed to unmarshal config")
}
