package connector

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

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

	// Tunnel opens a connection to a host through the gateway, for a request the
	// gateway is not to see. It is the same connection in its other role: a node
	// tunnels through the gateway it is already talking to, on the same address.
	Tunnel(ctx context.Context, gatewayID, address string) (net.Conn, error)
}

// Dependency returns this process's gateway connection.
//
// A configured run connects to the gateways it was told about, signing as this
// node with the key crecore holds. An embedded run has no gateway to connect to
// and no node to be, so it runs one in this process and talks to it by function
// call: see the inproc package for why that is the honest shape rather than a
// loopback socket.
func Dependency(lggr logger.Logger, keystore standalone.BootstrapDependency[core.Keystore]) standalone.BootstrapDependency[Connection] {
	cfg := Defaults
	return &dependency{lggr: lggr, keystore: keystore, cfg: &cfg}
}

type dependency struct {
	lggr     logger.Logger
	keystore standalone.BootstrapDependency[core.Keystore]
	cfg      *Config

	// embedded is the one embedded form, and shared is what its instances share: a
	// gateway, and the set of nodes it serves. The bootstrapper asks for a form per
	// instance and once more to collect settings, and they all have to be the same
	// gateway.
	embedded *dependency
	shared   *embeddedRun
	instance int
}

// embeddedRun is the gateway an embedded run talks to, and the nodes on it.
type embeddedRun struct {
	nodes   *inproc.Nodes
	gateway *service.Gateway
	err     error
	started bool

	// proxy is the gateway's CONNECT listener, and members who may use it.
	//
	// It is a real socket, dialled over real TCP, even though both ends are in this
	// process. Tunnelling is what an uncached request does on a node, and an embedded
	// run is for finding out whether that works: a shortcut here would test a path
	// that nothing runs in earnest.
	proxy   net.Listener
	members *members

	// connections is instance i's end of the gateway, kept because an instance is
	// asked for more than once - once by whatever hosts the capabilities, and again
	// by whatever their outbound requests go through. Building a second one would
	// register a second node under the same name, and the gateway would send to the
	// one holding no handlers.
	connections map[int]*embeddedConnection
}

// members is the embedded DON, which fills up as its instances resolve. The
// gateway is serving before the last of them exists, so membership cannot be a
// list settled once.
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
		// An embedded run is one DON on one gateway, and neither is reached over a
		// network, so the names are only names.
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

	// One per instance, sharing the run: what differs between them is which node
	// they are.
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

// embed returns this instance's end of the in-process gateway, starting the
// gateway the first time it is asked for.
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

	// This instance's identity on the proxy. The control connection needs none -
	// it is a function call - but the tunnel is a socket with a handshake on it, and
	// the whole point of dialling it is that the handshake runs.
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

// listen starts the embedded gateway's CONNECT proxy.
//
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

// EmbeddedGatewayPath is where an embedded run's gateway takes customer
// requests, on the same server the health endpoints are on.
//
// An embedded run has no customer-facing listener of its own - it is one process
// pretending to be a DON, not a deployment - but a trigger only ever fires
// because a customer asked for it, so there has to be somewhere to ask.
const EmbeddedGatewayPath = "/gateway"

// Serve mounts an embedded run's gateway on mux, and reports whether it did.
//
// A connection to a gateway elsewhere is nobody's to serve, so this does nothing
// for one: the customer's end of that gateway is wherever it is running.
func Serve(c Connection, mux *http.ServeMux) bool {
	embedded, ok := c.(*embeddedConnection)
	if !ok {
		return false
	}
	mux.Handle("POST "+EmbeddedGatewayPath, embedded.gateway)
	return true
}

// Embedded reports whether this connection is to a gateway this process runs
// itself, rather than to one it dialled.
//
// It is how a capability tells the two apart when the difference changes what it
// should do: the action, whose choice between reaching out directly and going
// through a gateway is only a real choice when there is a gateway that is not
// ours.
func Embedded(c Connection) bool {
	_, embedded := c.(*embeddedConnection)
	return embedded
}

// Tunnel opens a connection to address through the gateway this process runs.
//
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
