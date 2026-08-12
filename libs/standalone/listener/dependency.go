// Package listener provides a standalone.BootstrapDependency that resolves the net.Listener a
// server serves on.
//
// A listening port is the one thing several instances of a binary in one process cannot share, and
// this is where that is dealt with: instance i listens one port along, so a process running an
// embedded DON needs no per-instance configuration and the service that serves on the listener
// never learns which instance it belongs to. That is the point of resolving it as a dependency
// rather than having a service open a port from an address it was configured with - a service
// aware of its own instance index would have to be written twice, once for the single-instance
// case and once for the embedded one.
package listener

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// Config is where the server listens.
type Config struct {
	ListenAddress string `usage:"address (host:port) this server listens on; instance i of an embed run listens on the port plus i"`
}

// Dependency returns a standalone.BootstrapDependency that resolves a listening TCP socket.
//
// namespace groups the setting, so a binary serving on more than one address gives each its own
// ("proxy" yields --proxy.listen-address). defaultAddress is what it listens on when nothing is
// configured; pass "" to require the address to be set.
func Dependency(namespace, defaultAddress string) standalone.BootstrapDependency[net.Listener] {
	// Wrap in OnceBootstrapper so the port is bound at most once even if several services resolve
	// this dependency - two listeners on one address is an error, and the second service would be
	// the one to fail.
	return standalone.OnceBootstrapper[net.Listener](&dependency{
		namespace: namespace,
		cfg:       &Config{ListenAddress: defaultAddress},
	})
}

type dependency struct {
	namespace string
	// cfg is shared with the embedded forms rather than copied into them: it is the instance the
	// flags are bound to, so a copy taken before decoding would never see the configured address.
	cfg *Config
	// portOffset is added to the configured port, set by ForEmbedding. Zero for a single instance,
	// which listens exactly where it is configured to.
	portOffset int
}

var _ standalone.BootstrapDependency[net.Listener] = (*dependency)(nil)

func (d *dependency) Namespace() string { return d.namespace }

func (d *dependency) Config() any { return d.cfg }

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{}
}

// ForEmbedding returns a form that listens i ports along from the configured address, since a port
// is the one thing two instances in one process cannot share. It reads the same settings as the
// receiver - an address, wherever it came from - so those stay one shared config instance,
// registered once and inherited by both commands.
func (d *dependency) ForEmbedding(i int) standalone.BootstrapDependency[net.Listener] {
	clone := *d
	clone.portOffset = i
	return &clone
}

func (d *dependency) Get(ctx context.Context, _ standalone.CommonConfig) (net.Listener, error) {
	if d.cfg.ListenAddress == "" {
		return nil, fmt.Errorf("--%s.listen-address is required", d.namespace)
	}

	address, err := standalone.OffsetPort(d.cfg.ListenAddress, d.portOffset)
	if err != nil {
		return nil, err
	}

	// Bound through a ListenConfig so the context governs it, as it does every other resolution.
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}
	return &listener{Listener: lis}, nil
}

// listener makes Close idempotent. The bootstrapper closes every resolved dependency on shutdown,
// and a server given a listener closes it as part of stopping, so whichever gets there second
// would otherwise report an already-closed socket as a shutdown failure.
type listener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *listener) Close() error {
	l.once.Do(func() {
		if err := l.Listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			l.err = err
		}
	})
	return l.err
}
