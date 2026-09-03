package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	"github.com/smartcontractkit/capabilities/http/gateway/inproc"
	"github.com/smartcontractkit/capabilities/http/gateway/service"
)

// Connection is what the capabilities in this binary are handed: somewhere to
// send what they have to say, and something that starts and stops with them.
type Connection interface {
	core.MultiGatewayConnector
	services.Service

	// And somewhere to ask, for what they are waiting on rather than getting on
	// without: the answer comes back on the request itself.
	Request(ctx context.Context, gatewayID string, msg *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error)

	// The same connection in its other role: a node tunnels through the gateway it is
	// already talking to, on the same address, for a request it is not to see.
	Tunnel(ctx context.Context, gatewayID, address string) (net.Conn, error)
}

// Dependency returns this process's gateway connection.
//
// An embedded run has no gateway to connect to and no node to be, so it runs one
// in this process and talks to it by function call: see the inproc package for
// why that is the honest shape rather than a loopback socket.
func Dependency(lggr logger.Logger, keystore standalone.BootstrapDependency[core.Keystore]) standalone.BootstrapDependency[Connection] {
	cfg := Defaults
	return &dependency{lggr: lggr, keystore: keystore, cfg: &cfg}
}

type dependency struct {
	lggr     logger.Logger
	keystore standalone.BootstrapDependency[core.Keystore]
	cfg      *Config

	// The bootstrapper asks for a form per instance and once more to collect
	// settings, and they all have to end up on the same gateway.
	embedded *dependency
	shared   *embeddedRun
	instance int
}

type embeddedRun struct {
	nodes   *inproc.Nodes
	gateway *service.Gateway
	err     error
	started bool

	// A real socket, dialled over real TCP, even though both ends are in this process:
	// an embedded run is for finding out whether the tunnel works, and a shortcut here
	// would exercise a path that nothing runs in earnest.
	proxy   net.Listener
	members *members

	// Kept because an instance is asked for more than once - by whatever hosts the
	// capabilities, and by whatever their outbound requests go through. A second one
	// would register a second node under the same name, and the gateway would send to
	// whichever of them held no handlers.
	connections map[int]*embeddedConnection
}

// members fills up as instances resolve: the gateway is serving before the last
// of them exists, so membership cannot be a list settled once.
type members struct {
	mu        sync.Mutex
	donID     string
	addresses []string
}

func (m *members) add(address string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addresses = append(m.addresses, address)
}

func (m *members) Nodes(donID string) ([]string, bool) {
	if donID != m.donID {
		return nil, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return auth.DONs{m.donID: m.addresses}.Nodes(m.donID)
}

func (m *members) Verify(address string, hash, sig []byte) bool {
	return auth.DONs{}.Verify(address, hash, sig)
}

var _ standalone.BootstrapDependency[Connection] = (*dependency)(nil)

func (d *dependency) Namespace() string { return "gateway" }

func (d *dependency) Config() any { return d.cfg }

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	if d.shared != nil {
		// An embedded instance signs nothing: there is no gateway to prove anything to.
		return nil
	}
	return []standalone.BootstrapCommand{d.keystore}
}

// ForEmbedding gives instance i its own connection to the one gateway this
// process runs.
func (d *dependency) ForEmbedding(i, instances int) standalone.BootstrapDependency[Connection] {
	if d.embedded == nil {
		cfg := Defaults
		// Neither is reached over a network, so the names are only names.
		cfg.DonID, cfg.Gateways = "embedded", nil
		d.embedded = &dependency{
			lggr: d.lggr,
			cfg:  &cfg,
			shared: &embeddedRun{
				nodes:       inproc.NewNodes(logger.Named(d.lggr, "Gateway")),
				connections: map[int]*embeddedConnection{},
			},
		}
	}
	_ = instances

	// What differs between instances is which node they are.
	instanceDep := *d.embedded
	instanceDep.instance = i
	return &instanceDep
}

func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (Connection, error) {
	if d.shared != nil {
		return d.embed()
	}

	keystore, err := d.keystore.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get the keystore this node signs with: %w", err)
	}

	return New(d.lggr, *d.cfg, auth.KeystoreSigner(keystore, d.cfg.NodeAddress))
}

