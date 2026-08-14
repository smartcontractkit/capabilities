package capability

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/smartcontractkit/capabilities/libs/standalone"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
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
	// closers are the connections Get opened, torn down by Close.
	closers []func() error
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
func Dependency(lggr logger.Logger) common.BootstrapDependency[Dependencies] {
	// Wrapped so the connections Get dials are made at most once however many
	// services resolve this.
	return common.OnceBootstrapper[Dependencies](&dependency{lggr: lggr})
}

type dependency struct {
	lggr logger.Logger
	capabilityConfig
}

var _ common.BootstrapDependency[Dependencies] = (*dependency)(nil)

func (d *dependency) Namespace() string { return "capabilities" }

func (d *dependency) Config() any { return &d.capabilityConfig }

func (d *dependency) Dependencies() []common.BootstrapCommand { return nil }

// ForEmbedding returns the form with no proxy behind it: an embedded instance
// has no node to ask, so its registry holds only what this binary registers. See
// embedded.
//
// The index is not used: what this dependency resolves belongs to the process
// rather than to an instance, so every instance shares the one form (and, through
// OnceBootstrapper, the one value it resolves to).
func (d *dependency) ForEmbedding(int) common.BootstrapDependency[Dependencies] {
	return &embedded{lggr: d.lggr, cfg: embeddedConfig{CapabilityDonID: defaultEmbeddedDonID}}
}

func (d *dependency) Get(ctx context.Context, _ common.CommonConfig) (Dependencies, error) {
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

	return Dependencies{
		LimitsFactory:      newLimitsFactory(d.lggr, settings),
		CRESettings:        settings,
		CapabilityRegistry: newOverlayRegistry(d.lggr, registry.NewBaseRegistry(d.lggr), proxy),
		CapabilityDonID:    d.CapabilityDonID,
		lggr:               d.lggr,
		settings:           settings,
		settingsPath:       SettingsPath(),
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

// Run builds the services for a binary hosting capabilities: it serves the
// settings reload endpoint the node calls, and registers each capability once
// the process starts.
//
// The capabilities are returned alongside the registration service rather than
// wrapped by it, so the bootstrapper supervises each of them in its own right
// and their health is reported separately.
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

// registrar initialises each capability and puts it in the registry, then takes
// them back out on shutdown.
//
// It is a service rather than something Run does inline because both halves need
// a context: Initialise and Add take one, and Run has none to give - the
// bootstrapper builds services first and starts them after.
type registrar struct {
	services.Service
	eng *services.Engine

	deps Dependencies
	caps []Capability
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
		if err := c.Initialise(ctx, &r.deps); err != nil {
			return fmt.Errorf("failed to initialise capability %d: %w", i, err)
		}
		info, err := c.Info(ctx)
		if err != nil {
			return fmt.Errorf("failed to read capability %d info: %w", i, err)
		}
		if err := r.deps.CapabilityRegistry.Add(ctx, c); err != nil {
			return fmt.Errorf("failed to register capability %s: %w", info.ID, err)
		}
		r.eng.Infow("Registered capability", "capabilityID", info.ID, "type", info.CapabilityType)
	}
	return nil
}

// close deregisters what start registered. Failures are logged rather than
// returned: the process is going away, and a capability left in a registry that
// is also going away is not worth failing shutdown over.
func (r *registrar) close() error {
	ctx, cancel := r.eng.NewCtx()
	defer cancel()
	for _, c := range r.caps {
		info, err := c.Info(ctx)
		if err != nil {
			r.eng.Warnw("Failed to read capability info while deregistering", "err", err)
			continue
		}
		if err := r.deps.CapabilityRegistry.Remove(ctx, info.ID); err != nil {
			r.eng.Warnw("Failed to deregister capability", "capabilityID", info.ID, "err", err)
		}
	}
	return nil
}
