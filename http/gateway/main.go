// Command gateway runs the CRE gateway.
//
// It is what stands between a customer and a DON: a customer's trigger request
// arrives here, is checked against what the workflow authorised, and is put to
// the DON; and a workflow's outbound HTTP request goes out from here, so that
// every node of the DON is answered with the same thing.
//
// It serves three listeners, because the three have nothing to do with each
// other and should not be reachable from the same places:
//
//   - --user.address, where customers send JSON-RPC. Public.
//   - --node.address, where the DON's nodes connect. Reachable by the DON.
//   - --proxy.address, the CONNECT proxy a workflow uses when it has turned the
//     cache off. Reachable by the DON; carries bytes this process cannot read.
//
// A node proves who it is by signing: a header naming this gateway and the
// moment, then a challenge this gateway chose. Both signatures are made with the
// node's chain key, which is the key the DON's membership is recorded under - so
// there is no credential to issue and none to leak.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hashicorp/go-plugin"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	"github.com/smartcontractkit/capabilities/http/gateway/server"
	"github.com/smartcontractkit/capabilities/http/gateway/service"
)

func main() {
	if err := command().Execute(); err != nil {
		log.Fatal(err)
	}
}

// Config is what this binary needs told.
type Config struct {
	service.Config `toml:",inline"`

	// Nodes is the DON's membership, as node addresses. A signature that recovers to
	// anything else is refused, so this is the whole of who may connect.
	Nodes []string `json:"nodes" usage:"addresses of the nodes of this gateway's DON" example:"['0x0000000000000000000000000000000000000000']"`

	// UserAddress, NodeAddress and ProxyAddress are the three listeners. An empty
	// proxy address serves no proxy, which is what a deployment that only allows
	// cached requests wants.
	UserAddress  string `json:"userAddress" usage:"address the customer-facing JSON-RPC listener binds to"`
	NodeAddress  string `json:"nodeAddress" usage:"address the DON's nodes connect to"`
	ProxyAddress string `json:"proxyAddress" usage:"address the CONNECT proxy binds to; empty serves no proxy"`

	// TLSCertFile and TLSKeyFile turn on TLS for the customer-facing listener. The
	// node listener needs none - a node's identity is proved by signature, not by the
	// transport - though a deployment may put one in front of it anyway.
	TLSCertFile string `json:"tlsCertFile" usage:"certificate for the customer-facing listener; empty serves plaintext"`
	TLSKeyFile  string `json:"tlsKeyFile" usage:"key for the customer-facing listener"`
}

var defaultConfig = Config{
	Config:      service.Defaults,
	UserAddress: ":5002",
	NodeAddress: ":5003",
}

