package capability

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	registryclient "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/client"
	common "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

type Dependencies struct {
	LimitsFactory      limits.Factory
	CRESettings        core.SettingsBroadcaster
	CapabilityRegistry core.CapabilitiesRegistry

	// CapabilityDonID is the on-chain DON ID of the capability DON this plugin
	// process was spawned for, resolved authoritatively by the host before
	// Initialise is called. Plugins should use this as the source of truth for
	// their own DON identity (e.g. when emitting events that need to carry the
	// *sending* DON ID, distinct from the consumer workflow's DON ID).
	//
	// Zero means the host did not provide one — either a legacy core node that
	// pre-dates this field, or a boot path that has not yet been updated to
	// populate it. Plugins SHOULD fall back to resolving via the capability
	// registry in that case, but the fallback path cannot disambiguate when
	// the local node belongs to multiple DONs running the same capability.
	CapabilityDonID uint32

	lggr logger.Logger

	// settings backs CRESettings. Held as the concrete type because Run needs to
	// write to it - the reload endpoint's whole job - which the broadcaster
	// interface cannot express.
	settings *loop.AtomicSettings
	// settingsPath is the file the reload endpoint re-reads.
	settingsPath string
	// proxy is the node's registry, which capabilities hosted here are announced
	// to by address. nil when embedded: there is no node to announce to, and the
	// capabilities are resolved in-process as values.
	proxy core.AddressableRegistryBase
	// servers opens one gRPC server per capability. nil when embedded, which
	// serves nothing.
	servers *standalonegrpc.Factory
	// closers are the connections Get opened, torn down by Close.
	closers []func() error
}

// OCRConfigRegistry is where an OCR-based capability reads the configuration it
// runs under: the node's registry, which computes the config digest from the
// contract it read - something a capability is deliberately not told.
//
// Nil when this process has no node behind it, as an embedded run does not: an
// oracle then has no configuration to join, which is a clearer failure than one
// invented locally that no other member of the DON agrees with.
func (d Dependencies) OCRConfigRegistry() core.OCRConfigRegistry {
	registry, ok := d.proxy.(core.OCRConfigRegistry)
	if !ok {
		return nil
	}
	return registry
}

// Close releases whatever Get dialled. The bootstrapper closes resolved
// dependency values on shutdown, after the services built from them.
func (d Dependencies) Close() error {
	var errs []error
	for _, c := range d.closers {
		errs = append(errs, c())
	}
	return errors.Join(errs...)
}

// capabilityConfig is the configured form's settings, under capabilities.*.
type capabilityConfig struct {
	// ProxyURL is a grpc.NewClient target rather than a port, so the registry
	// proxy is not assumed to be a process on this machine. The node usually
	// runs it beside this one ("localhost:9000"), but a shared proxy, a
	// sidecar on another host or a DNS name behind several of them are all just
	// a different target - and none of them can be expressed as a port.
	ProxyURL        string `validate:"required" usage:"gRPC target of the node's capability registry proxy (e.g. localhost:9000, or dns:///registry.internal:9000), used to resolve capabilities this binary does not host"`
	CapabilityDonID uint32 `validate:"required" usage:"on-chain DON ID of the capability DON this process was spawned for"`
}

// Dependency returns the standalone.BootstrapDependency a capability binary
// resolves to get everything a capability needs from its host: the limits
// factory, the CRE settings behind it, and a capability registry.
//
// The three arrive together because they are one thing seen from three sides.
// Settings are what the node broadcasts, limits are those settings resolved per
// key, and the registry is how a capability reaches the ones it does not host.
// Resolving them separately would mean three dependencies agreeing on one
// settings file and one proxy address.
//
// servers is the gRPC factory this binary serves its capabilities with: one
// server per capability, since the registry addresses a capability by the address
// serving it. Taken as a dependency rather than built here so its settings are
// registered and documented like any other's.
func Dependency(lggr logger.Logger, servers common.BootstrapDependency[*standalonegrpc.Factory]) common.BootstrapDependency[Dependencies] {
	// Wrapped so the connections Get dials are made at most once however many
	// services resolve this.
	return common.OnceBootstrapper[Dependencies](&dependency{
		lggr:    lggr,
		servers: servers,
	})
}

type dependency struct {
	lggr    logger.Logger
	servers common.BootstrapDependency[*standalonegrpc.Factory]
	capabilityConfig
}

var _ common.BootstrapDependency[Dependencies] = (*dependency)(nil)

func (d *dependency) Namespace() string { return "capabilities" }

func (d *dependency) Config() any { return &d.capabilityConfig }

func (d *dependency) Dependencies() []common.BootstrapCommand {
	return []common.BootstrapCommand{d.servers}
}

