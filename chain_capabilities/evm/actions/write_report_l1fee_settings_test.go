package actions

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	settingsremote "github.com/smartcontractkit/chainlink-common/pkg/settings/remote"
)

// TestL1FeeFlagFollowsHostSettings reproduces the plugin-side condition: the
// capability's range limiter must resolve the host's pushed CRE settings
// override for FeatureEVMWriteReportL1FeeActivePeriod, with a ctx seeded the
// way WriteReport seeds it.
func TestL1FeeFlagFollowsHostSettings(t *testing.T) {
	host := &settings.Atomic{}
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	settingsremote.RegisterSettingsServer(srv, logger.Test(t), host)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)

	pluginSettings := &settings.Atomic{Lggr: logger.Test(t)}
	pluginSettings.SetGetter(cresettings.DefaultGetter)
	conn, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := settingsremote.NewSettingsClient(logger.Test(t), conn)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { settings.Follow(ctx, logger.Test(t), client, pluginSettings); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// The window the node pushes: active now.
	require.NoError(t, host.Store(settings.Snapshot{
		Settings: "[global.PerWorkflow]\nFeatureEVMWriteReportL1FeeActivePeriod = \"[0001-01-01 00:00:00 +0000 UTC,2101-01-01 00:00:00 +0000 UTC]\"\n",
		Hash:     "v24",
	}))

	f := limits.Factory{Settings: pluginSettings}
	limiter, err := limits.MakeRangeLimiter(f, cresettings.Default.PerWorkflow.FeatureEVMWriteReportL1FeeActivePeriod)
	require.NoError(t, err)

	md := &capabilities.RequestMetadata{WorkflowID: "wf", WorkflowOwner: "owner"}
	checkCtx := md.ContextWithCRE(t.Context())

	// The limiter must follow the host's settings: the override window covers now.
	require.Eventually(t, func() bool {
		return limiter.Check(checkCtx, config.NewTimestamp(time.Now())) == nil
	}, 10*time.Second, 10*time.Millisecond, "the pushed override should make the flag active")
}
