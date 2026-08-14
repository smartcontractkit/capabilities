package capability

import (
	"context"
	"errors"
	"fmt"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// errNoProxy is what an embedded instance answers with when asked something only
// the node's registry knows. Embedding replaces the node, and there is no
// on-chain registry behind an embedded run: the DONs, nodes and capability
// configurations it would report do not exist.
var errNoProxy = errors.New("no registry proxy: an embedded instance only knows the capabilities this binary registered")

// overlayRegistry resolves capabilities this binary hosts before asking the
// node's registry for anything else.
//
// Local first is not an optimisation. A capability this process hosts is a value
// it already holds, so resolving it locally hands back the implementation rather
// than a gRPC client looping back into this same process - and it works before
// this binary has announced anything, which is what lets one capability here call
// another during startup.
//
// proxy is nil for an embedded instance, which has no node to ask: then this is
// the base registry and nothing more, and the metadata calls fail rather than
// answering with something invented.
type overlayRegistry struct {
	lggr  logger.Logger
	local core.CapabilitiesRegistryBase
	proxy core.AddressableCapabilitiesRegistry // nil when embedded
}

var _ core.CapabilitiesRegistry = (*overlayRegistry)(nil)

func newOverlayRegistry(lggr logger.Logger, local core.CapabilitiesRegistryBase, proxy core.AddressableCapabilitiesRegistry) *overlayRegistry {
	return &overlayRegistry{lggr: logger.Named(lggr, "OverlayRegistry"), local: local, proxy: proxy}
}

// Add registers c in this binary's own registry, by value.
//
// It is not forwarded to the node. Announcing a capability there means naming the
// address serving it, which the registrar does with AddAt once it has opened that
// capability's server - holding the value here is what makes it resolvable
// in-process, and the two are not the same registration.
func (r *overlayRegistry) Add(ctx context.Context, c capabilities.BaseCapability) error {
	return r.local.Add(ctx, c)
}

// Remove drops a capability from the local registry, and from the node's if it
// was announced there. A capability absent from the node's is not an error: this
// binary may never have announced it.
func (r *overlayRegistry) Remove(ctx context.Context, id string) error {
	err := r.local.Remove(ctx, id)
	if r.proxy == nil {
		return err
	}
	if perr := r.proxy.Remove(ctx, id); perr != nil {
		r.lggr.Debugw("capability not removed from the registry proxy", "capabilityID", id, "err", perr)
	}
	return err
}

func (r *overlayRegistry) Get(ctx context.Context, id string) (capabilities.BaseCapability, error) {
	return overlayGet(ctx, r, id, r.local.Get, proxyGet(r, func(p core.AddressableCapabilitiesRegistry) getFn[capabilities.BaseCapability] {
		return p.Get
	}))
}

func (r *overlayRegistry) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	return overlayGet(ctx, r, id, r.local.GetTrigger, proxyGet(r, func(p core.AddressableCapabilitiesRegistry) getFn[capabilities.TriggerCapability] {
		return p.GetTrigger
	}))
}

func (r *overlayRegistry) GetExecutable(ctx context.Context, id string) (capabilities.ExecutableCapability, error) {
	return overlayGet(ctx, r, id, r.local.GetExecutable, proxyGet(r, func(p core.AddressableCapabilitiesRegistry) getFn[capabilities.ExecutableCapability] {
		return p.GetExecutable
	}))
}

// List returns everything this binary hosts plus everything the node knows about,
// with local entries winning on ID so a capability hosted here comes back as the
// value rather than as a client dialling back into this process.
func (r *overlayRegistry) List(ctx context.Context) ([]capabilities.BaseCapability, error) {
	local, err := r.local.List(ctx)
	if err != nil {
		return nil, err
	}
	if r.proxy == nil {
		return local, nil
	}

	remote, err := r.proxy.List(ctx)
	if err != nil {
		// The local half is still usable and still correct; a node that cannot be
		// reached should not blank the capabilities this process holds.
		r.lggr.Warnw("failed to list capabilities from the registry proxy", "err", err)
		return local, nil
	}

	seen := make(map[string]bool, len(local))
	for _, c := range local {
		info, ierr := c.Info(ctx)
		if ierr != nil {
			return nil, fmt.Errorf("failed to read local capability info: %w", ierr)
		}
		seen[info.ID] = true
	}
	for _, c := range remote {
		info, ierr := c.Info(ctx)
		if ierr != nil {
			r.lggr.Warnw("skipping remote capability whose info could not be read", "err", ierr)
			continue
		}
		if !seen[info.ID] {
			local = append(local, c)
		}
	}
	return local, nil
}

// --- metadata: only the node's registry knows any of this ---

func (r *overlayRegistry) LocalNode(ctx context.Context) (capabilities.Node, error) {
	if r.proxy == nil {
		return capabilities.Node{}, errNoProxy
	}
	return r.proxy.LocalNode(ctx)
}

func (r *overlayRegistry) NodeByPeerID(ctx context.Context, peerID ragetypes.PeerID) (capabilities.Node, error) {
	if r.proxy == nil {
		return capabilities.Node{}, errNoProxy
	}
	return r.proxy.NodeByPeerID(ctx, peerID)
}

func (r *overlayRegistry) ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	if r.proxy == nil {
		return capabilities.CapabilityConfiguration{}, errNoProxy
	}
	return r.proxy.ConfigForCapability(ctx, capabilityID, donID)
}

func (r *overlayRegistry) DONsForCapability(ctx context.Context, capabilityID string) ([]capabilities.DONWithNodes, error) {
	if r.proxy == nil {
		return nil, errNoProxy
	}
	return r.proxy.DONsForCapability(ctx, capabilityID)
}

func (r *overlayRegistry) DONByID(ctx context.Context, donID uint32) (capabilities.DON, error) {
	if r.proxy == nil {
		return capabilities.DON{}, errNoProxy
	}
	return r.proxy.DONByID(ctx, donID)
}

// getFn is the shape the three resolving calls share on either registry.
type getFn[T capabilities.BaseCapability] func(ctx context.Context, id string) (T, error)

// proxyGet returns the node registry's resolving call, or nil when there is none,
// so overlayGet has one thing to check rather than two.
func proxyGet[T capabilities.BaseCapability](r *overlayRegistry, pick func(core.AddressableCapabilitiesRegistry) getFn[T]) getFn[T] {
	if r.proxy == nil {
		return nil
	}
	return pick(r.proxy)
}

// overlayGet tries local, then the node. A local miss is expected rather than
// exceptional - most capabilities live elsewhere - so its error is only reported
// if the node cannot resolve the ID either, where it is the more useful half of
// the answer: it says what this binary does host.
func overlayGet[T capabilities.BaseCapability](ctx context.Context, r *overlayRegistry, id string, local, remote getFn[T]) (T, error) {
	var zero T

	got, localErr := local(ctx, id)
	if localErr == nil {
		return got, nil
	}
	if remote == nil {
		return zero, localErr
	}

	got, remoteErr := remote(ctx, id)
	if remoteErr == nil {
		return got, nil
	}
	return zero, fmt.Errorf("capability %s not found locally (%w) or in the registry proxy: %w", id, localErr, remoteErr)
}
