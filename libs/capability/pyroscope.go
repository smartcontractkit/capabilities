package capability

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop/pyroscope"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// newProfiler returns continuous profiling as a service, or nil when no pyroscope server is
// configured.
func newProfiler(lggr logger.Logger, appName string, cfg pyroscope.PyroscopeConfig, tracingEnabled bool) *profilerService {
	if cfg.PyroscopeServerAddress == "" {
		return nil
	}

	p := &profilerService{lggr: lggr, appName: appName, cfg: cfg, tracingEnabled: tracingEnabled}
	p.Service, _ = services.Config{
		Name:  "Profiler",
		Start: p.start,
		Close: p.close,
	}.NewServiceEngine(lggr)
	return p
}

func startProfiler(ctx context.Context, lggr logger.Logger, appName string, cfg pyroscope.PyroscopeConfig, tracingEnabled bool) (*profilerService, error) {
	profiler := newProfiler(lggr, appName, cfg, tracingEnabled)
	if profiler == nil {
		return nil, nil
	}

	return profiler, profiler.Start(ctx)
}

type profilerService struct {
	services.Service

	lggr    logger.Logger
	appName string
	cfg     pyroscope.PyroscopeConfig
	// tracingEnabled lets the profiler link traces to profiles, if cfg also asks for it.
	tracingEnabled bool

	// profiler is what start made and close stops. Written by one hook and read by the other, which
	// the state machine's lock orders: a service cannot be closed unless it started.
	profiler *pyroscope.Profiler
}

func (p *profilerService) start(context.Context) error {
	profiler, err := pyroscope.Start(p.lggr, p.cfg, p.appName, p.tracingEnabled)
	if err != nil {
		return err
	}

	p.profiler = profiler
	return nil
}

func (p *profilerService) close() error { return p.profiler.Stop() }
