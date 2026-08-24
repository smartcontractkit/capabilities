// Command evm runs the EVM chain capability as its own binary.
//
// It hosts no node of its own. The chain it reads and writes is built here from
// chainlink-evm's own components - an RPC client, a head tracker, a log poller
// and a transaction manager over a database of its own - rather than reached
// through a relayer in the node's process. What it still borrows from the node is
// what only a node has: the keys it transmits under, the rage networking its
// oracle runs over, and the registry that says which DON it is.
package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/chainlink-evm/pkg/chains/legacyevm"
	creevm "github.com/smartcontractkit/chainlink-evm/pkg/cre/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/chain"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/protos"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/simulated"
	consMetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/capability"
	"github.com/smartcontractkit/capabilities/libs/standalone/eventstore"
	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"
	"github.com/smartcontractkit/capabilities/libs/standalone/keystore"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationsTable is this binary's goose history. Named for the binary rather
// than shared, so that a database holding more than one capability's tables keeps
// their migrations apart.
const migrationsTable = "evm_capability_migrations"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// The capability's own settings, bound directly: the chain, the client, the
	// database and the keys are dependencies, and each binds its own.
	cfg := config.Default

	root := &cobra.Command{
		Use:   "evm",
		Short: "The CRE EVM chain capability",
		Long: `Runs the EVM capability, which reads an EVM chain for a workflow, fires its
log triggers, and writes the reports a DON agrees on.

It reaches the chain itself: --evm.http-url is the RPC it dials, --chain.chain-id
says which chain that is, and --database.url plus --database.schema is where its
log poller and transaction manager keep their state - a schema of this
capability's own, so nothing it runs shares a table with the node's own relayers.
What it does not hold is keys or a peer - --keystore.proxy-address signs as this
node, --ocr.proxy-address carries the oracle's messages, and
--capabilities.proxy-url is the registry that says which DON this is and what OCR
configuration it runs under.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file;
run "docs" to write the full reference to docs/CONFIG.md.`,
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = "evm"
	if err := flags.RegisterCommandFlags(root, &cfg, opts); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root,
		standalone.WithOtelViews(append(consMetrics.MetricViews(), monitoring.MetricViews()...)))
	lggr := bootstrapper.Logger()

	// This capability's own database, in a schema of its own: the tables are
	// chainlink-evm's, and the node's copies of them are not this capability's to
	// share.
	dbDep := chain.DBDependency(lggr.Named("Database"), embeddedMigrations, migrationsTable)
	// The keys are the node's, borrowed a signature at a time: the transaction
	// manager sends as the account the registry knows this node by. An embedded run
	// has no node to borrow from and signs with keys derived from its index.
	ksDep := keystore.Proxy(lggr.Named("Keystore"), embeddedKeystore(&cfg))
	// The client says where the chain is; the chain is built over it. An embedded run
	// told about no chain gets one of its own, started in this process by the client
	// dependency itself.
	clientDep := creevm.Dependency(lggr.Named("EVMClient"))
	// What a deployment would have put on that chain: the instances' accounts funded,
	// and the forwarder they write reports through deployed and told who they are.
	// Nothing at all when the run named a chain, which came with its own.
	simDep := simulated.Dependency(lggr.Named("SimulatedChain"), clientDep.Simulated(), embeddedKeystore(&cfg))
	// Narrowed to this chain's account, the way a node's key states narrow the
	// keystore a relayer is handed: the store behind the proxy holds a key per chain
	// this node runs, and only one of them is this chain's transmitter.
	chainDep := chain.Dependency(lggr.Named("Chain"), clientDep, dbDep, ksDep, &cfg.NodeAddress)
	// The proxy form, not the host one: this binary drives an oracle, it does not run a peer.
	ocrDep := ocr.Proxy(lggr.Named("OCR"))
	capDep := capability.Dependency(lggr.Named("Capabilities"), standalonegrpc.FactoryDependency(lggr.Named("CapabilityAPI")))

	return standalone.Run6(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		evmChain legacyevm.Chain,
		keys core.Keystore,
		database *sql.DB,
		factories *ocr.OCRFactories,
		deps capability.Dependencies,
		deployment *simulated.Deployment,
	) []services.Service {
		lggr := scfg.Logger.Named("evm")

		// A simulated chain names its own forwarder: this process deployed it, and there
		// was nothing to configure the capability with before it existed.
		cfg := cfg
		if deployment != nil {
			cfg.CREForwarderAddress = deployment.Forwarder.Hex()
			if cfg.ReceiverGasMinimum == 0 {
				cfg.ReceiverGasMinimum = simulated.DefaultReceiverGasMinimum
			}
		}

		chainInfo, err := evmChain.GetChainInfo(ctx)
		if err != nil {
			lggr.Fatalw("Failed to read chain info", "error", err)
		}

		// The chain's read and write surface, as a relayer would have handed it over.
		evmService, err := chain.EVMService(lggr.Named("EVMService"), evmChain, database, keys, deps.CapabilityRegistry)
		if err != nil {
			lggr.Fatalw("Failed to build the chain's service", "error", err)
		}

		capabilityImpl, err := New(lggr, cfg, Dependencies{
			EVMService:         evmService,
			ChainInfo:          chainInfo,
			DonID:              deps.CapabilityDonID,
			Registry:           deps.OCRConfigRegistry,
			CapabilityRegistry: deps.CapabilityRegistry,
			Endpoints:          factories.OCR2Endpoint,
			Offchain:           factories.Offchain,
			Onchain:            factories.Onchain,
			TransmitAccount:    factories.TransmitAccount,
			Bootstrappers:      factories.Bootstrappers,
			// Trigger events outlive a restart, so they are kept where the chain state is,
			// under this chain's ID: the schema holds every chain this node runs the
			// capability on, the same way the chain tables do.
			EventStore:    eventstore.New(sqlx.NewDb(database, "pgx"), chainInfo.ChainID),
			LimitsFactory: deps.LimitsFactory,
			Metrics:       scfg.MetricsRegisterer,
		})
		if err != nil {
			lggr.Fatalw("Failed to create the EVM capability", "error", err)
		}

		// Run supervises the capability and makes it reachable: registered, served,
		// and announced to the node's registry.
		svcs, err := capability.Run(deps, *scfg, protos.NewClientServer(capabilityImpl))
		if err != nil {
			lggr.Fatalw("Failed to host the EVM capability", "error", err)
		}

		// The chain goes first: it is what the capability answers with, and the
		// bootstrapper starts services in the order given.
		return append([]services.Service{evmChain}, svcs...)
	}, chainDep, ksDep, dbDep, ocrDep, capDep, simDep)
}

// embeddedKeystore is what an embedded instance signs this chain's transactions
// with: the key it was given, or - given none - the one its index derives.
//
// Configured keys are per instance and in instance order, because the instances
// of an embedded run are separate DON members: two of them sending from one
// account would be two transaction managers assigning the same nonces.
func embeddedKeystore(cfg *config.Config) func(instance int) (core.Keystore, error) {
	return func(instance int) (core.Keystore, error) {
		keys := cfg.PrivateKeys
		if len(keys) == 0 {
			return chain.DeterministicKeystore(instance)
		}
		if instance >= len(keys) {
			return nil, fmt.Errorf("instance %d has no --evm.private-keys entry: %d were given, and every instance sends from an account of its own", instance, len(keys))
		}
		return chain.KeystoreFromPrivateKey(keys[instance])
	}
}