func command() *cobra.Command {
	cfg := defaultConfig

	root := &cobra.Command{
		Use:   "gateway",
		Short: "The CRE gateway",
		Long: `Runs the gateway: the way into a DON for a customer's HTTP trigger, and the way
out for a workflow's HTTP request.

--gateway-id is the name nodes authenticate to, --don-id the DON it serves and
--nodes its membership: a node connects by signing, with the key that membership
is recorded under, a header naming this gateway and then a challenge it is given.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			health, err := cmd.Flags().GetUint16(healthPortFlag)
			if err != nil {
				return err
			}
			return run(cmd.Context(), cfg, health)
		},
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	// Not under the gateway namespace, and spelled with a dot: this is the flag a
	// capabilityrunner job requires of anything it launches, and that is how a node
	// starts this.
	root.PersistentFlags().Uint16(healthPortFlag, 0,
		"port serving /healthz and the reload endpoint the process that launched this calls")

	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = "gateway"
	if err := flags.RegisterCommandFlags(root, &cfg, opts); err != nil {
		log.Fatal(err)
	}
	return root
}

// healthPortFlag is what a capabilityrunner job passes to whatever it starts.
const healthPortFlag = "http.port"

func run(ctx context.Context, cfg Config, healthPort uint16) error {
	lggr, err := logger.New()
	if err != nil {
		return err
	}
	if len(cfg.Nodes) == 0 {
		return errors.New("--gateway.nodes is required: a gateway that knows no nodes can serve nobody")
	}

	// The DON's membership, which is what every signature is checked against.
	dons := auth.DONs{cfg.DonID: cfg.Nodes}

	transport, err := server.NewTransport(logger.Named(lggr, "Transport"), server.Config{
		GatewayID: cfg.GatewayID,
	}, dons, nil)
	if err != nil {
		return err
	}

	gateway, err := service.New(logger.Named(lggr, "Gateway"), cfg.Config, transport)
	if err != nil {
		return err
	}
	// The transport was built before the gateway because the gateway needs it, and
	// the gateway is what the transport hands messages to: one of them has to be
	// second.
	transport.Handles(gateway)

	if err := gateway.Start(ctx); err != nil {
		return err
	}
	defer func() {
		if err := gateway.Close(); err != nil {
			lggr.Errorw("Failed to close the gateway", "err", err)
		}
	}()

	nodes := http.NewServeMux()
	transport.Routes(nodes)

	users := http.NewServeMux()
	gateway.Routes(users)

	servers := []*listener{
		// HTTP/2 without TLS on the node listener: a session is pinned to one
		// connection, and the poll and the messages that answer it have to share it.
		{name: "nodes", address: cfg.NodeAddress, handler: server.Serve(nodes)},
		{name: "users", address: cfg.UserAddress, handler: users, cert: cfg.TLSCertFile, key: cfg.TLSKeyFile},
	}

	if healthPort > 0 {
		health := http.NewServeMux()
		health.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			if err := gateway.Ready(); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("ok\n"))
		})
		// Whoever launched this reloads settings by asking for them; this gateway has
		// none to reload, and saying so is better than a connection refused.
		health.HandleFunc("GET /reload/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

		servers = append(servers, &listener{name: "health", address: fmt.Sprintf(":%d", healthPort), handler: health})
	}

	if cfg.ProxyAddress != "" {
		tunnel, terr := service.NewTunnel(logger.Named(lggr, "Proxy"), service.TunnelConfig{GatewayID: cfg.GatewayID}, dons)
		if terr != nil {
			return terr
		}
		// Plain HTTP/1.1: a CONNECT takes over its connection, so there is nothing to
		// multiplex, and TLS here would encrypt a hop whose contents are already
		// encrypted end to end.
		servers = append(servers, &listener{name: "proxy", address: cfg.ProxyAddress, handler: tunnel})
	}

	stopped := make(chan error, len(servers))
	for _, l := range servers {
		lggr.Infow("Serving", "what", l.name, "address", l.address, "tls", l.cert != "")
		go func() { stopped <- l.serve() }()
	}

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if underPluginHost() {
		// Launched by a node rather than by a person: a capabilityrunner job supervises
		// what it starts over go-plugin, and a process that does not answer the
		// handshake is one it reports as unavailable however well it is running. The
		// empty plugin is the answer - there is nothing to serve over it, since what
		// this process does it does over its own listeners - and it blocks until the
		// node shuts this down.
		//
		// The same thing the capabilities' bootstrapper does, for the same reason.
		lggr.Info("Serving the empty plugin: this gateway was launched by a node")
		go func() {
			plugin.Serve(&plugin.ServeConfig{
				HandshakeConfig: loop.EmptyHandshakeConfig(),
				Plugins:         map[string]plugin.Plugin{loop.PluginEmptyName: &loop.EmptyLoop{}},
				GRPCServer:      plugin.DefaultGRPCServer,
			})
			cancel()
		}()
	}

	select {
	case <-ctx.Done():
		lggr.Info("Shutting down")
	case err := <-stopped:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdown, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer done()
	for _, l := range servers {
		_ = l.close(shutdown)
	}
	return nil
}

// listener is one of the three servers this binary runs.
type listener struct {
	name      string
	address   string
	handler   http.Handler
	cert, key string

	server *http.Server
}

func (l *listener) serve() error {
	l.server = &http.Server{
		Addr:              l.address,
		Handler:           l.handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Which connection a request arrived on, which is what a node's session is
		// pinned to. Harmless on the other listeners, which do not ask.
		ConnContext: server.ConnContext,
	}

	if l.cert != "" {
		return l.server.ListenAndServeTLS(l.cert, l.key)
	}
	return l.server.ListenAndServe()
}

func (l *listener) close(ctx context.Context) error {
	if l.server == nil {
		return nil
	}
	return l.server.Shutdown(ctx)
}

// underPluginHost reports whether a go-plugin host started this process, by the
// handshake cookie it sets. go-plugin's Serve exits when that is absent, so this
// only serves the plugin when there is a host to serve it to.
func underPluginHost() bool {
	handshake := loop.EmptyHandshakeConfig()
	return os.Getenv(handshake.MagicCookieKey) == handshake.MagicCookieValue
}
