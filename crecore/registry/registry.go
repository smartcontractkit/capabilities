package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/grpc"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	baseregistry "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/client"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Handle is a registered capability: its ID, which capability services it
// serves, and the gRPC target serving them.
//
// Unlike the go-plugin registry, a Handle holds no connection and owns no
// resources, so registering and resolving a capability allocates nothing that
// has to be reclaimed. Reachability of URL is the registrant's responsibility
// for as long as the capability stays registered.
type Handle struct {
	ID   string
	Type capabilities.CapabilityType
	URL  string
}

// Registry is the CapabilitiesRegistry this binary serves.
//
// It composes two independent sources:
//
//   - Handles, registered at runtime over AddHandle by the processes that host the capabilities.
//     AddHandle dials the handle's address itself and wraps it into a real capabilities.BaseCapability
//     (the same conversion registry/client does for its own callers), then adds that to local,
//     chainlink's ordinary in-process base registry. AddHandle is a drop-in replacement for the
//     in-process Add(BaseCapability) core uses without a proxy: a capability registered this way is
//     just as real and callable as one added directly, whether the caller asking for it is in this
//     process or another one reached the normal way, over the gRPC service Get/GetTrigger/GetExecutable
//     wrap.
//   - Metadata, read from whichever snapshot is current when a call arrives. Read-only from here;
//     whoever supplies the snapshots decides how they are refreshed.
//
// Registry also implements core.CapabilitiesRegistry directly (see the passthrough
// methods below), so a dispatcher in this same process can call it exactly like any
// other in-process registry: the Handle-addressed API above exists for the gRPC
// service instead, so the two never share a method name.
//
// Registry is safe for concurrent use.
type Registry struct {
	lggr logger.Logger

	// snapshot resolves the registry as it currently stands. It is a function rather than a stored
	// value because a snapshot is replaced wholesale on every sync, and because there is nothing to
	// answer with before the first one lands - which it reports as an error, so metadata calls fail
	// with a reason instead of with an empty registry.
	snapshot func() (*LocalRegistry, error)

	// local holds the real, callable value AddHandle resolves a Handle to. Anything in this process
	// that wants to call a capability - locally added, or reached only by dialing a Handle's address -
	// goes through this, not through handles below.
	local core.CapabilitiesRegistryBase

	// dialOpts are applied when dialing a Handle's address to resolve it into local.
	dialOpts []grpc.DialOption

	mu      sync.RWMutex
	handles map[string]Handle
	conns   map[string]*grpc.ClientConn // by URL; closed and dropped on Remove
}

// New returns a Registry answering metadata from whatever snapshot resolves at the time of the
// call - in this binary, the Syncer's.
func New(lggr logger.Logger, snapshot func() (*LocalRegistry, error), dialOpts ...grpc.DialOption) *Registry {
	return &Registry{
		lggr:     logger.Named(lggr, "CapabilitiesRegistry"),
		snapshot: snapshot,
		local:    baseregistry.NewBaseRegistry(lggr),
		dialOpts: dialOpts,
		handles:  map[string]Handle{},
		conns:    map[string]*grpc.ClientConn{},
	}
}

// Local is the real, in-process registry AddHandle resolves Handles into: whatever in this process
// wants to actually call a capability - rather than tell some other process where to find it -
// uses this.
func (r *Registry) Local() core.CapabilitiesRegistryBase { return r.local }

// AddHandle registers a capability served at url.
//
// Re-registration is expected rather than exceptional, so it is accepted in both
// forms:
//
//   - Same address as the existing entry: nothing to change, so this succeeds
//     without touching the registry. A capability that serves itself at a fixed
//     address re-announces that same address every time it restarts, and failing
//     the second registration would take the capability out of service even though
//     the entry already points exactly where it should.
//   - Different address: the host moved, so the entry is repointed. Requiring a
//     Remove first would lose the capability for the window between the two calls.
//
// This registry deliberately does not try to distinguish a restart from a second
// process claiming the same ID. It cannot: it holds an address, not a connection,
// so it has no way to tell whether the existing entry is still alive. Ownership of
// a capability ID is settled on chain, and a caller that wants the stricter rule
// has the liveness information locally — see how core gates replacement on
// connection state in chainlink-common's baseRegistry.
func (r *Registry) AddHandle(ctx context.Context, h Handle) error {
	if h.ID == "" {
		return errors.New("capability ID is required")
	}
	if h.URL == "" {
		return fmt.Errorf("callback URL is required to register capability %s", h.ID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if prev, ok := r.handles[h.ID]; ok && prev.URL == h.URL && prev.Type == h.Type {
		r.lggr.Debugw("capability re-registered at the same address; nothing to do",
			"capabilityID", h.ID, "url", h.URL)
		return nil
	}

	conn, err := grpc.NewClient(h.URL, r.dialOpts...)
	if err != nil {
		return fmt.Errorf("failed to dial capability %s at %s: %w", h.ID, h.URL, err)
	}
	wrapped, err := client.Wrap(r.lggr, conn, h.Type)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to wrap capability %s at %s: %w", h.ID, h.URL, err)
	}

	// The wrapped value cannot report connection state (it is a plain RPC client, not a
	// *grpc.ClientConn), so local's own liveness-gated replace never fires - drop the previous
	// entry explicitly instead so a moved capability is replaced rather than rejected.
	if prevConn, ok := r.conns[h.ID]; ok {
		_ = r.local.Remove(ctx, h.ID)
		_ = prevConn.Close()
	}
	if err := r.local.Add(ctx, wrapped); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to add capability %s to local registry: %w", h.ID, err)
	}
	r.conns[h.ID] = conn

	if prev, ok := r.handles[h.ID]; ok {
		r.lggr.Infow("re-registering capability at a new address",
			"capabilityID", h.ID, "previousURL", prev.URL, "url", h.URL)
	}
	r.handles[h.ID] = h
	r.lggr.Infow("capability registered", "capabilityID", h.ID, "type", h.Type, "url", h.URL)
	return nil
}

