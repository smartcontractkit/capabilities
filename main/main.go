package main

import (
	"context"
	"embed"
	"log"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/db"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
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

	root := &cobra.Command{
		Use:   "main",
		Short: "P2P proxy for the CRE",
		Long: `Runs a single shared rage (libocr) peer and exposes it over gRPC so that
core can delegate its OCR networking (and, in future, DON-to-DON networking) to
this process. The peer's identity is loaded from the node's keystore (shared DB
via CL_DATABASE_URL, decrypted with CL_PASSWORD_KEYSTORE).

Provide exactly one networking mode: --listen-addresses to run a local libocr
peer, or --proxy-address to delegate to another proxy.`,
	}

	root.PersistentFlags().StringVar(&cfg.ProxyListenAddress, "proxy-listen-address", ":50051", "address the proxy gRPC server listens on")

	lggr, err := logger.New()
	if err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root, lggr)

	// The ocr dependency owns the libocr networking config (create vs proxy
	// mode) and wraps the database dependency it needs for the P2P identity and
	// OCR discoverer table.
	dbDep := db.Dependency(embeddedMigrations, migrationsTable)
	ocrDep := ocr.Dependency(lggr, dbDep, ocrDiscovererTable)

	return standalone.Run1(bootstrapper, func(ctx context.Context, factories standalone.Dependency[*ocr.Factories]) []services.Service {
		return []services.Service{newProxyService(cfg, lggr, factories)}
	}, ocrDep)
}
