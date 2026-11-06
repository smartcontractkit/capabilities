package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
)

func TestNewConsensus(t *testing.T) {
	lggr := logger.Test(t)
	s := &loop.Server{Logger: logger.Sugared(lggr), LimitsFactory: limits.Factory{Logger: lggr}}
	factory := &fakeOracleFactory{}

	c, err := newConsensus(s, core.StandardCapabilitiesDependencies{
		Config:        `{"MaxRequestSizeBytes": 2, "RequestBatchSize": 5}`,
		OracleFactory: factory,
	})
	require.NoError(t, err)
	servicetest.Run(t, c)

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, protos.ConsensusID, info.ID)

	require.NotNil(t, factory.args.ReportingPluginFactoryService)
	require.NotNil(t, factory.args.ContractTransmitter)
	assert.True(t, factory.oracle.started)
}

func TestNewConsensus_BadConfig(t *testing.T) {
	lggr := logger.Test(t)
	s := &loop.Server{Logger: logger.Sugared(lggr), LimitsFactory: limits.Factory{Logger: lggr}}

	_, err := newConsensus(s, core.StandardCapabilitiesDependencies{Config: "{", OracleFactory: &fakeOracleFactory{}})
	require.ErrorContains(t, err, "failed to unmarshal config")
}

func TestNewConsensus_NoOracleFactory(t *testing.T) {
	lggr := logger.Test(t)
	s := &loop.Server{Logger: logger.Sugared(lggr), LimitsFactory: limits.Factory{Logger: lggr}}

	_, err := newConsensus(s, core.StandardCapabilitiesDependencies{})
	require.ErrorContains(t, err, "no oracle factory")
}

type fakeOracleFactory struct {
	args   core.OracleArgs
	oracle *fakeOracle
}

func (f *fakeOracleFactory) NewOracle(_ context.Context, args core.OracleArgs) (core.Oracle, error) {
	f.args = args
	f.oracle = &fakeOracle{}
	return f.oracle, nil
}

type fakeOracle struct {
	started bool
}

func (o *fakeOracle) Start(context.Context) error {
	o.started = true
	return nil
}

func (o *fakeOracle) Close(context.Context) error { return nil }
