// Command http runs the HTTP capabilities as one binary.
//
// Two capabilities, one process: the trigger, which a customer's request reaches
// through a gateway, and the action, which is how a workflow reaches out. They
// were separate binaries because they were separate LOOPs; they share a gateway
// connection, and now they share the process that holds it.
//
// It holds no keys. The connection to a gateway is authenticated by a signature
// from this node's chain key, which lives in crecore and is reached over
// --keystore.proxy-address; what crosses that hop is a digest and a signature.
package main

import (
	"context"
	"database/sql"
	"embed"
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/http/action"
	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/smartcontractkit/capabilities/http/gateway/connector"
	"github.com/smartcontractkit/capabilities/http/outbound"
	"github.com/smartcontractkit/capabilities/http/protos"
	"github.com/smartcontractkit/capabilities/http/trigger"

	commonstandalone "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/capability"
	"github.com/smartcontractkit/capabilities/libs/standalone/db"
	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"
	"github.com/smartcontractkit/capabilities/libs/standalone/keystore"
	"github.com/smartcontractkit/capabilities/libs/standalone/kvstore"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationsTable is this binary's goose history. Named for the binary rather
// than shared, so that a database holding more than one capability's tables keeps
// their migrations apart.
const migrationsTable = "http_capability_migrations"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	triggerConfig := trigger.ServiceConfig{}

	root := &cobra.Command{
		Use:   "http",
		Short: "The CRE HTTP trigger and action capabilities",
		Long: `Runs the HTTP capabilities: the trigger a customer's request reaches through a
gateway, and the action a workflow reaches out with.

Both talk to the same gateways over one connection - --gateway.gateways is where
they are, --gateway.don-id and --gateway.node-address who this node says it is -
and both sign as this node without holding its key: --keystore.proxy-address is
the process that does.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file;
run "docs" to write the full reference to docs/CONFIG.md.`,
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = "trigger"
	if err := flags.RegisterCommandFlags(root, &triggerConfig, opts); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root)
	lggr := bootstrapper.Logger()

	// The node's keys, borrowed a signature at a time: what authenticates this
	// process to a gateway is the node's chain key, and it stays where it is.
	ksDep := keystore.Proxy(lggr.Named("Keystore"), nil)
	// Where an answered trigger request is remembered, so that a retry of it is
	// answered rather than run again. A node kept this in its own table; this binary
	// keeps it in a schema of its own in the same database.
	dbDep := db.Dependency(embeddedMigrations, migrationsTable)
	// The gateway this process talks to. An embedded run has none to talk to, so it
	// runs one here and reaches it by function call rather than by dialling itself.
	connDep := commonstandalone.OnceBootstrapper[connector.Connection](connector.Dependency(lggr.Named("Gateway"), ksDep))
	capDep := capability.Dependency(lggr.Named("Capabilities"), standalonegrpc.FactoryDependency(lggr.Named("CapabilityAPI")))
	// Where the action's requests go, and the whole of what deciding that involves:
	// through the gateway above, or straight out of this process. Nothing outside
	// that package knows which - see outbound.Dependency.
	outDep := outbound.Dependency(lggr.Named("Outbound"), connDep, capDep)

	return standalone.Run4(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		gatewayConnector connector.Connection,
		database *sql.DB,
		deps capability.Dependencies,
		requests common.Outbound,
	) []services.Service {
		lggr := scfg.Logger.Named("http")

		triggerService, err := trigger.NewService(lggr, triggerConfig, trigger.Dependencies{
			Connector:       gatewayConnector,
			Store:           kvstore.New(sqlx.NewDb(database, "pgx"), "http_trigger"),
			CapabilityDonID: deps.CapabilityDonID,
			LimitsFactory:   deps.LimitsFactory,
		})
		if err != nil {
			lggr.Fatalw("Failed to create the HTTP trigger capability", "error", err)
		}

		actionService, err := action.NewService(lggr, action.Dependencies{
			Outbound:      requests,
			LimitsFactory: deps.LimitsFactory,
		})
		if err != nil {
			lggr.Fatalw("Failed to create the HTTP action capability", "error", err)
		}

		// An embedded run's gateway has no listener of its own, so it takes customer
		// requests on this instance's own server. Nothing else changes about it: the
		// same JSON-RPC, the same token, the same workflow authorisation.
		if connector.Serve(gatewayConnector, scfg.Mux) {
			lggr.Infow("The embedded gateway takes customer requests here", "path", connector.EmbeddedGatewayPath)
		}

		// Both capabilities, one host: they are registered and served together, and the
		// gateway connection they share starts before either of them does.
		svcs, err := capability.Run(deps, *scfg,
			protos.NewHTTPServer(triggerService),
			protos.NewClientServer(actionService),
		)
		if err != nil {
			lggr.Fatalw("Failed to host the HTTP capabilities", "error", err)
		}

		return append([]services.Service{gatewayConnector}, svcs...)
	}, connDep, dbDep, capDep, outDep)
}
