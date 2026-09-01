// Package triggermeter isolates every metering concern of a trigger
// capability behind one type, so the trigger service body stays domain-only:
// it holds a single *TriggerMeter field and never branches on metering state.
//
// Trigger capabilities are snapshot-only producers. They hold no durable
// state (their registration stores are in-memory projections of workflow
// deployments), so they cannot anchor MeterRecord deltas on durable
// transitions the way the workflow syncer does: a node restart would re-emit
// every +1, and a delete that arrives while a capability-DON node is down is
// never observed at all. Billing for trigger resources therefore rides the
// ResourceManager's periodic MeterSnapshots exclusively — the level rises when
// a registration appears in the next snapshot and is released by its absence
// (the consumer drawdown contract counts any nonzero snapshot). There is no
// delta-emission surface on this type by design; do not add one.
package triggermeter

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"
)

// DefaultProduct is the metering product a trigger falls back to when neither
// the host (DeploymentIdentity.Product via loop.EnvConfig) nor the trigger's
// Config supplies one. Trigger capabilities are CRE products, so this is a
// meaningful default rather than resourcemanager.UnsetProduct.
const DefaultProduct = "cre"

// ErrDonIDNotInitialised is returned by DonID when the host has not (yet)
// delivered a non-zero CapabilityDonID (StandardCapabilitiesDependencies) to
// the trigger's Initialise. The consumer workflow's DON ID is a different
// dimension and is never substituted for it: callers either degrade
// explicitly at their own call site (event labels, CRE-4409) or proceed with
// the DON dimension absent (metering snapshots).
var ErrDonIDNotInitialised = errors.New("capability DON ID not initialised: waiting for Initialise to deliver StandardCapabilitiesDependencies.CapabilityDonID")

// Config carries the per-trigger metering identity constants. They are
// stamped once at construction (never settable later): Service is the stable
// service constant (it must not encode environment or zone), ResourcePool the
// service-level pool snapshots apply to, and ResourceType the billing unit on
// each Utilization. Product overrides DefaultProduct as the fallback used
// when the host supplies no product.
type Config struct {
	Service      string
	ResourcePool string
	ResourceType string
	Product      string
}

// SnapshotRow is the absolute level of one active resource, as reported by
// the trigger's own store at a snapshot tick. DonID optionally re-stamps the
// DON dimension for this row only (a resource whose DON was resolved at its
// registration, e.g. an EVM log filter); when empty the meter's base identity
// is used unchanged.
type SnapshotRow struct {
	Value      int64
	ResourceID string
	OrgID      string
	DonID      string
}

// SnapshotFunc supplies the trigger's current resource levels. It is invoked
// on the ResourceManager's snapshot tick and MUST be a cheap, non-blocking
// read-snapshot of in-memory state: no network, no disk, no lock held across
// I/O (the resourcemanager.Meterable contract). Org IDs must come from state
// captured at registration, never from a resolver call here.
type SnapshotFunc func(ctx context.Context) []SnapshotRow

// TriggerMeter owns a trigger capability's metering: the ResourceManager
// lifecycle, the base ResourceIdentity, org resolution for registration
// paths, and the resourcemanager.Meterable implementation. A nil
// *TriggerMeter and a TriggerMeter constructed with a nil ResourceManager are
// both safe, indistinguishable no-ops — "meter is off" is expressed by the
// value, not by guards at call sites. DonID remains served from the base
// identity even when metering is off (event labels need it regardless).
type TriggerMeter struct {
	lggr logger.Logger
	// rm may be nil: metering off. The meter never constructs a disabled
	// substitute; nil is the disabled state.
	rm          *resourcemanager.ResourceManager
	cfg         Config
	base        resourcemanager.ResourceIdentity
	orgResolver orgresolver.OrgResolver
	snapshot    SnapshotFunc

	mu sync.Mutex
	// started is set only when rm.Start succeeded; it gates Close so a
	// fail-open Start can never turn into a Close error on a never-started
	// ResourceManager.
	started    bool
	unregister func()
}

// New builds the meter for one trigger capability. rm may be nil (metering
// off; every method no-ops). dep carries the static deployment/node identity
// dimensions delivered via loop.EnvConfig; capabilityDonID is the
// host-injected DON ID from StandardCapabilitiesDependencies (0 = unknown:
// the DON dimension is left absent, never substituted). orgResolver may be
// nil (org IDs resolve to empty). snapshot supplies the trigger's levels on
// each snapshot tick and may be nil only if the trigger has nothing to
// snapshot.
func New(
	lggr logger.Logger,
	rm *resourcemanager.ResourceManager,
	dep resourcemanager.DeploymentIdentity,
	capabilityDonID uint32,
	cfg Config,
	orgResolver orgresolver.OrgResolver,
	snapshot SnapshotFunc,
) *TriggerMeter {
	if dep.Product == "" {
		if cfg.Product != "" {
			dep.Product = cfg.Product
		} else {
			dep.Product = DefaultProduct
		}
	}
	base := resourcemanager.NewBaseIdentity(dep, cfg.Service, cfg.ResourcePool)
	if capabilityDonID != 0 {
		base = base.WithDonID(strconv.FormatUint(uint64(capabilityDonID), 10))
	}
	return &TriggerMeter{
		lggr:        logger.Named(lggr, "TriggerMeter"),
		rm:          rm,
		cfg:         cfg,
		base:        base,
		orgResolver: orgResolver,
		snapshot:    snapshot,
	}
}

