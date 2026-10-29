package trigger

import (
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

func TestLegacyService_Initialise(t *testing.T) {
	cases := []struct {
		name          string
		config        string
		defaultClosed bool
		errString     string
	}{
		{name: "no config keeps default limit", config: "", errString: "[101]LimitExceeded: maximum fastest cron schedule is 30s"},
		{name: "config without limit keeps default limit", config: "{}", errString: "[101]LimitExceeded: maximum fastest cron schedule is 30s"},
		{name: "config with limit replaces default limit", config: `{"fastestScheduleIntervalSeconds":1}`, defaultClosed: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ts, err := NewLegacyService(logger.Nop(), clockwork.NewRealClock(), limits.Factory{})
			require.NoError(t, err)
			defaultLimiter := &closeCountingLimiter{TimeLimiter: ts.fastestScheduleInterval}
			ts.fastestScheduleInterval = defaultLimiter

			require.NoError(t, ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: tt.config}))
			require.Equal(t, tt.defaultClosed, defaultLimiter.closed == 1)
			t.Cleanup(func() { require.NoError(t, ts.Close()) })

			_, _, err = registerTriggerToCronTriggerService(t.Context(), t, server.NewCronServer(ts), everySecond, triggerID1)
			if tt.errString != "" {
				require.EqualError(t, err, tt.errString)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLegacyService_InitialiseInvalidConfig(t *testing.T) {
	ts, err := NewLegacyService(logger.Nop(), clockwork.NewFakeClock(), limits.Factory{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ts.Close()) })

	require.ErrorContains(t, ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: "{"}), "failed to unmarshal config")
}
