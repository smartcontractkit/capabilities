package capability

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/grafana/pyroscope-go"

	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// PyroscopeConfig configures continuous profiling. An empty ServerAddress leaves profiling off.
type PyroscopeConfig struct {
	ServerAddress string                    `usage:"pyroscope server address; profiling is disabled when unset"`
	AuthToken     commonconfig.SecretString `usage:"pyroscope auth token" flagdocs:"noexample"`
	Environment   string                    `usage:"environment tag attached to profiles"`
}

// newProfiler returns continuous profiling as a service, or nil when no pyroscope server is
// configured.
func newProfiler(lggr logger.Logger, appName string, cfg PyroscopeConfig) *profilerService {
	if cfg.ServerAddress == "" {
		return nil
	}

	p := &profilerService{appName: appName, cfg: cfg}
	p.Service, _ = services.Config{
		Name:  "Profiler",
		Start: p.start,
		Close: p.close,
	}.NewServiceEngine(lggr)
	return p
}

func startProfiler(ctx context.Context, lggr logger.Logger, appName string, cfg PyroscopeConfig) (*profilerService, error) {
	profiler := newProfiler(lggr, appName, cfg)
	if profiler == nil {
		return nil, nil
	}

	return profiler, profiler.Start(ctx)
}

type profilerService struct {
	services.Service

	appName string
	cfg     PyroscopeConfig

	// profiler is what start made and close stops. Written by one hook and read by the other, which
	// the state machine's lock orders: a service cannot be closed unless it started.
	profiler *pyroscope.Profiler
}

func (p *profilerService) start(context.Context) error {
	var ver, sha string
	if bi, ok := debug.ReadBuildInfo(); ok {
		ver = bi.Main.Version
		sha = bi.Main.Sum
		if len(sha) > 7 {
			sha = sha[:7]
		}
	}

	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: p.appName,
		ServerAddress:   p.cfg.ServerAddress,
		AuthToken:       string(p.cfg.AuthToken),
		Tags: map[string]string{
			"version":     ver,
			"sha":         sha,
			"environment": p.cfg.Environment,
		},
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to start profiler: %w", err)
	}

	p.profiler = profiler
	return nil
}

func (p *profilerService) close() error { return p.profiler.Stop() }
