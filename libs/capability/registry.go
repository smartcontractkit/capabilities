package capability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// newRegistry builds the registry this process holds its capabilities in and resolves others
// through, as a service.
//
// Nothing is dialled here. grpc.NewClient connects on the first RPC rather than when it is made, so
// a proxy that is not up yet delays the first lookup instead of failing the whole boot - which
// matters because the node starts this process and the two race.
//
// It does not validate the target either: an empty string and an unknown scheme both build a client
// happily, so the error below is close to unreachable and a typo shows up as a failed lookup rather
// than at startup. What catches an unset URL is the `validate:"required"` tag on the setting.
//
// It is built rather than started for the same reason telemetry is: a capability is handed the
// registry when it is constructed, which is before the root that owns this can start anything.
func newRegistry(lggr logger.Logger, cfg capabilityConfig, servers *serverFactory) (*registryService, error) {
	r := &registryService{servers: servers}
	local := registry.Local(lggr)

	if cfg.ProxyURL == "" {
		// No node to ask. The local registry is the whole of this process's: it resolves what this
		// binary registered and nothing else, and the metadata calls - which DONs exist, what OCR
		// configuration a capability runs under - fail rather than answering with something
		// invented. That is what a binary run on its own wants, and it dials nothing.
		r.proxy = local
	} else {
		conn, err := grpc.NewClient(cfg.ProxyURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("failed to create registry proxy client for %s: %w", cfg.ProxyURL, err)
		}
		r.conn = conn

		// One registry, two questions: what capabilities there are, and what configuration an OCR
		// one runs under. Both are answered by whoever read the registry, over the one connection.
		//
		// The addresses map WithRemote takes is deliberately nil. Add on the result would announce
		// the address it found written there - a convention shared between whoever serves a
		// capability and whoever announces it. This service announces with AddAt instead, at the
		// point the address exists, so the map would be a write nobody reads.
		r.proxy = local.WithRemote(conn, nil)
	}

	r.Service, r.eng = services.Config{Name: "CapabilityRegistry", Close: r.close}.NewServiceEngine(lggr)
	return r, nil
}

// registryService is the registry this process holds its own capabilities in and resolves others
// through, the servers its own are reached on, and the connection to the node's registry - as one
// service, so that closing it undoes all three in order.
//
// There is no Start. A lazily-connected client has nothing to start, and the work with ordering in
// it - serving and announcing a capability - is Add's, called once the capability exists rather
// than when the root starts.
type registryService struct {
	services.Service
	eng *services.Engine

	// proxy resolves capabilities: what this binary hosts first, as the values it already holds,
	// and the rest from the node behind conn. Local first is not an optimisation: a capability
	// hosted here is a value this process already has, so resolving it locally hands back the
	// implementation rather than a gRPC client looping back into this same process.
	proxy registry.Registry

	// servers makes the gRPC server each Add serves its capability on - one per capability, since
	// a registry addresses a capability by the address serving it.
	servers *serverFactory

	// hosted is what Add made reachable, so close undoes exactly that, in reverse.
	hosted []hosted

	// conn is the connection to the node's registry proxy, which resolutions, announcements and
	// OCR questions all share. Nil when no proxy is configured, which is a binary with no node
	// behind it.
	conn *grpc.ClientConn
}

// hosted is one capability being served, and the server serving it.
type hosted struct {
	id     string
	server *server
}

// Add makes c reachable: served on a gRPC server of its own, held in this process's registry, and
// announced to the node's.
//
// The order is the registration protocol. Serving comes first because the announcement is what
// invites traffic, so nothing is announced until it can be answered - and the address is announced
// where it is made, rather than written somewhere an Add can find it later, so the two cannot
// disagree.
//
// It is called at build time rather than from Start: a capability that cannot be announced fails
// the run before it is nominally up, and the health checker - which starts inside root.start -
// only reports ready once this has run. The capability's own Start still runs later, with the
// rest of the root; the announcement invites nothing that can arrive in that window, since the
// node only learns the address at the end of it.
func (r *registryService) Add(ctx context.Context, c Capability) error {
	info, err := c.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to read the capability's info: %w", err)
	}

	server, err := r.servers.new(ctx, logger.Named(r.eng, info.ID))
	if err != nil {
		return fmt.Errorf("failed to open a server for capability %s: %w", info.ID, err)
	}
	// Undone here on any failure below rather than by close: a failure before root.start means
	// this service never started, and StopOnce would refuse to run close's undo at all.
	if err := registry.RegisterCapability(r.eng, server.registrar(), c, info.CapabilityType); err != nil {
		_ = server.Close()
		return fmt.Errorf("failed to serve capability %s: %w", info.ID, err)
	}
	if err := server.Start(ctx); err != nil {
		_ = server.Close()
		return fmt.Errorf("failed to start the server for capability %s: %w", info.ID, err)
	}

	if err := r.proxy.Add(ctx, c); err != nil {
		_ = server.Close()
		return fmt.Errorf("failed to register capability %s: %w", info.ID, err)
	}

	// Announced last: the announcement is what invites traffic, so nothing is announced until it
	// can be served. With no node behind this process there is nothing to announce to.
	if r.conn != nil {
		if err := r.proxy.AddAt(ctx, info.ID, info.CapabilityType, server.address()); err != nil {
			_ = r.proxy.Remove(ctx, info.ID)
			_ = server.Close()
			return fmt.Errorf("failed to announce capability %s: %w", info.ID, err)
		}
	}

	r.hosted = append(r.hosted, hosted{id: info.ID, server: server})
	r.eng.Infow("Registered capability", "capabilityID", info.ID, "type", info.CapabilityType, "address", server.address())
	return nil
}

// close undoes every Add in reverse: stop inviting traffic (Remove drops the local hold and tells
// the node's registry to drop the address), then stop answering it, then release the connection.
//
// One ordered function rather than concurrent closes, because the steps are each other's
// preconditions: the Remove RPC has to reach the node before the connection it travels on closes.
//
// Failure to deregister is logged rather than returned. The process is going away, and a stale
// entry in a registry that cannot reach it any more is not worth failing shutdown over - the
// registry fails to dial it and drops it.
func (r *registryService) close() error {
	// A context of its own rather than the engine's: Close closes the StopChan the engine's
	// derives from before it runs this hook, so the deregistration would be cancelled before it
	// left the process.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := len(r.hosted) - 1; i >= 0; i-- {
		h := r.hosted[i]
		if err := r.proxy.Remove(ctx, h.id); err != nil {
			r.eng.Warnw("Failed to deregister capability", "capabilityID", h.id, "err", err)
		}
		r.eng.ErrorIfFn(h.server.Close, "Failed to stop the server for capability "+h.id)
	}

	// The registry first: what it closes is what it dialled of its own accord - the capability
	// addresses it resolved - which are connections of their own rather than this one.
	err := r.proxy.Close()
	if r.conn == nil {
		return err
	}
	return errors.Join(err, r.conn.Close())
}