// embed starts the gateway the first time an instance asks for its end of it.
func (d *dependency) embed() (Connection, error) {
	if !d.shared.started {
		d.shared.started = true

		gateway, err := service.New(logger.Named(d.lggr, "Gateway"), service.Config{
			GatewayID: embeddedGatewayID,
			DonID:     d.cfg.DonID,
			// F is zero: an embedded run's instances are goroutines in one process, and a
			// process cannot disagree with itself about what a workflow answered. Waiting
			// for a second opinion would be waiting for its own.
			F: 0,
		}, d.shared.nodes)
		if err != nil {
			d.shared.err = err
		}
		d.shared.gateway = gateway

		if err := d.listen(); err != nil {
			d.shared.err = err
		}
	}
	if d.shared.err != nil {
		return nil, d.shared.err
	}

	if existing, built := d.shared.connections[d.instance]; built {
		return existing, nil
	}

	// The control connection needs no identity - it is a function call - but the
	// tunnel is a socket, and the point of dialling it is that the handshake runs.
	sign, address, err := auth.Generated()
	if err != nil {
		return nil, err
	}
	d.shared.members.add(address)

	node := fmt.Sprintf("node-%d", d.instance)
	connection := &embeddedConnection{
		Connector: d.shared.nodes.Connector(d.shared.gateway, embeddedGatewayID, d.cfg.DonID, node),
		gateway:   d.shared.gateway,
		first:     d.instance == 0,
		proxy:     d.shared.proxy,
		donID:     d.cfg.DonID,
		signer:    sign,
	}
	d.shared.connections[d.instance] = connection

	return connection, nil
}

// On a loopback socket rather than in memory: what a node does with an uncached
// request is dial a proxy and speak HTTP to it, and an embedded run is where that
// is meant to be exercised. The port is the OS's to choose, since two embedded
// runs on one machine are two proxies.
func (d *dependency) listen() error {
	d.shared.members = &members{donID: d.cfg.DonID}

	tunnel, err := service.NewTunnel(
		logger.Named(d.lggr, "Proxy"),
		service.TunnelConfig{GatewayID: embeddedGatewayID},
		d.shared.members,
	)
	if err != nil {
		return fmt.Errorf("failed to build the embedded gateway's proxy: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to open the embedded gateway's proxy: %w", err)
	}
	d.shared.proxy = listener

	d.lggr.Infow("The embedded gateway tunnels here", "address", listener.Addr().String())
	go func() {
		//nolint:gosec // A proxy hijacks its connections; a read header timeout would cut a tunnel.
		if err := (&http.Server{Handler: tunnel}).Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.lggr.Errorw("The embedded gateway's proxy stopped", "err", err)
		}
	}()
	return nil
}

// embeddedGatewayID is what an embedded run's gateway calls itself. Nothing
// authenticates to it, so the name is for logs.
const embeddedGatewayID = "embedded"

// embeddedConnection is an instance's connection, and - for the first instance -
// the gateway itself, so that something starts and stops it.
type embeddedConnection struct {
	*inproc.Connector
	gateway *service.Gateway
	first   bool

	// proxy, donID and signer are this instance's way through the tunnel: the
	// gateway's own listener, and what proves this instance may use it.
	proxy  net.Listener
	donID  string
	signer auth.SignerFunc
}

var _ Connection = (*embeddedConnection)(nil)

func (c *embeddedConnection) Start(ctx context.Context) error {
	if !c.first {
		return nil
	}
	return c.gateway.Start(ctx)
}

func (c *embeddedConnection) Close() error {
	if !c.first {
		return nil
	}
	return errors.Join(c.gateway.Close(), c.proxy.Close())
}

func (c *embeddedConnection) Ready() error {
	if !c.first {
		return nil
	}
	return c.gateway.Ready()
}

func (c *embeddedConnection) HealthReport() map[string]error {
	if !c.first {
		return map[string]error{c.Name(): nil}
	}
	return c.gateway.HealthReport()
}

func (c *embeddedConnection) Name() string { return "EmbeddedGatewayConnection" }

// EmbeddedGatewayPath is on the same server the health endpoints are on: an
// embedded run has no customer-facing listener of its own, but a trigger only
// fires because a customer asked, so there has to be somewhere to ask.
const EmbeddedGatewayPath = "/gateway"

// Serve reports whether it mounted anything: a gateway elsewhere is nobody's to
// serve here, and its customer end is wherever it is running.
func Serve(c Connection, mux *http.ServeMux) bool {
	embedded, ok := c.(*embeddedConnection)
	if !ok {
		return false
	}
	mux.Handle("POST "+EmbeddedGatewayPath, embedded.gateway)
	return true
}

// The same CONNECT with the same two signatures a node makes to a gateway across
// a network: what is embedded here is the gateway, not the protocol.
func (c *embeddedConnection) Tunnel(ctx context.Context, gatewayID, address string) (net.Conn, error) {
	if gatewayID != embeddedGatewayID {
		return nil, fmt.Errorf("gateway %q is not the one this process runs", gatewayID)
	}

	tunnel := &Tunnel{
		Gateway:   c.proxy.Addr().String(),
		GatewayID: embeddedGatewayID,
		DonID:     c.donID,
		Signer:    c.signer,
	}
	return tunnel.DialContext(ctx, "tcp", address)
}
