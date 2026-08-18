package grpc

import (
	"context"
	"net"
	"strconv"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// namespace roots both configs, so every gRPC setting is a grpc.* one however
// many of these a binary takes. They use different keys, since two configs
// binding one key would leave the value read from whichever registered last.
const namespace = "grpc"

// defaultHost is where a server binds and is advertised unless told otherwise.
//
// localhost rather than every interface: a process reached only by the node that
// launched it should not be listening publicly. Set the host to whatever this
// process is reachable at (a container name, a service DNS name, or empty for
// every interface) when something off-box has to dial it.
const defaultHost = "localhost"

// Config is the configured server's address.
//
// Host is both what the server binds to and what it is advertised as: a service
// that hands its address to something else can only be reached where the server
// is actually listening, so keeping them one setting means the two cannot
// disagree.
type Config struct {
	Host string `usage:"host the gRPC server binds to and is advertised at; empty binds every interface"`
	Port uint16 `validate:"required" usage:"port the gRPC server listens on. Instance i of an embed run listens on this port plus i"`
}

// Dependency returns the process's gRPC server, bound to the configured address.
//
// lggr names the server in the logs, and the caller names it rather than this
// does: a binary running one server calls it what that server is for.
func Dependency(lggr logger.Logger) standalone.BootstrapDependency[*Server] {
	// Wrapped so the port is bound once however many services resolve this.
	return standalone.OnceBootstrapper[*Server](&dependency{lggr: lggr, cfg: Config{Host: defaultHost}})
}

type dependency struct {
	lggr  logger.Logger
	cfg   Config
	index int
}

var _ standalone.BootstrapDependency[*Server] = (*dependency)(nil)

func (d *dependency) Namespace() string { return namespace }

func (d *dependency) Config() any { return &d.cfg }

func (d *dependency) Dependencies() []standalone.BootstrapCommand { return nil }

// ForEmbedding gives instance i the configured port plus i, so instances sharing
// a process do not collide over it - the same rule the metrics/health server
// follows. The settings are otherwise the configured ones: an address is an
// address whether or not there are siblings.
func (d *dependency) ForEmbedding(i int) standalone.BootstrapDependency[*Server] {
	return standalone.OnceBootstrapper[*Server](&dependency{lggr: d.lggr, cfg: d.cfg, index: i})
}

func (d *dependency) Get(ctx context.Context, _ standalone.CommonConfig) (*Server, error) {
	port := d.cfg.Port + uint16(d.index)
	return newServer(ctx, d.lggr, net.JoinHostPort(d.cfg.Host, strconv.Itoa(int(port))))
}

// FactoryConfig configures the servers the factory makes: where they are
// reachable, and optionally where their ports start.
type FactoryConfig struct {
	AdvertiseHost string `usage:"host the gRPC servers this process opens are advertised at; empty binds every interface"`

	// StartPort is where the factory's ports begin. Zero, the default, means every
	// server asks the OS for a free port instead - which is what a process
	// announcing its own addresses wants, since nothing has to predict them.
	//
	// Set it when something outside the process does have to predict them: a
	// firewall rule, a port mapping, or an operator reading a log. The ports are
	// then consecutive from here, one per server, in the order they are opened.
	StartPort uint16 `usage:"first port the gRPC servers this process opens bind to, incrementing per server; 0 asks the OS for a free port for each of them"`
}

// Factory makes gRPC servers, one per thing that has to be told apart by address
// - see the package comment. Each call binds a port immediately, so the caller
// can announce the address before anything is serving on it.
//
// One factory hands out one run of ports, and it is shared rather than copied
// per embedded instance: instances live in one process and so compete for the
// same ports, and a counter each would have them all try to bind StartPort.
type Factory struct {
	host      string
	startPort uint16

	mu     sync.Mutex
	opened uint16 // servers made so far, which is the offset from startPort
}

// New returns a server bound to the next port on the configured host. lggr names
// it, and the caller names it after whatever it serves, so a process with several
// says which is which.
func (f *Factory) New(ctx context.Context, lggr logger.Logger) (*Server, error) {
	return newServer(ctx, lggr, net.JoinHostPort(f.host, strconv.Itoa(int(f.nextPort()))))
}

// nextPort is the port the next server binds.
//
// Zero is not a port but a request for any free one, so it is handed out as-is
// however many servers ask: incrementing it would turn "any port" into a
// deliberate 1, 2, 3, which are neither free nor wanted.
func (f *Factory) nextPort() uint16 {
	if f.startPort == 0 {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	port := f.startPort + f.opened
	f.opened++
	return port
}

// FactoryDependency returns a factory for gRPC servers on ephemeral ports.
//
// lggr is what the factory itself logs under; each server New makes is named by
// whoever asks for it.
func FactoryDependency(lggr logger.Logger) standalone.BootstrapDependency[*Factory] {
	return standalone.OnceBootstrapper[*Factory](&factoryDependency{lggr: lggr, cfg: FactoryConfig{AdvertiseHost: defaultHost}})
}

type factoryDependency struct {
	lggr logger.Logger
	cfg  FactoryConfig
}

var _ standalone.BootstrapDependency[*Factory] = (*factoryDependency)(nil)

func (d *factoryDependency) Namespace() string { return namespace }

func (d *factoryDependency) Config() any { return &d.cfg }

func (d *factoryDependency) Dependencies() []standalone.BootstrapCommand { return nil }

// ForEmbedding returns the receiver, so every instance resolves the one factory
// and draws from the one run of ports.
//
// This is the case BootstrapDependency.ForEmbedding describes as a dependency
// backed by a process-wide resource: ports are the process's, not an instance's.
// Partitioning them per instance the way the configured server does would need a
// stride - how many servers an instance opens - that only the instance knows, and
// a shared counter needs no such guess.
func (d *factoryDependency) ForEmbedding(_ int) standalone.BootstrapDependency[*Factory] { return d }

func (d *factoryDependency) Get(_ context.Context, _ standalone.CommonConfig) (*Factory, error) {
	return &Factory{host: d.cfg.AdvertiseHost, startPort: d.cfg.StartPort}, nil
}
