package main

import (
	"context"
	"embed"
	"log"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/db"
	"github.com/smartcontractkit/capabilities/libs/standalone/evm"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	evmclient "github.com/smartcontractkit/chainlink-evm/pkg/client"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

const migrationsTable = "proxy_migrations"

// ocrDiscovererTable is the table backing OCR p2p announcements. Must match the
// CREATE TABLE in migrations/0001_*.sql.
const ocrDiscovererTable = "proxy_ocr_discoverer_announcements"

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

It also serves the CapabilitiesRegistry on that same gRPC address, read directly
from chain with an EVM client (no relayer). Core uses that in place of its own
registrysyncer whenever it is delegating rage networking to this process, which
is why the registry address is required: this process running is what enables the
registry, and core does not start without it.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file;
run "docs" to write the full reference to docs/CONFIG.md.`,
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	if err := flags.RegisterCommandFlags(root, &cfg, flags.DefaultTOMLOptions("CRE", "CL")); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root, standalone.WithOtelViews(metricViews()))
	lggr := bootstrapper.Logger()

	// The ocr dependency owns the libocr networking config (create vs proxy
	// mode, and the keystore password that unlocks the peer identity) and wraps
	// the database dependency it needs for that identity and the OCR discoverer
	// table.
	dbDep := db.Dependency(embeddedMigrations, migrationsTable)
	ocrDep := ocr.Dependency(lggr.Named("OCR"), dbDep, ocrDiscovererTable)
	// The registry always runs, so the EVM client is always resolved and the
	// evm settings are as required in practice as the registry address is.
	evmDep := evm.Dependency(lggr.Named("EVM"))

	return standalone.Run2(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		factories standalone.Dependency[*ocr.Factories],
		evmClient standalone.Dependency[evmclient.Client],
	) []services.Service {
		regSvc := newRegistryService(cfg.CapabilitiesRegistryAddress, cfg.CapabilitiesRegistrySyncInterval.Duration(),
			scfg.Logger.Named("capabilities registry"), evmClient, factories)
		// The registry attaches to the proxy's gRPC server, so core reaches both
		// over the single --proxy-listen-address it already configures.
		proxySvc := newProxyService(&cfg, scfg.Logger.Named("proxy service"), factories, regSvc.Register)
		return []services.Service{proxySvc, regSvc}
	}, ocrDep, evmDep)
}
