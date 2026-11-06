package capability

import (
	"context"
	"fmt"

	"github.com/cenkalti/backoff/v5"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/remote"
	"github.com/smartcontractkit/chainlink-common/pkg/timeutil"
)

// SettingsConfig points this process at the host's CRE settings service.
type SettingsConfig struct {
	// URL is the gRPC target of the host's CRE settings service (e.g. localhost:9001), which pushes
	// settings updates to this process. Unset leaves every limit at its compiled-in default.
	URL string
}

// settingsService holds this process's CRE settings and, when the host serves them, keeps them
// following the host's updates for as long as it runs.
type settingsService struct {
	services.Service
	eng *services.Engine

	values *loop.AtomicSettings
	conn   *grpc.ClientConn
}

func newSettingsService(lggr logger.Logger, cfg SettingsConfig, grpcOpts loop.GRPCOpts) (*settingsService, error) {
	s := &settingsService{values: newSettings(lggr)}
	if cfg.URL != "" {
		conn, err := dialer(grpcOpts)(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to create settings client for %s: %w", cfg.URL, err)
		}
		s.conn = conn
	}
	s.Service, s.eng = services.Config{Name: "Settings", Start: s.start, Close: s.close}.NewServiceEngine(lggr)
	return s, nil
}

func startSettings(ctx context.Context, lggr logger.Logger, cfg SettingsConfig, grpcOpts loop.GRPCOpts) (*settingsService, error) {
	s, err := newSettingsService(lggr, cfg, grpcOpts)
	if err != nil {
		return nil, err
	}
	return s, s.Start(ctx)
}

func (s *settingsService) start(context.Context) error {
	if s.conn == nil {
		return nil
	}
	host := remote.NewSettingsClient(s.eng, s.conn)
	s.eng.Go(func(ctx context.Context) { followHostSettings(ctx, s.eng, host, s.values) })
	return nil
}

func (s *settingsService) close() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

// newSettings builds this process's CRE settings. Until the host pushes an update, every limit
// resolves to its compiled-in default.
func newSettings(lggr logger.Logger) *loop.AtomicSettings {
	s := &loop.AtomicSettings{Lggr: lggr}
	s.SetGetter(cresettings.DefaultGetter)
	return s
}

// followHostSettings applies every settings update the host broadcasts, resubscribing with backoff
// whenever the subscription cannot be made or ends.
func followHostSettings(ctx context.Context, lggr logger.Logger, host settings.Broadcaster, values *loop.AtomicSettings) {
	bo := backoff.NewExponentialBackOff()
	for {
		ch, err := host.Subscribe(ctx)
		if err != nil {
			lggr.Errorw("Failed to subscribe to settings updates", "err", err)
			if !timeutil.Sleep(ctx.Done(), bo.NextBackOff()) {
				return
			}
			continue
		}
		lggr.Info("Subscribed to settings updates")
		bo.Reset()
		for update := range ch {
			if err := values.Store(update); err != nil {
				lggr.Errorw("Failed to update settings", "err", err)
				continue
			}
			lggr.Infow("Updated settings", "hash", update.Hash)
		}
	}
}

// newLimitsFactory builds the limits factory over settings.
//
// AtomicSettings is a settings.Getter rather than a settings.Registry, so the factory polls it
// rather than subscribing - which is why swapping the getter is enough to make every limit in the
// process follow an update.
//
// The meter is read here rather than captured earlier, so it has to be built after telemetry has
// installed itself: an instrument resolves its meter once, and one made against the noop client
// records nothing for the life of the process.
func newLimitsFactory(lggr logger.Logger, settings *loop.AtomicSettings) limits.Factory {
	return limits.Factory{
		Settings: settings,
		Meter:    beholder.GetMeter(),
		Logger:   logger.Named(lggr, "Limits"),
	}
}