// ForEmbedding returns the form with no proxy behind it: an embedded instance
// has no node to ask, so its registry holds only what this binary registers. See
// embedded.
//
// The gRPC factory is embedded rather than passed through, so what an embedded
// capability serves on is whatever the factory says instance i gets. Whether that
// is one factory for the process or one each is the factory's to decide, and
// deciding it here would be this dependency asserting something about another's
// internals.
func (d *dependency) ForEmbedding(i int) common.BootstrapDependency[Dependencies] {
	return &embedded{lggr: d.lggr, servers: d.servers.ForEmbedding(i), cfg: &embeddedConfig{CapabilityDonID: defaultEmbeddedDonID}}
}

func (d *dependency) Get(ctx context.Context, cc common.CommonConfig) (Dependencies, error) {
	settings, err := newSettings(d.lggr)
	if err != nil {
		return Dependencies{}, err
	}

	// grpc.NewClient does not connect here: the first RPC does. So a proxy that
	// is not up yet delays the first lookup rather than failing the whole boot,
	// which matters because the node starts this process and the two race. It
	// also means an unreachable target is reported where it is used rather than
	// here, so the error below is only ever a malformed one.
	conn, err := grpc.NewClient(d.ProxyURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return Dependencies{}, fmt.Errorf("failed to create registry proxy client for %s: %w", d.ProxyURL, err)
	}
	proxy := registryclient.New(d.lggr, conn)

	servers, err := d.servers.Get(ctx, cc)
	if err != nil {
		return Dependencies{}, fmt.Errorf("failed to get gRPC server factory: %w", err)
	}

	return Dependencies{
		LimitsFactory:      newLimitsFactory(d.lggr, settings),
		CRESettings:        settings,
		CapabilityRegistry: newOverlayRegistry(d.lggr, registry.NewBaseRegistry(d.lggr), proxy),
		CapabilityDonID:    d.CapabilityDonID,
		lggr:               d.lggr,
		settings:           settings,
		settingsPath:       SettingsPath(),
		proxy:              proxy,
		servers:            servers,
		closers:            []func() error{proxy.Close, conn.Close},
	}, nil
}

// newSettings builds the process's settings, seeded from the dumped file if the
// node has already written one.
func newSettings(lggr logger.Logger) (*loop.AtomicSettings, error) {
	s := &loop.AtomicSettings{Lggr: lggr}
	s.SetGetter(cresettings.DefaultGetter)
	if err := loadSettings(s, SettingsPath()); err != nil {
		return nil, err
	}
	return s, nil
}

// newLimitsFactory builds the limits factory over settings.
//
// AtomicSettings is a settings.Getter and not a settings.Registry, so the
// factory polls it rather than subscribing - which is why a reload only has to
// swap the getter for every limit in the process to follow it.
func newLimitsFactory(lggr logger.Logger, settings *loop.AtomicSettings) limits.Factory {
	return limits.Factory{
		Settings: settings,
		Meter:    beholder.GetMeter(),
		Logger:   logger.Named(lggr, "Limits"),
	}
}

// Run builds the services for a binary hosting caps: it serves the settings
// reload endpoint the node calls, and gives each capability a gRPC server and a
// registration once the process starts.
//
// A capability gets a server of its own rather than sharing one, because the
// registry addresses a capability by the address serving it and most of the RPCs
// reached through that address carry nothing to tell two capabilities apart. That
// is also how the LOOP transport does it - one grpc.Server per capability behind
// its own broker connection - so a binary hosting several is not a thing this
// gives up.
//
// The capabilities are returned alongside the registration service rather than
// wrapped by it, so the bootstrapper supervises each of them in its own right and
// their health is reported separately.
func Run(dependencies Dependencies, sc standalone.StandaloneConfig, caps ...Capability) ([]services.Service, error) {
	if dependencies.settings == nil {
		return nil, errors.New("dependencies were not built by this package's Dependency")
	}
	if sc.Mux == nil {
		return nil, errors.New("standalone config has no HTTP mux to serve the reload endpoint on")
	}

	// Registered during construction, which is when the bootstrapper expects
	// routes: it starts serving the mux only once every service has started.
	sc.Mux.HandleFunc(ReloadPath(), reloadHandler(sc.Logger, dependencies.settings, dependencies.settingsPath))

	svcs := make([]services.Service, 0, len(caps)+1)
	for _, c := range caps {
		svcs = append(svcs, c)
	}
	svcs = append(svcs, newRegistrar(sc.Logger, dependencies, caps))
	return svcs, nil
}

