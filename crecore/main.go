package main

import (
	"context"
	"embed"
	"log"
	"time"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/db"
	"github.com/smartcontractkit/capabilities/libs/standalone/evm"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"

	"github.com/smartcontractkit/capabilities/crecore/registry"

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
	cfg := &Config{}

	var (
		registryAddress      string
		registrySyncInterval time.Duration
	)

	root := &cobra.Command{
		Use:   "main",
		Short: "P2P proxy for the CRE",
		Long: `Runs a single shared rage (libocr) peer and exposes it over gRPC so that
core can delegate its OCR networking (and, in future, DON-to-DON networking) to
this process. The peer's identity is loaded from the node's keystore (shared DB
via CL_DATABASE_URL, decrypted with CL_PASSWORD_KEYSTORE).

Provide exactly one networking mode: --listen-addresses to run a local libocr
peer, or --proxy-address to delegate to another proxy.

It also serves the CapabilitiesRegistry on that same gRPC address, read directly
from chain with an EVM client (no relayer). Core uses that in place of its own
registrysyncer whenever it is delegating rage networking to this process, so
--capabilities-registry-address is required: this process running is what enables
the registry, and core does not start without it.`,
	}

	root.PersistentFlags().StringVar(&cfg.ProxyListenAddress, "proxy-listen-address", ":50051", "address the proxy gRPC server listens on")
	root.PersistentFlags().StringVar(&registryAddress, "capabilities-registry-address", "", "on-chain CapabilitiesRegistry (v2) contract address (required)")
	root.PersistentFlags().DurationVar(&registrySyncInterval, "capabilities-registry-sync-interval", registry.DefaultSyncInterval, "how often the on-chain registry is re-read")
	if err := root.MarkPersistentFlagRequired("capabilities-registry-address"); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root, standalone.WithOtelViews(metricViews()))
	lggr := bootstrapper.Logger()

	// The ocr dependency owns the libocr networking config (create vs proxy
	// mode) and wraps the database dependency it needs for the P2P identity and
	// OCR discoverer table.
	dbDep := db.Dependency(embeddedMigrations, migrationsTable)
	ocrDep := ocr.Dependency(lggr.Named("OCR"), dbDep, ocrDiscovererTable)
	// The registry always runs, so the EVM client is always resolved and the
	// --evm-* flags are as required in practice as the registry address is.
	evmDep := evm.Dependency(lggr.Named("EVM"))

	return standalone.Run2(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		factories standalone.Dependency[*ocr.Factories],
		evmClient standalone.Dependency[evmclient.Client],
	) []services.Service {
		regSvc := newRegistryService(registryAddress, registrySyncInterval,
			scfg.Logger.Named("capabilities registry"), evmClient, factories)
		// The registry attaches to the proxy's gRPC server, so core reaches both
		// over the single --proxy-listen-address it already configures.
		proxySvc := newProxyService(cfg, scfg.Logger.Named("proxy service"), factories, regSvc.Register)
		return []services.Service{proxySvc, regSvc}
	}, ocrDep, evmDep)
}
