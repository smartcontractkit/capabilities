package capability

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cenkalti/backoff/v5"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/timeutil"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
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
	host := newSettingsClient(s.eng, s.conn)
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
func followHostSettings(ctx context.Context, lggr logger.Logger, host core.SettingsBroadcaster, settings *loop.AtomicSettings) {
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
			if err := settings.Store(update); err != nil {
				lggr.Errorw("Failed to update settings", "err", err)
				continue
			}
			lggr.Infow("Updated settings", "hash", update.Hash)
		}
	}
}

// settingsClient is the host's settings service as a core.SettingsBroadcaster. Each subscription is
// one server stream, forwarded until it ends or ctx is done.
type settingsClient struct {
	lggr logger.Logger
	grpc pb.SettingsClient
}

func newSettingsClient(lggr logger.Logger, conn grpc.ClientConnInterface) *settingsClient {
	return &settingsClient{lggr: lggr, grpc: pb.NewSettingsClient(conn)}
}

func (c *settingsClient) Subscribe(ctx context.Context) (<-chan core.SettingsUpdate, error) {
	stream, err := c.grpc.Subscribe(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}
	ch := make(chan core.SettingsUpdate)
	go func() {
		defer close(ch)
		for {
			update, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && stream.Context().Err() == nil {
					c.lggr.Errorw("Failed to receive settings update", "err", err)
				}
				return
			}
			select {
			case <-stream.Context().Done():
				return
			case ch <- core.SettingsUpdate{Settings: update.Settings, Hash: update.Hash}:
			}
		}
	}()
	return ch, nil
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
