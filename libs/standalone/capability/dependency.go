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
	"github.com/smartcontractkit/capabilities/libs/standalone/protohelpers/ui"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
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

	// OCRConfigRegistry is where an OCR-based capability reads the configuration it runs under.
	//
	// It is a field of its own rather than CapabilityRegistry seen from another side because a
	// capability that runs no oracle should not have to know the question exists. Where it comes from
	// is the registry either way: the node's for a configured run, and for an embedded one a
	// configuration computed over the run's own instances - see RegisterEmbeddedOCRConfig.
	OCRConfigRegistry core.OCRConfigRegistry

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
	// addresses is where the capabilities this binary hosts are served, by capability ID. The
	// registry announces a capability at the address it finds here, so the registrar fills an entry
	// in as it opens each server - see registrar.serve. Empty when embedded, which announces nothing.
	addresses map[string]string
	// servers opens one gRPC server per capability. nil when embedded, which
	// serves nothing.
	servers *standalonegrpc.Factory
	// closers are the connections Get opened, torn down by Close.
	closers []func() error

	// httpDebug says whether Run mounts the debug UI.
	httpDebug bool
	// index is this instance's number, 0 for a configured run and i for instance i
	// of an embed run. It names the instance on the fan-out page.
	index int
	// fleet is every instance's debug page, shared by pointer between the
	// configured dependency and each embedded form - the same way the embedded
	// config is - so the fan-out page can reach a sibling by calling into its
	// handler rather than over a socket. nil when the UI is off.
	fleet *ui.Fleet
	// hub holds the trigger subscriptions the debug page has registered, shared
	// the same way and for the same reason: a trigger registered across several
	// instances is one subscription with a column per instance, not one each.
	hub *ui.Hub
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

	// HTTPDebug serves the debug UI on the shared HTTP server. Off by default: it
	// invokes capabilities, so it is something a run opts into rather than
	// something a configured process exposes because it can.
	//
	// An embed run has it on regardless - see embedded.Get. Embedding is the local
	// shape of this binary, run to be poked at, and a flag to turn on the thing you
	// started it for is a flag nobody wants to remember.
	HTTPDebug bool `usage:"serve the capability debug UI on the shared HTTP server, under /debug/capabilities. Always on for an embed run"`
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
		lggr:           lggr,
		servers:        servers,
		embeddedConfig: &embeddedConfig{CapabilityDonID: defaultEmbeddedDonID},
		fleet:          &ui.Fleet{},
		hub:            ui.NewHub(),
	})
}

