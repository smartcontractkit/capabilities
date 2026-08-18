package main

import (
	"context"
	"database/sql"
	"embed"
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/db"
	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
	"github.com/smartcontractkit/capabilities/libs/x/registry"
	"github.com/smartcontractkit/capabilities/libs/x/registrysyncer"

	"github.com/smartcontractkit/chainlink-evm/pkg/cre/evm"
	evmregistry "github.com/smartcontractkit/chainlink-evm/pkg/cre/registry"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

const migrationsTable = "proxy_migrations"

// ocrDiscovererTable is the table backing OCR p2p announcements. Must match the
// CREATE TABLE in migrations/0001_*.sql.
const ocrDiscovererTable = "proxy_ocr_discoverer_announcements"

// registrySnapshotsTable is where the last known registry is kept, so a restart can answer registry
// lookups before its first on-chain read lands. Must match the CREATE TABLE in migrations/0002_*.sql.
const registrySnapshotsTable = "proxy_registry_snapshots"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := defaultConfig

	root := &cobra.Command{
		Use:   "main",
		Short: "P2P proxy for the CRE",
		Long: `Runs a single shared rage (libocr) peer and exposes it over gRPC so that
core can delegate its OCR networking (and, in future, DON-to-DON networking) to
this process. The peer's identity is the node's own, loaded from the keystore in
the database the two share.

It also serves the CapabilitiesRegistry on that same gRPC address (--grpc.port),
read directly from chain with an EVM client (no relayer). Core uses that in place
of its own registrysyncer whenever it is delegating rage networking to this
process, which is why --grpc.port must be configured: this process running is
what enables the registry, and core does not start without it.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file;
run "docs" to write the full reference to docs/CONFIG.md.`,
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	if err := flags.RegisterCommandFlags(root, &cfg, flags.DefaultTOMLOptions("CRE", "CL")); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root, standalone.WithOtelViews(metricViews()))
	lggr := bootstrapper.Logger()

	dbDep := db.Dependency(embeddedMigrations, migrationsTable)
	ocrDep := ocr.Host(lggr.Named("OCR"), dbDep, ocrDiscovererTable)

	readerDep := evmregistry.Dependency(lggr.Named("CapabilitiesRegistry"), evm.Dependency(lggr.Named("EVM")))

	grpcDep := standalonegrpc.Dependency(lggr.Named("CoreAPI"))

	return standalone.Run4(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		factories *ocr.RageFactories,
		reader registry.Reader,
		database *sql.DB,
		grpcSrv *standalonegrpc.Server,
	) []services.Service {
		// Where the last known registry is kept. Resolving the database again costs nothing - it is
		// the same dependency the OCR host already took, opened once - and taking it here is what
		// keeps the registry's own table its own business rather than the OCR host's.
		regORM := registrysyncer.NewORM(sqlx.NewDb(database, "pgx"),
			scfg.Logger.Named("registry snapshots"), registrySnapshotsTable)

		// Both attach to this process's one gRPC server rather than each opening a listener, so
		// core reaches both over the single address it is configured with.
		regSvc := newRegistryService(cfg.CapabilitiesRegistrySyncInterval.Duration(),
			scfg.Logger.Named("capabilities registry"), reader, regORM, factories.PeerID, grpcSrv.Registrar())
		proxySvc := newProxyService(scfg.Logger.Named("proxy service"), grpcSrv.Registrar(), factories)
		// The dispatcher runs the real DON-to-DON work over the same rage connection, rather than
		// core running it and this process merely fronting it.
		dispatcherSvc := newDispatcherService(cfg.Dispatcher, scfg.Logger.Named("dispatcher"), factories, regSvc.CapabilitiesRegistry())
		// The server goes last: it starts serving only once the services registering on it have
		// started, and services.Engine starts sub-services in the order given.
		return []services.Service{proxySvc, regSvc, dispatcherSvc, grpcSrv}
	}, ocrDep, readerDep, dbDep, grpcDep)
}
