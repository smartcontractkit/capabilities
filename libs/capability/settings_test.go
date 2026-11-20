package capability

import (
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	settingsremote "github.com/smartcontractkit/chainlink-common/pkg/settings/remote"
)

// TestLimitsFactoryIsBuiltOverTheSettings covers the wiring the whole thing exists for: a limit made
// by this factory reads the settings this process holds, so an update reaches it.
func TestLimitsFactoryIsBuiltOverTheSettings(t *testing.T) {
	s := newSettings(logger.Test(t))

	f := newLimitsFactory(logger.Test(t), s)

	assert.Same(t, s, f.Settings, "the factory should poll the settings this process holds")
	assert.NotNil(t, f.Meter)
	assert.NotNil(t, f.Logger)
}

// TestSettingsServiceWithoutAURL covers a run the host pushes no settings to: it starts and stops
// cleanly, and every limit stays at its compiled-in default.
func TestSettingsServiceWithoutAURL(t *testing.T) {
	s, err := startSettings(t.Context(), logger.Test(t), SettingsConfig{}, newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)
	assert.Nil(t, s.conn)
	require.NoError(t, s.Close())
}

// TestSettingsURLFollowsTheHostEnvVar covers the host's contract: it picks the settings address at
// launch and exports it as CL_SETTINGS_URL, so the env var fills in where the config cannot.
func TestSettingsURLFollowsTheHostEnvVar(t *testing.T) {
	assert.Empty(t, settingsURL(SettingsConfig{}), "no config and no env var resolves to nothing")

	t.Setenv("CL_SETTINGS_URL", "localhost:9001")
	assert.Equal(t, "localhost:9001", settingsURL(SettingsConfig{}), "the env var is the fallback")

	assert.Equal(t, "localhost:9002", settingsURL(SettingsConfig{URL: "localhost:9002"}),
		"an explicit config URL wins over the env var")
}

// TestSettingsServiceFollowsTheHost covers --settings.url end to end, against the settings server the
// node runs: the host stores new settings, and a limit made from this process's factory resolves
// against them.
func TestSettingsServiceFollowsTheHost(t *testing.T) {
	host := &settings.Atomic{}
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	settingsremote.RegisterSettingsServer(srv, logger.Test(t), host)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)

	s, err := startSettings(t.Context(), logger.Test(t), SettingsConfig{URL: l.Addr().String()}, newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	// A global-scope setting, so no tenant is needed to resolve it. The compiled-in default is 0s.
	limit, err := newLimitsFactory(logger.Test(t), s.values).MakeTimeLimiter(cresettings.Default.TriggerRegistrationStatusUpdateTimeout)
	require.NoError(t, err)

	require.NoError(t, host.Store(settings.Snapshot{Settings: "[global]\nTriggerRegistrationStatusUpdateTimeout = \"15s\"\n", Hash: "a"}))

	require.Eventually(t, func() bool {
		got, err := limit.Limit(t.Context())
		return err == nil && got == 15*time.Second
	}, 10*time.Second, 5*time.Millisecond, "the limit should resolve against the host's settings")
}
