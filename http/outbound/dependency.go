// Package outbound is where the HTTP action capability's requests go.
//
// The capability itself has no idea: it decides whether a workflow may make a
// request - the limits, the validation, the errors it is answered with - and
// hands the request to a common.Outbound. This package is what supplies one, and
// the whole of what deciding between them involves.
//
// Two are offered. The gateway one sends to the gateway this node's DON shares,
// which either fetches on the DON's behalf (so that every node is served the same
// answer, which is how they agree on what the internet said) or opens a tunnel
// for this node to fetch through, which is what a workflow gets when it turns the
// cache off. The direct one makes the request from this process, which is what
// something with no gateway to speak of does.
package outbound

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/smartcontractkit/capabilities/http/gateway/connector"
	"github.com/smartcontractkit/capabilities/libs/standalone/capability"
)

// Config is how requests leave, and nothing else. It is a namespace of its own
// rather than part of the capability's settings, because the capability has no
// business knowing which of these it got.
type Config struct {
	// Direct makes requests from this process rather than through a gateway.
	//
	// A run that has a gateway goes through it: that is what a node does, and what an
	// embedded run does with the gateway it runs itself, so that an embedded DON
	// exercises the same path a real one takes. This is the way out of that, for a
	// run with no gateway to reach - a CLI, an enclave - or for testing the
	// capability without one in front of it.
	Direct bool `json:"direct" usage:"make outbound requests from this process rather than through a gateway"`

	// GatewayConnection is how a request to the gateway is retried while the gateway
	// is being reached.
	GatewayConnection common.GatewayConnectionConfig `json:"gatewayConnection"`

	// HTTPClient is what a direct request is allowed to reach. It is the protection
	// a gateway would otherwise be giving: addresses, ports and schemes a workflow
	// must not be able to name.
	HTTPClient common.HTTPClientConfig `json:"httpClient"`
}

// Dependency returns this process's Outbound.
//
// The gateway connection is a dependency of this one rather than something a
// caller wires up, so that a direct run resolves no connection at all: what makes
// requests is decided here, and nothing above it knows which was chosen.
func Dependency(
	lggr logger.Logger,
	gateway standalone.BootstrapDependency[connector.Connection],
	capabilities standalone.BootstrapDependency[capability.Dependencies],
) standalone.BootstrapDependency[common.Outbound] {
	return &dependency{lggr: lggr, gateway: gateway, capabilities: capabilities, cfg: &Config{}}
}

type dependency struct {
	lggr    logger.Logger
	gateway standalone.BootstrapDependency[connector.Connection]
	// capabilities is where the limits a gateway request is settled by come from -
	// which gateway DON a workflow's requests go to is one of its settings.
	capabilities standalone.BootstrapDependency[capability.Dependencies]
	cfg          *Config
}

var _ standalone.BootstrapDependency[common.Outbound] = (*dependency)(nil)

func (d *dependency) Namespace() string { return "outbound" }

func (d *dependency) Config() any { return d.cfg }

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	if d.cfg.Direct {
		// A direct run reaches no gateway, so it needs none: not to connect to, and not
		// to be configured with.
		return nil
	}
	return []standalone.BootstrapCommand{d.gateway, d.capabilities}
}

// ForEmbedding is the same decision for an embedded instance.
//
// The instances of an embedded run share the gateway that run started, so each of
// them goes through it exactly as a node does - the connection dependency is what
// hands out an instance's end of it, and this only has to ask.
func (d *dependency) ForEmbedding(i, instances int) standalone.BootstrapDependency[common.Outbound] {
	return &dependency{
		lggr:         d.lggr,
		gateway:      d.gateway.ForEmbedding(i, instances),
		capabilities: d.capabilities.ForEmbedding(i, instances),
		cfg:          d.cfg,
	}
}

func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (common.Outbound, error) {
	if d.cfg.Direct {
		d.lggr.Info("Outbound requests are made from this process: there is no gateway in front of them")
		return common.NewDirect(d.cfg.HTTPClient, d.lggr)
	}

	gateway, err := d.gateway.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get the gateway outbound requests go through: %w", err)
	}

	capabilities, err := d.capabilities.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get the settings a gateway request is sent under: %w", err)
	}

	return NewGatewayOutboundProxy(
		gateway,
		d.cfg.GatewayConnection,
		d.lggr,
		capabilities.LimitsFactory,
	)
}
