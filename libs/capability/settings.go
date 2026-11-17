package capability

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	settingsremote "github.com/smartcontractkit/chainlink-common/pkg/settings/remote"
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

	values *settings.Atomic
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
	host := settingsremote.NewSettingsClient(s.eng, s.conn)
	s.eng.Go(func(ctx context.Context) { settings.Follow(ctx, s.eng, host, s.values) })
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
func newSettings(lggr logger.Logger) *settings.Atomic {
	s := &settings.Atomic{Lggr: lggr}
	s.SetGetter(cresettings.DefaultGetter)
	return s
}

// newLimitsFactory builds the limits factory over settings.
//
// settings.Atomic is a settings.Getter rather than a settings.Registry, so the factory polls it
// rather than subscribing - which is why swapping the getter is enough to make every limit in the
// process follow an update.
//
// The meter is read here rather than captured earlier, so it has to be built after telemetry has
// installed itself: an instrument resolves its meter once, and one made against the noop client
// records nothing for the life of the process.
func newLimitsFactory(lggr logger.Logger, s *settings.Atomic) limits.Factory {
	return limits.Factory{
		Settings: s,
		Meter:    beholder.GetMeter(),
		Logger:   logger.Named(lggr, "Limits"),
	}
}
