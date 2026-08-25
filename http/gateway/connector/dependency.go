package connector

import (
	"context"
	"fmt"
	"net/http"

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
			lggr:   d.lggr,
			cfg:    &cfg,
			shared: &embeddedRun{nodes: inproc.NewNodes(logger.Named(d.lggr, "Gateway"))},
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
	}
	if d.shared.err != nil {
		return nil, d.shared.err
	}

	node := fmt.Sprintf("node-%d", d.instance)
	return &embeddedConnection{
		Connector: d.shared.nodes.Connector(d.shared.gateway, embeddedGatewayID, d.cfg.DonID, node),
		gateway:   d.shared.gateway,
		first:     d.instance == 0,
	}, nil
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
	return c.gateway.Close()
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
