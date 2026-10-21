package capability

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/hashicorp/go-plugin"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

func run(ctx context.Context, lggr logger.Logger, name string, cfg *config, newCapability func(do.Injector) (Capability, error)) error {
	defer func() { _ = lggr.Sync() }()

	// Deferred first so it runs last: the beholder client loop.Server owns has to outlive every
	// service that reports through it.
	var srv *loop.Server
	defer restoreBeholder()()
	defer func() {
		if srv != nil {
			srv.Stop()
		}
	}()

	var svcs []services.Service
	defer func() {
		if err := services.MultiCloser(svcs).Close(); err != nil {
			logger.Sugared(lggr).Errorw("failed to stop the services of this run", "err", err)
		}
	}()

	// Before the server, which takes the settings as its limits' getter.
	settings, err := startSettings(ctx, lggr, cfg.settings)
	if err != nil {
		return fmt.Errorf("failed to start settings: %w", err)
	}
	svcs = append(svcs, settings)

	srv, err = startLoopServer(name, cfg, settings.values)
	if err != nil {
		return fmt.Errorf("failed to start loop server: %w", err)
	}

	reg, err := startRegistry(ctx, lggr, cfg.capabilities, cfg.grpc, srv.GRPCOpts)
	if err != nil {
		return fmt.Errorf("failed to start registry: %w", err)
	}
	svcs = append(svcs, reg)

	capability, err := startCapability(ctx, lggr, newCapability, reg, settings.values)
	if err != nil {
		return err
	}
	svcs = append(svcs, capability)

	for _, s := range svcs {
		if err := srv.Register(s); err != nil {
			return fmt.Errorf("failed to register %s with the health checker: %w", s.Name(), err)
		}
	}

	if underPluginHost() {
		lggr.Info("Serving the empty LOOP: this process is supervised by a go-plugin host")
		plugin.Serve(&plugin.ServeConfig{
			HandshakeConfig: emptyHandshakeConfig(),
			Plugins:         map[string]plugin.Plugin{emptyPluginName: &emptyLoop{}},
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
	h := emptyHandshakeConfig()
	return os.Getenv(h.MagicCookieKey) == h.MagicCookieValue
}

// startCapability builds the capability from its constructor and makes it reachable: started, and
// served and announced by the registry.
func startCapability(ctx context.Context, lggr logger.Logger, ctor func(do.Injector) (Capability, error), reg *registryService, settings *loop.AtomicSettings) (Capability, error) {
	// The run supervises the services it provides, so the injector is never shut down: doing so
	// would close them a second time.
	i := do.New()
	do.ProvideValue(i, lggr)
	do.ProvideValue[core.CapabilitiesRegistry](i, reg)
	do.ProvideValue(i, newLimitsFactory(lggr, settings))

	c, err := ctor(i)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate capability: %w", err)
	}
	if c == nil {
		return nil, errors.New("failed to instantiate capability: the constructor returned neither a capability nor an error")
	}

	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start capability: %w", err)
	}

	if err := reg.Add(ctx, c); err != nil {
		// Not yet on the caller's list, so the deferred close would never reach it.
		_ = c.Close()
		return nil, err
	}

	return c, nil
}