// reloadHandler re-reads the settings file and swaps it in. 200 means every limit
// in this process now resolves against the new settings; 500 means none of them
// do and the previous settings are still in force.
func reloadHandler(lggr logger.Logger, settings *loop.AtomicSettings, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := loadSettings(settings, path); err != nil {
			lggr.Errorw("Failed to reload settings", "err", err, "path", path)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		lggr.Infow("Reloaded settings", "path", path)
		fmt.Fprintln(w, "ok")
	}
}

// registrar makes each capability reachable, and takes it back out on shutdown.
//
// Reachable means three things, in order: the local registry holds it, so
// anything else in this process resolves it as a value; a gRPC server of its own
// is serving it; and the node's registry knows that server's address. Announcing
// last is deliberate - the announcement is what invites traffic, so nothing is
// announced until it can be served.
//
// It owns the servers it opens rather than returning them as services, because
// there is one per capability and how many there are is only known here: the
// capabilities are initialised before their types can be read, and the types are
// what decide which RPCs each server carries.
//
// It is a service rather than something Run does inline because all of it needs a
// context, and Run has none to give: the bootstrapper builds services first and
// starts them after.
type registrar struct {
	services.Service
	eng *services.Engine

	deps Dependencies
	caps []Capability

	// hosted is what start got as far as making reachable, so close undoes
	// exactly that - a start that failed part-way leaves the rest alone.
	hosted []hosted
}

// hosted is one capability that is being served, and the server serving it.
type hosted struct {
	id     string
	server *standalonegrpc.Server // nil when embedded, which serves nothing
}

func newRegistrar(lggr logger.Logger, deps Dependencies, caps []Capability) *registrar {
	r := &registrar{deps: deps, caps: caps}
	r.Service, r.eng = services.Config{
		Name:  "CapabilityRegistrar",
		Start: r.start,
		Close: r.close,
	}.NewServiceEngine(lggr)
	return r
}

func (r *registrar) start(ctx context.Context) error {
	for i, c := range r.caps {
		info, err := c.Info(ctx)
		if err != nil {
			return fmt.Errorf("failed to read capability %d info: %w", i, err)
		}
		if err := r.deps.CapabilityRegistry.Add(ctx, c); err != nil {
			return fmt.Errorf("failed to register capability %s: %w", info.ID, err)
		}
		r.hosted = append(r.hosted, hosted{id: info.ID})

		if err := r.serve(ctx, c, info); err != nil {
			return err
		}
		r.eng.Infow("Registered capability", "capabilityID", info.ID, "type", info.CapabilityType)
	}
	return nil
}

// serve binds c to a gRPC server of its own and, when there is a node to tell,
// announces that server's address to its registry.
//
// The server is opened either way. Serving is what makes the capability callable
// from outside this process, and that is worth having under `embed` too - an
// embedded instance's capabilities are reachable in-process as values, but the
// address is what anything else has to go through. Only the announcement needs a
// node, so only the announcement is conditional.
func (r *registrar) serve(ctx context.Context, c Capability, info capabilities.CapabilityInfo) error {
	server, err := r.deps.servers.New(ctx, logger.Named(r.eng, info.ID))
	if err != nil {
		return fmt.Errorf("failed to open a server for capability %s: %w", info.ID, err)
	}
	r.hosted[len(r.hosted)-1].server = server

	if err := registryclient.RegisterCapability(r.eng, server.Registrar(), c, info.CapabilityType); err != nil {
		return fmt.Errorf("failed to serve capability %s: %w", info.ID, err)
	}
	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("failed to start the server for capability %s: %w", info.ID, err)
	}

	if r.deps.proxy == nil {
		return nil
	}
	if err := r.deps.proxy.AddAt(ctx, info.ID, info.CapabilityType, server.Address()); err != nil {
		return fmt.Errorf("failed to announce capability %s at %s: %w", info.ID, server.Address(), err)
	}
	return nil
}

// close undoes what start did, in reverse: stop inviting traffic (the registry
// entry, local and announced, which overlayRegistry.Remove drops from both), then
// stop answering it (the server).
//
// Failures are logged rather than returned. The process is going away, and a
// stale entry in a registry that cannot reach it any more is not worth failing
// shutdown over - the registry fails to dial it and drops it.
func (r *registrar) close() error {
	ctx, cancel := r.eng.NewCtx()
	defer cancel()

	for i := len(r.hosted) - 1; i >= 0; i-- {
		h := r.hosted[i]
		if err := r.deps.CapabilityRegistry.Remove(ctx, h.id); err != nil {
			r.eng.Warnw("Failed to deregister capability", "capabilityID", h.id, "err", err)
		}
		if h.server != nil {
			r.eng.ErrorIfFn(h.server.Close, "Failed to stop the server for capability "+h.id)
		}
	}
	return nil
}
