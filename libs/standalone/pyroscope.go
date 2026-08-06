package standalone

import (
	"os"
	"runtime/debug"

	"github.com/grafana/pyroscope-go"
)

const (
	envPyroscopeServerAddress = "CL_PYROSCOPE_SERVER_ADDRESS"
	envPyroscopeAuthToken     = "CL_PYROSCOPE_AUTH_TOKEN"
	envPyroscopeEnvironment   = "CL_PYROSCOPE_ENVIRONMENT"
)

// startProfiler starts continuous profiling to a Pyroscope server when
// CL_PYROSCOPE_SERVER_ADDRESS is configured, tagged with appName (typically
// the binary's cobra command name) plus build version/SHA. Returns a nil
// profiler and no error when unconfigured.
func startProfiler(appName string) (*pyroscope.Profiler, error) {
	addr := os.Getenv(envPyroscopeServerAddress)
	if addr == "" {
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
		ServerAddress:   addr,
		AuthToken:       os.Getenv(envPyroscopeAuthToken),
		Tags: map[string]string{
			"version":     ver,
			"sha":         sha,
			"environment": os.Getenv(envPyroscopeEnvironment),
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
