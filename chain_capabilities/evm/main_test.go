package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	libsocr "github.com/smartcontractkit/capabilities/libs/ocr"
)

func testLimitsFactory(t *testing.T) limits.Factory {
	t.Helper()
	g, err := settings.NewJSONGetter([]byte(`{}`))
	require.NoError(t, err)
	return limits.Factory{Settings: g}
}

// testConfig is a configuration that runs: the two required settings filled in,
// and the defaults everywhere else.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default
	cfg.CREForwarderAddress = testutils.NewAddress().String()
	cfg.ReceiverGasMinimum = 1000
	cfg.LogTriggerPollInterval = 60 * time.Second
	// Staggering needs a DON to place this node in, and there is no registry here.
	cfg.IsLocal = true
	cfg.DeltaStage = time.Second
	return cfg
}

// testDependencies are what the host would resolve, with the chain mocked and the
// oracle stubbed: what is under test is the capability being assembled and
// started, not libocr agreeing with itself.
func testDependencies(t *testing.T) Dependencies {
	t.Helper()
	evmSvc := evmmock.NewEVMService(t)
	evmSvc.On("GetFiltersNames", mock.Anything).Maybe().Return([]string{}, nil)

	return Dependencies{
		EVMService:    evmSvc,
		ChainInfo:     types.ChainInfo{FamilyName: "evm", ChainID: "1337"},
		EventStore:    capabilities.NewMemEventStore(),
		LimitsFactory: testLimitsFactory(t),
		NewOracle:     func(libsocr.OracleArgs) (Oracle, error) { return stubOracle{}, nil },
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	// The capability is built here but not started: starting it joins a protocol and
	// polls a chain, which is what the integration tests are for.
	t.Run("builds", func(t *testing.T) {
		_, err := New(logger.Test(t), testConfig(t), testDependencies(t))
		require.NoError(t, err)
	})

	t.Run("builds with the trigger's buffers configured", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.LogTriggerSendChannelBufferSize = 100
		cfg.LogTriggerLimitQueryLogSize = 10

		_, err := New(logger.Test(t), cfg, testDependencies(t))
		require.NoError(t, err)
	})

	t.Run("builds without staggered transmission", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.DeltaStage = 0

		_, err := New(logger.Test(t), cfg, testDependencies(t))
		require.NoError(t, err)
	})

	t.Run("the capability ID names the chain", func(t *testing.T) {
		c, err := New(logger.Test(t), testConfig(t), testDependencies(t))
		require.NoError(t, err)

		// 1337 is the selector chain-selectors gives chain ID 1337, and a workflow asks
		// for a chain by that selector rather than for EVM in general.
		assert.NotZero(t, c.ChainSelector())
		assert.Contains(t, c.id, "ChainSelector:")
	})

	t.Run("a chain with no selector is refused", func(t *testing.T) {
		deps := testDependencies(t)
		deps.ChainInfo.ChainID = "424242424242"

		_, err := New(logger.Test(t), testConfig(t), deps)
		assert.ErrorContains(t, err, "no chain selector for chain ID 424242424242")
	})

	t.Run("a trigger buffer smaller than a query is refused", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.LogTriggerSendChannelBufferSize = 5
		cfg.LogTriggerLimitQueryLogSize = 10

		_, err := New(logger.Test(t), cfg, testDependencies(t))
		assert.ErrorContains(t, err, "logTriggerLimitQueryLogSize (10) must be less than logTriggerSendChannelBufferSize (5)")
	})
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	t.Run("a configuration that runs", func(t *testing.T) {
		require.NoError(t, testConfig(t).Validate())
	})

	t.Run("no forwarder address", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.CREForwarderAddress = ""
		assert.ErrorContains(t, cfg.Validate(), "is not an address")
	})

	t.Run("no receiver gas floor", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.ReceiverGasMinimum = 0
		assert.ErrorContains(t, cfg.Validate(), "--evm.receiver-gas-minimum must be greater than 0")
	})

	t.Run("a negative trigger poll interval", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.LogTriggerPollInterval = -1
		assert.ErrorContains(t, cfg.Validate(), "must not be negative")
	})
}

// stubOracle stands in for libocr, which needs a DON to agree with and a registry
// to read its configuration from - neither of which a unit test has.
type stubOracle struct{}

func (stubOracle) Start() error { return nil }

func (stubOracle) Close() error { return nil }