// Start starts the ResourceManager (it owns the snapshot tick) and registers
// this meter as its Meterable. Metering is fail-open: an RM start failure is
// logged and swallowed — snapshots are disabled but the trigger service must
// come up regardless — so Start never returns a non-nil error today.
func (tm *TriggerMeter) Start(ctx context.Context) error {
	if tm == nil || tm.rm == nil {
		return nil
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.started {
		return nil
	}
	if err := tm.rm.Start(ctx); err != nil {
		logger.Sugared(tm.lggr).Errorw("failed to start metering ResourceManager; snapshots disabled", "err", err)
		return nil
	}
	tm.started = true
	tm.unregister = tm.rm.Register(tm)
	return nil
}

// Close deregisters the Meterable FIRST (so no snapshot tick can observe a
// half-torn-down trigger store) and then closes the ResourceManager — but
// only when Start actually started it, so a fail-open Start never becomes a
// Close error. Close is idempotent.
func (tm *TriggerMeter) Close() error {
	if tm == nil || tm.rm == nil {
		return nil
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if !tm.started {
		return nil
	}
	if tm.unregister != nil {
		tm.unregister()
		tm.unregister = nil
	}
	tm.started = false
	return tm.rm.Close()
}

// Ready implements services.Service. The meter is fail-open by contract and
// therefore never blocks readiness.
func (tm *TriggerMeter) Ready() error { return nil }

// HealthReport implements services.Service, folding in the ResourceManager's
// report while it is running.
func (tm *TriggerMeter) HealthReport() map[string]error {
	if tm == nil {
		return map[string]error{}
	}
	report := map[string]error{tm.Name(): nil}
	tm.mu.Lock()
	started := tm.started
	tm.mu.Unlock()
	if started {
		for name, err := range tm.rm.HealthReport() {
			report[name] = err
		}
	}
	return report
}

// Name implements services.Service.
func (tm *TriggerMeter) Name() string {
	if tm == nil || tm.cfg.Service == "" {
		return "TriggerMeter"
	}
	return "TriggerMeter." + tm.cfg.Service
}

// DonID returns the capability DON identifier stamped on the base metering
// identity, or ErrDonIDNotInitialised when the host has not delivered one.
// It is served even when metering is off, because trigger event labels
// (CRE-4409) share the value; only a nil *TriggerMeter reports the error
// unconditionally.
func (tm *TriggerMeter) DonID() (string, error) {
	if tm == nil {
		return "", ErrDonIDNotInitialised
	}
	if id := tm.base.DonID(); id != "" {
		return id, nil
	}
	return "", ErrDonIDNotInitialised
}

// ResolveOrg resolves the org ID for a registration path, fail-open: the org
// already resolved upstream on the request context (contexts.CREValue) wins;
// otherwise one resolver call is made, with any error (or panic) logged and
// swallowed to an empty org. Callers store the result alongside the
// registration so snapshot paths never resolve — SnapshotFunc must be
// network-free.
func (tm *TriggerMeter) ResolveOrg(ctx context.Context, owner string) string {
	if tm == nil {
		return ""
	}
	if orgID := contexts.CREValue(ctx).Org; orgID != "" {
		return orgID
	}
	if tm.orgResolver == nil || owner == "" {
		return ""
	}
	var orgID string
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Sugared(tm.lggr).Warnw("panic while resolving org ID for metering", "owner", owner, "panic", r)
			}
		}()
		if resolved, err := tm.orgResolver.Get(ctx, owner); err != nil {
			logger.Sugared(tm.lggr).Warnw("failed to resolve org ID for metering", "owner", owner, "err", err)
		} else {
			orgID = resolved
		}
	}()
	return orgID
}

// GetUtilization implements resourcemanager.Meterable: it maps the trigger's
// SnapshotFunc rows to snapshot entries, one per active resource, stamping
// the base identity (with an optional per-row DON re-stamp) and the
// configured resource type. The ResourceManager derives snapshot event_ids
// itself.
func (tm *TriggerMeter) GetUtilization(ctx context.Context) []resourcemanager.SnapshotEntry {
	if tm == nil || tm.snapshot == nil || ctx.Err() != nil {
		return nil
	}
	rows := tm.snapshot(ctx)
	entries := make([]resourcemanager.SnapshotEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, resourcemanager.SnapshotEntry{
			// WithDonID is a no-op for an empty DonID, so rows without a
			// per-resource DON inherit the base identity unchanged.
			Identity: tm.base.WithDonID(row.DonID),
			Utilizations: []*meteringpb.Utilization{
				resourcemanager.NewUtilizationInt(row.Value, resourcemanager.UtilizationFields{
					ResourceType: tm.cfg.ResourceType,
					ResourceID:   row.ResourceID,
					OrgID:        row.OrgID,
				}),
			},
		})
	}
	return entries
}
