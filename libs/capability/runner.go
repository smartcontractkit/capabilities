package capability

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/hashicorp/go-plugin"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func run(ctx context.Context, lggr logger.Logger, name string, cfg *config, newCapability constructor) error {
	defer func() { _ = lggr.Sync() }()

	root := &rootService{lggr: lggr, name: name}

	profiler := newProfiler(lggr, name, cfg.observability.pyroscope)
	if profiler != nil {
		root.add(profiler)
	}

	// Building telemetry installs it as the process's beholder client, and closing the service is
	// what puts the previous one back - so the root owns the undo along with everything else.
	//
	// KNOWN GAP, accepted: the swap happens here, but the undo only becomes available once the root
	// has started this service. StopOnce refuses to run a Close hook on a service that never
	// started, so a run that fails between this line and root.start below - the constructor, the
	// reg.Add that serves and announces, or the health checker - leaves the global pointing at a
	// client nothing will close. A failure inside root.start is fine: MultiStart closes whatever
	// already started, and this is ahead of the capability in the list.
	//
	// Left as is because Run exits the process straight after such a failure. It would matter to a
	// RunErr caller that carries on, and to an embed run, where one failed instance would strand
	// the global for the others - so it has to be closed before this runs more than once.
	telemetry, err := newTelemetry(lggr, &cfg.observability)
	if err != nil {
		return fmt.Errorf("failed to build telemetry: %w", err)
	}
	root.add(telemetry)

	// The registry a capability resolves other capabilities through, and is itself announced by.
	// Built before the constructor for the same reason telemetry is - a capability is handed this
	// when it is built - and closed by the root, which takes back the announcements, stops the
	// servers, and releases the connection to the node's proxy along with it.
	reg, err := newRegistry(lggr, cfg.capabilities, &serverFactory{
		host:      cfg.grpc.AdvertiseHost,
		startPort: cfg.grpc.StartPort,
	})
	if err != nil {
		return err
	}
	root.add(reg)

	// The settings a capability's limits resolve against, seeded from whatever the node has dumped.
	// After telemetry, since the factory reads the meter as it is built and one made against the
	// noop client would record nothing for the life of the process.
	settings, err := newSettings(lggr)
	if err != nil {
		return err
	}

	// The mux is the run's own, created here rather than inside the web server, so that things
	// built before it can register their routes - the reload endpoint first among them.
	mux := http.NewServeMux()

	// The node dumps new settings to the file and then hits this endpoint. Reloading swaps what
	// the AtomicSettings hold, and every limit resolves against the new payload on its next use -
	// no restart, and nothing has to be told.
	mux.HandleFunc(reloadPath(), reloadHandler(lggr, settings, settingsPath()))

	capability, err := newCapability.call(Dependencies{
		Logger:             lggr,
		CapabilityRegistry: reg.proxy,
		LimitsFactory:      newLimitsFactory(lggr, settings),
	})
	if err != nil {
		return err
	}
	root.add(capability)

	// Make the capability reachable now that it exists: served on a gRPC server of its own, held
	// in the local registry, and announced to the node's. Here rather than in a service's Start,
	// so that a capability that cannot be announced fails the run before it is nominally up - and
	// the health checker, started inside root.start, only reports ready once this has happened.
	if err := reg.Add(ctx, capability); err != nil {
		return err
	}

	// The debug UI, when asked for. After the capability is registered, because the page calls it
	// through the registry rather than around it.
	if cfg.capabilities.HTTPDebug {
		if err := mountDebugUI(ctx, lggr, mux, reg.proxy, capability); err != nil {
			return err
		}
	}

	health, err := newHealthChecker(lggr, beholder.GetClient(), root.reporters())
	if err != nil {
		return err
	}
	root.add(health)

	ws := newWebServer(lggr, cfg.observability.http, mux, health.checker)
	root.add(ws)

	supervised, err := root.start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start the services of this run: %w", err)
	}
	defer func() {
		if err := supervised.Close(); err != nil {
			logger.Sugared(lggr).Errorw("Failed to stop the services of this run", "err", err)
		}
	}()

	if underPluginHost() {
		lggr.Info("Serving the empty LOOP: this process is supervised by a go-plugin host")
		plugin.Serve(&plugin.ServeConfig{
			HandshakeConfig: loop.EmptyHandshakeConfig(),
			Plugins:         map[string]plugin.Plugin{loop.PluginEmptyName: &loop.EmptyLoop{}},
			GRPCServer:      plugin.DefaultGRPCServer,
		})
		return nil
	}

	<-ctx.Done()
	lggr.Info("Shutting down")
	return nil
}

// underPluginHost reports whether this process was launched by a go-plugin host, detected via the
// empty plugin's handshake magic cookie.
//
// The check is necessary rather than defensive: go-plugin's Serve refuses to run - and exits the
// process - when the cookie is absent, so a standalone binary that called it would die on startup.
func underPluginHost() bool {
	h := loop.EmptyHandshakeConfig()
	return os.Getenv(h.MagicCookieKey) == h.MagicCookieValue
}
