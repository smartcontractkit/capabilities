package capcommon

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
)

func TestAverageRequestTimeout(t *testing.T) {
	t.Parallel()

	const capID = "evm:ChainSelector:42@1.0.0"
	const donID = uint32(10)
	const fallback = 5 * time.Second
	lggr := logger.Test(t)

	// withTimeout returns a RemoteExecutableConfig with the given RequestTimeout.
	withTimeout := func(d time.Duration) *capabilities.RemoteExecutableConfig {
		return &capabilities.RemoteExecutableConfig{RequestTimeout: d}
	}

	// configWith builds a CapabilityConfiguration with one method config per
	// RemoteExecutableConfig, keyed by the given method names. Nil entries yield
	// method configs without a RemoteExecutableConfig.
	configWith := func(recs ...*capabilities.RemoteExecutableConfig) capabilities.CapabilityConfiguration {
		methodCfgs := make(map[string]capabilities.CapabilityMethodConfig, len(recs))
		for i, rec := range recs {
			methodCfgs[fmt.Sprintf("method-%d", i)] = capabilities.CapabilityMethodConfig{RemoteExecutableConfig: rec}
		}
		return capabilities.CapabilityConfiguration{CapabilityMethodConfig: methodCfgs}
	}

	// configWithMethods is like configWith but takes explicit method names.
	configWithMethods := func(methodCfgs map[string]capabilities.CapabilityMethodConfig) capabilities.CapabilityConfiguration {
		return capabilities.CapabilityConfiguration{CapabilityMethodConfig: methodCfgs}
	}

	t.Run("returns fallback when registry is nil", func(t *testing.T) {
		t.Parallel()

		got := MaxRequestTimeoutWithMultiplier(context.Background(), nil, capID, donID, fallback, lggr)
		require.Equal(t, fallback, got)
	})

	t.Run("returns max RequestTimeout across method configs", func(t *testing.T) {
		t.Parallel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).Return(configWith(
			withTimeout(1*time.Second),
			withTimeout(2*time.Second),
			withTimeout(3*time.Second),
		), nil)

		got := MaxRequestTimeoutWithMultiplier(context.Background(), reg, capID, donID, fallback, lggr)
		require.Equal(t, 3*time.Second, got)
	})

	t.Run("skips method configs without RemoteExecutableConfig or zero RequestTimeout", func(t *testing.T) {
		t.Parallel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).Return(configWith(
			nil,
			withTimeout(0),
			withTimeout(4*time.Second),
			withTimeout(8*time.Second),
		), nil)

		got := MaxRequestTimeoutWithMultiplier(context.Background(), reg, capID, donID, fallback, lggr)
		require.Equal(t, 8*time.Second, got)
	})

	t.Run("returns fallback when no method config has a RequestTimeout", func(t *testing.T) {
		t.Parallel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).Return(configWith(
			nil,
			withTimeout(0),
		), nil)

		got := MaxRequestTimeoutWithMultiplier(context.Background(), reg, capID, donID, fallback, lggr)
		require.Equal(t, fallback, got)
	})

	t.Run("returns fallback when config is empty", func(t *testing.T) {
		t.Parallel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).Return(configWith(), nil)

		got := MaxRequestTimeoutWithMultiplier(context.Background(), reg, capID, donID, fallback, lggr)
		require.Equal(t, fallback, got)
	})

	t.Run("excludes WriteReport and LogTrigger methods from the max", func(t *testing.T) {
		t.Parallel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).Return(configWithMethods(map[string]capabilities.CapabilityMethodConfig{
			"CallContract": {RemoteExecutableConfig: withTimeout(2 * time.Second)},
			"FilterLogs":   {RemoteExecutableConfig: withTimeout(4 * time.Second)},
			"WriteReport":  {RemoteExecutableConfig: withTimeout(100 * time.Second)},
			"LogTrigger":   {RemoteExecutableConfig: withTimeout(200 * time.Second)},
		}), nil)

		got := MaxRequestTimeoutWithMultiplier(context.Background(), reg, capID, donID, fallback, lggr)
		require.Equal(t, 4*time.Second, got)
	})

	t.Run("returns fallback when only WriteReport and LogTrigger have RequestTimeout", func(t *testing.T) {
		t.Parallel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).Return(configWithMethods(map[string]capabilities.CapabilityMethodConfig{
			"WriteReport": {RemoteExecutableConfig: withTimeout(100 * time.Second)},
			"LogTrigger":  {RemoteExecutableConfig: withTimeout(200 * time.Second)},
		}), nil)

		got := MaxRequestTimeoutWithMultiplier(context.Background(), reg, capID, donID, fallback, lggr)
		require.Equal(t, fallback, got)
	})

	t.Run("returns fallback when config fetch fails", func(t *testing.T) {
		t.Parallel()

		// Cancelled ctx makes WithPollingRetry give up after the first failure
		// instead of retrying for up to 60s.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).
			Return(capabilities.CapabilityConfiguration{}, errors.New("boom"))

		got := MaxRequestTimeoutWithMultiplier(ctx, reg, capID, donID, fallback, lggr)
		require.Equal(t, fallback, got)
	})

	t.Run("retries after a transient error and returns max", func(t *testing.T) {
		t.Parallel()

		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).
			Return(capabilities.CapabilityConfiguration{}, errors.New("metadataRegistry information not available")).Once()
		reg.EXPECT().ConfigForCapability(mock.Anything, capID, donID).Return(configWith(
			withTimeout(1*time.Second),
			withTimeout(3*time.Second),
		), nil)

		got := MaxRequestTimeoutWithMultiplier(context.Background(), reg, capID, donID, fallback, lggr)
		require.Equal(t, 3*time.Second, got)
	})
}
