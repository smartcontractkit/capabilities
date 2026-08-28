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
//
// Nil rather than a service that does nothing, so that a binary not profiling has nothing in its
// health report about profiling.
//
// appName is the binary's name, which profiles are tagged with along with the build version and
// SHA. One profiler covers the whole process: profiles are per-process by nature, and a second
// would only sample the same goroutine scheduler twice.
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

// profilerService is the pyroscope profiler, started and stopped with everything else this run
// supervises.
//
// Nothing happens until it starts, which is what makes it reversible without anything having to
// remember: a profiler that was never started has nothing running to stop.
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
