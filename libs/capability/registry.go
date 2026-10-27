package capability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/remote"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
)

func newRegistry(lggr logger.Logger, cfg CapabilitiesConfig, grpcCfg GRPCConfig, grpcOpts loop.GRPCOpts) (*registryService, error) {
	r := &registryService{local: registry.NewRegistry(lggr)}

	if cfg.ProxyURL != "" {
		dial := dialer(grpcOpts)
		conn, err := dial(cfg.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create registry proxy client for %s: %w", cfg.ProxyURL, err)
		}

		transport, err := remote.NewListenerTransport(remote.ListenerTransportParams{
			Lggr: lggr,
			Listen: func() (net.Listener, error) {
				return net.Listen("tcp", net.JoinHostPort(grpcCfg.AdvertiseHost, strconv.Itoa(int(0))))
			},
			Connect:       dial,
			ServerFactory: func() *grpc.Server { return grpcOpts.NewServer(nil) },
		})
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed to create listener transport: %w", err)
		}

		r.conn = conn
		r.fallback = remote.NewCapabilitiesRegistryClient(lggr, conn, transport)
	}

	r.Service, r.eng = services.Config{Name: "CapabilityRegistry", Close: r.close}.NewServiceEngine(lggr)
	return r, nil
}

// dialer makes gRPC clients with the interceptors in opts. opts has no transport credentials, so
// dialer adds insecure credentials.
func dialer(opts loop.GRPCOpts) func(target string) (*grpc.ClientConn, error) {
	return func(target string) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, append(slices.Clone(opts.DialOpts), grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	}
}

// startRegistry builds the registry and starts it.
func startRegistry(ctx context.Context, lggr logger.Logger, cfg CapabilitiesConfig, grpcCfg GRPCConfig, grpcOpts loop.GRPCOpts) (*registryService, error) {
	r, err := newRegistry(lggr, cfg, grpcCfg, grpcOpts)
	if err != nil {
		return nil, err
	}
	return r, r.Start(ctx)
}

var _ registry.CapabilitiesRegistry = (*registryService)(nil)

// registryService resolves capabilities from what this binary hosts first and the node's registry
// second, and keeps the two in lock-step on Add and Remove.
type registryService struct {
	services.Service
	eng *services.Engine

	// Local first is not an optimisation: a capability hosted here is a value this process already
	// has, so resolving it locally hands back the implementation rather than a gRPC client looping
	// back into this same process.
	local registry.CapabilitiesRegistry

	// fallback is the node's registry, which also serves and announces what Add is handed. Nil
	// when no proxy is configured, which is a binary with no node behind it.
	fallback registry.CapabilitiesRegistry
	conn     *grpc.ClientConn

	mu sync.Mutex
	// added is what Add registered, so close undoes exactly that, in reverse.
	added []string
}

// close undoes every Add in reverse, then releases the connection the deregistrations travel on.
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

	r.mu.Lock()
	added := slices.Clone(r.added)
	r.mu.Unlock()

	for _, id := range slices.Backward(added) {
		if err := r.Remove(ctx, id); err != nil {
			r.eng.Warnw("Failed to deregister capability", "capabilityID", id, "err", err)
		}
	}

	if r.conn == nil {
		return nil
	}
	return r.conn.Close()
}

func withFallback[T any](r *registryService, call func(registry.CapabilitiesRegistry) (T, error)) (T, error) {
	v, err := call(r.local)
	if err == nil || r.fallback == nil {
		return v, err
	}

	fv, ferr := call(r.fallback)
	if ferr != nil {
		var zero T
		return zero, errors.Join(err, ferr)
	}
	return fv, nil
}

func (r *registryService) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) (capabilities.TriggerCapability, error) {
		return reg.GetTrigger(ctx, id)
	})
}

func (r *registryService) Get(ctx context.Context, id string) (capabilities.BaseCapability, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) (capabilities.BaseCapability, error) {
		return reg.Get(ctx, id)
	})
}

func (r *registryService) GetExecutable(ctx context.Context, id string) (capabilities.ExecutableCapability, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) (capabilities.ExecutableCapability, error) {
		return reg.GetExecutable(ctx, id)
	})
}

func (r *registryService) List(ctx context.Context) ([]capabilities.BaseCapability, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) ([]capabilities.BaseCapability, error) {
		return reg.List(ctx)
	})
}

func (r *registryService) LocalNode(ctx context.Context) (capabilities.Node, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) (capabilities.Node, error) {
		return reg.LocalNode(ctx)
	})
}

func (r *registryService) NodeByPeerID(ctx context.Context, peerID p2ptypes.PeerID) (capabilities.Node, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) (capabilities.Node, error) {
		return reg.NodeByPeerID(ctx, peerID)
	})
}

func (r *registryService) ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) (capabilities.CapabilityConfiguration, error) {
		return reg.ConfigForCapability(ctx, capabilityID, donID)
	})
}

func (r *registryService) DONsForCapability(ctx context.Context, capabilityID string) ([]capabilities.DONWithNodes, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) ([]capabilities.DONWithNodes, error) {
		return reg.DONsForCapability(ctx, capabilityID)
	})
}

func (r *registryService) DONByID(ctx context.Context, donID uint32) (capabilities.DON, error) {
	return withFallback(r, func(reg registry.CapabilitiesRegistry) (capabilities.DON, error) {
		return reg.DONByID(ctx, donID)
	})
}

// Add rolls back the local registration if the fallback rejects it, so the two never disagree.
func (r *registryService) Add(ctx context.Context, c capabilities.BaseCapability) error {
	info, err := c.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to read the capability's info: %w", err)
	}

	if err := r.local.Add(ctx, c); err != nil {
		return err
	}
	if r.fallback != nil {
		if err := r.fallback.Add(ctx, c); err != nil {
			return errors.Join(err, r.local.Remove(ctx, info.ID))
		}
	}

	r.mu.Lock()
	r.added = append(r.added, info.ID)
	r.mu.Unlock()
	return nil
}

func (r *registryService) Remove(ctx context.Context, id string) error {
	r.mu.Lock()
	r.added = slices.DeleteFunc(r.added, func(a string) bool { return a == id })
	r.mu.Unlock()

	err := r.local.Remove(ctx, id)
	if r.fallback == nil {
		return err
	}
	return errors.Join(err, r.fallback.Remove(ctx, id))
}
