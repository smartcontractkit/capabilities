// Command gateway runs the CRE gateway: what stands between a customer and a DON.
//
// Two listeners, because who may reach them differs:
//
//   - --gateway.user-address, where customers send JSON-RPC. Public.
//   - --gateway.node-address, where the DON's nodes connect. Reachable by the DON.
//
// The node listener carries the control traffic over HTTP/2 and, as HTTP/1.1
// CONNECTs, the tunnels a workflow uses when it has turned the cache off. One
// address, because they serve the same nodes and prove them the same way.
//
// A node proves who it is by signing with its chain key - the key the DON's
// membership is recorded under - so there is no credential to issue and none to
// leak.
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
	"golang.org/x/net/http2"

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

type Config struct {
	service.Config `toml:",inline"`

	// A signature that recovers to anything else is refused, so this is the whole of
	// who may connect.
	Nodes []string `json:"nodes" usage:"addresses of the nodes of this gateway's DON" example:"['0x0000000000000000000000000000000000000000']"`

	// The proxy a node tunnels through is not a third listener: it shares the node
	// one, since it serves the same nodes and proves them the same way.
	UserAddress string `json:"userAddress" usage:"address the customer-facing JSON-RPC listener binds to"`
	NodeAddress string `json:"nodeAddress" usage:"address the DON's nodes connect to, for control traffic and for tunnels"`

	// For the customer-facing listener. The node listener needs none - identity is
	// proved by signature, not by the transport - though a deployment may add one.
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

	// A node that turned the cache off fetches for itself through this, and the
	// gateway carries bytes it cannot read.
	tunnel, err := service.NewTunnel(logger.Named(lggr, "Proxy"), service.TunnelConfig{GatewayID: cfg.GatewayID}, dons)
	if err != nil {
		return err
	}

	servers := []*listener{
		// HTTP/2 without TLS: a session is pinned to one connection, and the poll and the
		// messages answering it have to share it. The CONNECTs go to the tunnel.
		{name: "nodes", address: cfg.NodeAddress, handler: server.Serve(nodes, tunnel)},
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
		// Whoever launched this reloads settings by asking; this gateway has none, and
		// saying so is better than a connection refused.
		health.HandleFunc("GET /reload/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

		servers = append(servers, &listener{name: "health", address: fmt.Sprintf(":%d", healthPort), handler: health})
	}

	stopped := make(chan error, len(servers))
	for _, l := range servers {
		lggr.Infow("Serving", "what", l.name, "address", l.address, "tls", l.cert != "")
		go func() { stopped <- l.serve() }()
	}

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if underPluginHost() {
		// A capabilityrunner job supervises what it starts over go-plugin, and a process
		// that does not answer the handshake is reported unavailable however well it is
		// running. There is nothing to serve over the plugin - this process works over
		// its own listeners - so it is empty, and blocks until the node shuts it down.
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
		// What a node's session is pinned to. Harmless on the listeners that do not ask.
		ConnContext: server.ConnContext,
	}

	if l.cert != "" {
		// The cleartext listener gets this from server.Serve, which wraps the mux; a
		// TLS one negotiates HTTP/2 in the handshake instead, and would otherwise take
		// net/http's default rather than the limit a node's traffic is sized for.
		if err := http2.ConfigureServer(l.server, &http2.Server{MaxConcurrentStreams: server.MaxConcurrentStreams}); err != nil {
			return fmt.Errorf("failed to configure HTTP/2 on the %s listener: %w", l.name, err)
		}
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

// go-plugin's Serve exits when the handshake cookie is absent, so the plugin is
// only served when there is a host to serve it to.
func underPluginHost() bool {
	handshake := loop.EmptyHandshakeConfig()
	return os.Getenv(handshake.MagicCookieKey) == handshake.MagicCookieValue
}