func (r *Registry) RemoveHandle(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if conn, ok := r.conns[id]; ok {
		_ = r.local.Remove(ctx, id)
		_ = conn.Close()
		delete(r.conns, id)
	}

	if _, ok := r.handles[id]; !ok {
		return fmt.Errorf("capability %s not found", id)
	}
	delete(r.handles, id)
	r.lggr.Infow("capability removed", "capabilityID", id)
	return nil
}

// GetHandle resolves a capability of any type.
func (r *Registry) GetHandle(_ context.Context, id string) (Handle, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h, ok := r.handles[id]
	if !ok {
		return Handle{}, fmt.Errorf("capability %s not found", id)
	}
	return h, nil
}

// GetTriggerHandle resolves a capability that must serve TriggerExecutable.
func (r *Registry) GetTriggerHandle(ctx context.Context, id string) (Handle, error) {
	h, err := r.GetHandle(ctx, id)
	if err != nil {
		return Handle{}, err
	}
	switch h.Type {
	case capabilities.CapabilityTypeTrigger, capabilities.CapabilityTypeCombined:
		return h, nil
	default:
		return Handle{}, fmt.Errorf("capability %s is a %s, not a trigger", id, h.Type)
	}
}

// GetExecutableHandle resolves a capability that must serve Executable.
func (r *Registry) GetExecutableHandle(ctx context.Context, id string) (Handle, error) {
	h, err := r.GetHandle(ctx, id)
	if err != nil {
		return Handle{}, err
	}
	switch h.Type {
	case capabilities.CapabilityTypeAction,
		capabilities.CapabilityTypeTarget,
		capabilities.CapabilityTypeConsensus,
		capabilities.CapabilityTypeCombined:
		return h, nil
	default:
		return Handle{}, fmt.Errorf("capability %s is a %s, not executable", id, h.Type)
	}
}

// ListHandles returns every registered capability, ordered by ID so callers and
// tests see a stable sequence.
func (r *Registry) ListHandles(_ context.Context) []Handle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Handle, 0, len(r.handles))
	for _, h := range r.handles {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// --- metadata, delegated to whichever snapshot is current ---
//
// Each call resolves the snapshot again rather than holding one, so a sync that lands between two
// calls is picked up without this ever serving a view it has outlived.

func (r *Registry) LocalNode(ctx context.Context) (capabilities.Node, error) {
	lr, err := r.snapshot()
	if err != nil {
		return capabilities.Node{}, err
	}
	return lr.LocalNode(ctx)
}

func (r *Registry) NodeByPeerID(ctx context.Context, peerID ragetypes.PeerID) (capabilities.Node, error) {
	lr, err := r.snapshot()
	if err != nil {
		return capabilities.Node{}, err
	}
	return lr.NodeByPeerID(ctx, peerID)
}

func (r *Registry) DONsForCapability(ctx context.Context, capabilityID string) ([]capabilities.DONWithNodes, error) {
	lr, err := r.snapshot()
	if err != nil {
		return nil, err
	}
	return lr.DONsForCapability(ctx, capabilityID)
}

func (r *Registry) DONByID(ctx context.Context, donID uint32) (capabilities.DON, error) {
	lr, err := r.snapshot()
	if err != nil {
		return capabilities.DON{}, err
	}
	return lr.DONByID(ctx, donID)
}

// RawConfigForCapability returns the undecoded capability config bytes; see
// DON.CapabilityConfigurations for why they are not decoded here.
func (r *Registry) RawConfigForCapability(ctx context.Context, capabilityID string, donID uint32) ([]byte, error) {
	lr, err := r.snapshot()
	if err != nil {
		return nil, err
	}
	return lr.RawConfigForCapability(ctx, capabilityID, donID)
}

// --- core.CapabilitiesRegistry, delegated to local / RawConfigForCapability ---
//
// These give the dispatcher in this same process a plain in-process registry to
// call, backed by the same local map and metadata a gRPC caller reaches through
// Handle/Server. Named the same as core.CapabilitiesRegistry's methods, distinct
// from the Handle-returning methods above, so one Registry answers both without a
// wrapping adapter type.
var _ core.CapabilitiesRegistry = (*Registry)(nil)

func (r *Registry) Add(ctx context.Context, c capabilities.BaseCapability) error {
	return r.local.Add(ctx, c)
}

func (r *Registry) Remove(ctx context.Context, id string) error {
	return r.local.Remove(ctx, id)
}

func (r *Registry) Get(ctx context.Context, id string) (capabilities.BaseCapability, error) {
	return r.local.Get(ctx, id)
}

func (r *Registry) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	return r.local.GetTrigger(ctx, id)
}

func (r *Registry) GetExecutable(ctx context.Context, id string) (capabilities.ExecutableCapability, error) {
	return r.local.GetExecutable(ctx, id)
}

func (r *Registry) List(ctx context.Context) ([]capabilities.BaseCapability, error) {
	return r.local.List(ctx)
}

// ConfigForCapability returns the decoded config, where the gRPC service's own ConfigForCapability
// RPC hands back the bytes for its caller to decode. Both end up in the same decoder.
func (r *Registry) ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	lr, err := r.snapshot()
	if err != nil {
		return capabilities.CapabilityConfiguration{}, err
	}
	return lr.ConfigForCapability(ctx, capabilityID, donID)
}
