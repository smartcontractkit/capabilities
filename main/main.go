package main

import (
	"context"
	"database/sql"
	"embed"
	"log"
	"time"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/db"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

const migrationsTable = "proxy_migrations"

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
this process. The peer's identity is set via the CL_P2P_PRIVATE_KEY environment
variable (hex-encoded ed25519 seed or private key), and its database connection
via CL_DATABASE_URL.`,
	}

	flags := root.PersistentFlags()
	flags.StringVar(&cfg.ProxyListenAddress, "proxy-listen-address", ":50051", "address the proxy gRPC server listens on")
	flags.StringSliceVar(&cfg.ListenAddresses, "listen-addresses", nil, "rage p2p V2 listen addresses (host:port); at least one required")
	flags.StringSliceVar(&cfg.AnnounceAddresses, "announce-addresses", nil, "rage p2p V2 announce addresses (host:port); defaults to the listen addresses")
	flags.DurationVar(&cfg.DeltaReconcile, "delta-reconcile", time.Minute, "rage p2p V2 delta reconcile interval")
	flags.DurationVar(&cfg.DeltaDial, "delta-dial", 5*time.Second, "rage p2p V2 minimum interval between dial attempts")
	flags.IntVar(&cfg.IncomingBufferSize, "incoming-buffer-size", 100, "per-remote incoming message buffer size")
	flags.IntVar(&cfg.OutgoingBufferSize, "outgoing-buffer-size", 100, "per-remote outgoing message buffer size")

	lggr, err := logger.New()
	if err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root, lggr)

	return standalone.Run1(bootstrapper, func(ctx context.Context, dbDep standalone.Dependency[*sql.DB]) []services.Service {
		return []services.Service{newProxyService(cfg, lggr, dbDep)}
	}, db.Dependency(embeddedMigrations, migrationsTable))
}
