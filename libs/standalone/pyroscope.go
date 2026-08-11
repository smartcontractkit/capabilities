package standalone

import (
	"runtime/debug"

	"github.com/grafana/pyroscope-go"
)

// startProfiler starts continuous profiling to a Pyroscope server when one is
// configured, tagged with appName (typically the binary's cobra command name)
// plus build version/SHA. Returns a nil profiler and no error when unconfigured.
//
// One profiler covers the whole process, including every instance of an embed
// run: profiles are per-process by nature, and splitting them per instance would
// only sample the same goroutine scheduler several times.
func startProfiler(appName string, cfg PyroscopeConfig) (*pyroscope.Profiler, error) {
	if cfg.ServerAddress == "" {
		return nil, nil
	}

	var ver, sha string
	if bi, ok := debug.ReadBuildInfo(); ok {
		ver = bi.Main.Version
		sha = bi.Main.Sum
		if len(sha) > 7 {
			sha = sha[:7]
		}
	}

	return pyroscope.Start(pyroscope.Config{
		ApplicationName: appName,
		ServerAddress:   cfg.ServerAddress,
		AuthToken:       string(cfg.AuthToken),
		Tags: map[string]string{
			"version":     ver,
			"sha":         sha,
			"environment": cfg.Environment,
		},
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
		},
	})
}