type dependency struct {
	lggr    logger.Logger
	servers common.BootstrapDependency[*standalonegrpc.Factory]
	capabilityConfig

	// embeddedConfig is the settings of every embedded form this produces, allocated here so that
	// all of them share it.
	//
	// Sharing is what makes those settings arrive at all: the form whose settings are registered on
	// the embed command is the one built to be asked for them, and the forms that go on to resolve
	// each instance are built later (see ForEmbedding). A config per form would leave the decoded
	// values in the first one and every instance reading the defaults.
	embeddedConfig *embeddedConfig

	// fleet is created once and shared with every embedded form, for the same reason
	// the config is: all of an embed run's instances register their debug page in
	// one list, so the fan-out page on any of them can reach the rest.
	fleet *ui.Fleet
	// hub is shared for the same reason, one level further on: a subscription is
	// registered on several instances and watched as one table, so the instances
	// have to be delivering into one place.
	hub *ui.Hub
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
// Every instance's form reads the one embeddedConfig this dependency holds, so all of them see the
// values the flags were bound to rather than a copy of the defaults.
//
// The gRPC factory is embedded rather than passed through, so what an embedded
// capability serves on is whatever the factory says instance i gets. Whether that
// is one factory for the process or one each is the factory's to decide, and
// deciding it here would be this dependency asserting something about another's
// internals.
func (d *dependency) ForEmbedding(i, instances int) common.BootstrapDependency[Dependencies] {
	return &embedded{
		lggr:      d.lggr,
		servers:   d.servers.ForEmbedding(i, instances),
		instances: instances,
		cfg:       d.embeddedConfig,
		fleet:     d.fleet,
		hub:       d.hub,
		index:     i,
	}
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

	// One registry, two questions: what capabilities there are, and what configuration an OCR one
	// runs under. Both are answered by whoever read the registry, over the one connection to it.
	addresses := map[string]string{}
	proxy := registry.Local(d.lggr).WithRemote(conn, addresses)

	servers, err := d.servers.Get(ctx, cc)
	if err != nil {
		return Dependencies{}, fmt.Errorf("failed to get gRPC server factory: %w", err)
	}

	return Dependencies{
		LimitsFactory:      newLimitsFactory(d.lggr, settings),
		CRESettings:        settings,
		CapabilityRegistry: proxy,
		OCRConfigRegistry:  proxy,
		CapabilityDonID:    d.CapabilityDonID,
		lggr:               d.lggr,
		settings:           settings,
		settingsPath:       SettingsPath(),
		addresses:          addresses,
		servers:            servers,
		closers:            []func() error{proxy.Close, conn.Close},
		httpDebug:          d.HTTPDebug,
		fleet:              d.fleet,
		hub:                d.hub,
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

	if dependencies.httpDebug {
		if err := mountDebugUI(sc, dependencies, caps); err != nil {
			return nil, err
		}
	}

	svcs := make([]services.Service, 0, len(caps)+1)
	for _, c := range caps {
		svcs = append(svcs, c)
	}
	svcs = append(svcs, newRegistrar(sc.Logger, dependencies, caps))
	return svcs, nil
}

// mountDebugUI serves the debug page for the capabilities this instance hosts.
//
// The page calls capabilities through the registry, as any caller would, so what
// it exercises is the path a workflow takes rather than a way around it. Every
// instance mounts its own, and each adds itself to the shared fleet, which is what
// lets the fan-out page on any of them reach the rest.
//
// A context is needed to read each capability's ID, and Run has none to give: the
// bootstrapper builds services before starting them. context.Background is right
// here because this only reads what the capability already knows - it does not
// start anything, and there is nothing for a cancelled boot to abandon.
func mountDebugUI(sc standalone.StandaloneConfig, dependencies Dependencies, caps []Capability) error {
	// Widened to what the UI asks for, which is less than a Capability: it only
	// reads what a capability is registered as and the service behind it.
	debuggable := make([]ui.Capability, 0, len(caps))
	for _, c := range caps {
		debuggable = append(debuggable, c)
	}

	server, err := ui.New(context.Background(), dependencies.CapabilityRegistry, debuggable...)
	if err != nil {
		return fmt.Errorf("failed to build the capability debug UI: %w", err)
	}

	if err := ui.Mount(ui.Options{
		Mux:    sc.Mux,
		Prefix: ui.DefaultPrefix,
		Server: server,
		Fleet:  dependencies.fleet,
		Hub:    dependencies.hub,
		Index:  dependencies.index,
		Title:  fmt.Sprintf("Capability debug (instance %d)", dependencies.index+1),
	}); err != nil {
		return fmt.Errorf("failed to mount the capability debug UI: %w", err)
	}

	sc.Logger.Infow("Serving the capability debug UI",
		"path", ui.DefaultPrefix+"/ui/", "fanout", ui.DefaultPrefix+"/request")
	return nil
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
		// Served first, registered second. Registering is what invites traffic - it holds the value
		// here and, when there is a registry behind this one, announces the address it is served at -
		// so nothing is registered until something can answer.
		if err := r.serve(ctx, c, info); err != nil {
			return err
		}
		if err := r.deps.CapabilityRegistry.Add(ctx, c); err != nil {
			return fmt.Errorf("failed to register capability %s: %w", info.ID, err)
		}
		r.eng.Infow("Registered capability", "capabilityID", info.ID, "type", info.CapabilityType)
	}
	return nil
}

// serve binds c to a gRPC server of its own and records where that server is, so that registering it
// announces the right address.
//
// The server is opened either way. Serving is what makes the capability callable from outside this
// process, and that is worth having under `embed` too - an embedded instance's capabilities are
// reachable in-process as values, but the address is what anything else has to go through. Whether
// the address is announced anywhere is the registry's business: an embedded run has nothing to
// announce to, and its map goes unread.
func (r *registrar) serve(ctx context.Context, c Capability, info capabilities.CapabilityInfo) error {
	server, err := r.deps.servers.New(ctx, logger.Named(r.eng, info.ID))
	if err != nil {
		return fmt.Errorf("failed to open a server for capability %s: %w", info.ID, err)
	}
	r.hosted = append(r.hosted, hosted{id: info.ID, server: server})

	if err := registry.RegisterCapability(r.eng, server.Registrar(), c, info.CapabilityType); err != nil {
		return fmt.Errorf("failed to serve capability %s: %w", info.ID, err)
	}
	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("failed to start the server for capability %s: %w", info.ID, err)
	}

	if r.deps.addresses != nil {
		r.deps.addresses[info.ID] = server.Address()
	}
	return nil
}

// close undoes what start did, in reverse: stop inviting traffic (the registry
// entry, local and announced, which Remove drops from both), then stop answering
// it (the server).
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
