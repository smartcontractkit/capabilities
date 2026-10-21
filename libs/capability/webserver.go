package capability

// defaultHTTPPort is where the shared HTTP server listens unless it is told otherwise.
const defaultHTTPPort = 8080

// HTTPConfig is the shared HTTP server, which serves /metrics and /debug/pprof.
type HTTPConfig struct {
	// Port serves /metrics and /debug/pprof.
	Port uint16
}
