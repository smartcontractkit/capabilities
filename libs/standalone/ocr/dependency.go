// Package ocr provides a standalone.BootstrapDependency that supplies the
// libocr rage networking factories (OCR endpoint, OCR3.1 endpoint, and
// DON-to-DON peer group) a standalone binary needs.
//
// It has two mutually exclusive modes, mirroring core's SingletonPeerWrapper:
//
//   - create: build a local libocr peer (networking.NewPeer) and expose its
//     factories. Requires --listen-addresses and uses the node's P2P identity
//     and OCR discoverer table from the database.
//   - proxy:  delegate rage networking to an out-of-process proxy at
//     --proxy-address, exposing proxy-client-backed factories instead of a
//     local peer.
//
// The two modes are wired as a cobra "one of" set: exactly one of
// --listen-addresses / --proxy-address may (and must) be provided.
package ocr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/libocr/networking"
	"github.com/smartcontractkit/libocr/networking/rageping"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	commonocr "github.com/smartcontractkit/chainlink-common/pkg/ocrcommon"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	"github.com/smartcontractkit/capabilities/libs/standalone"
)

// Factories bundles the libocr rage networking factories the caller serves or
// drives. It is produced by either the create or proxy mode; the caller does
// not need to know which. Close tears down the underlying peer (create mode) or
// proxy client connections (proxy mode).
type Factories struct {
	// OCR2Endpoint creates OCR2 BinaryNetworkEndpoints.
	OCR2Endpoint ocr2types.BinaryNetworkEndpointFactory
	// OCR3_1Endpoint creates OCR3.1 BinaryNetworkEndpoint2s.
	OCR3_1Endpoint ocr2types.BinaryNetworkEndpoint2Factory
	// PeerGroup creates DON-to-DON peer groups.
	PeerGroup creproxy.PeerGroupFactory

	closer io.Closer
}

// Close releases the underlying peer or proxy clients.
func (f *Factories) Close() error {
	if f == nil || f.closer == nil {
		return nil
	}
	return f.closer.Close()
}

// Dependency returns a standalone.BootstrapDependency that resolves the libocr
// Factories. It wraps the database dependency, which it uses to load the node's
// P2P identity (both modes) and the OCR discoverer database (create mode).
// discovererTable is the migration-created table backing p2p announcements.
func Dependency(lggr commonlogger.Logger, db standalone.BootstrapDependency[*sql.DB], discovererTable string) standalone.BootstrapDependency[*Factories] {
	// Wrap in OnceBootstrapper so Get (which creates the peer or proxy clients)
	// runs at most once even if several services resolve this dependency.
	return standalone.OnceBootstrapper[*Factories](&dependency{lggr: lggr, db: db, discovererTable: discovererTable})
}

type dependency struct {
	lggr            commonlogger.Logger
	db              standalone.BootstrapDependency[*sql.DB]
	discovererTable string

	// create-mode config
	listenAddresses    []string
	announceAddresses  []string
	deltaReconcile     time.Duration
	deltaDial          time.Duration
	incomingBufferSize int
	outgoingBufferSize int

	// proxy-mode config
	proxyAddress string
}

var _ standalone.BootstrapDependency[*Factories] = (*dependency)(nil)

func (d *dependency) AddCommands(cmd *cobra.Command) {
	// The database is a create/proxy-shared input (P2P identity, discoverer
	// table), so surface its flags too.
	d.db.AddCommands(cmd)

	f := cmd.PersistentFlags()
	// create-mode flags
	f.StringSliceVar(&d.listenAddresses, "listen-addresses", nil, "rage p2p V2 listen addresses (host:port); creates a local peer")
	f.StringSliceVar(&d.announceAddresses, "announce-addresses", nil, "rage p2p V2 announce addresses (host:port); defaults to the listen addresses")
	f.DurationVar(&d.deltaReconcile, "delta-reconcile", time.Minute, "rage p2p V2 delta reconcile interval")
	f.DurationVar(&d.deltaDial, "delta-dial", 5*time.Second, "rage p2p V2 minimum interval between dial attempts")
	f.IntVar(&d.incomingBufferSize, "incoming-buffer-size", 100, "per-remote incoming message buffer size")
	f.IntVar(&d.outgoingBufferSize, "outgoing-buffer-size", 100, "per-remote outgoing message buffer size")
	// proxy-mode flag
	f.StringVar(&d.proxyAddress, "proxy-address", "", "delegate rage networking to a proxy at this gRPC address instead of creating a local peer")

	// Exactly one mode: --listen-addresses (create) xor --proxy-address (proxy).
	cmd.MarkFlagsMutuallyExclusive("listen-addresses", "proxy-address")
	cmd.MarkFlagsOneRequired("listen-addresses", "proxy-address")
	// announce-addresses is optional in create mode (libocr defaults it to the
	// listen addresses) but is meaningless in proxy mode.
	cmd.MarkFlagsMutuallyExclusive("announce-addresses", "proxy-address")
}

func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (*Factories, error) {
	sqlDB, err := d.db.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}
	ds := sqlx.NewDb(sqlDB, "pgx")

	// Both modes use the node's own P2P identity so this process is the same
	// peer as the node it fronts.
	keyring, err := loadPeerKeyring(ctx, ds)
	if err != nil {
		return nil, err
	}
	peerID := ragetypes.PeerIDFromKeyring(keyring)

	if d.proxyAddress != "" {
		return d.proxyFactories(peerID)
	}
	return d.localFactories(ds, keyring, peerID)
}

// localFactories builds a real libocr peer and exposes its factories.
func (d *dependency) localFactories(ds *sqlx.DB, keyring ragetypes.PeerKeyring, peerID ragetypes.PeerID) (*Factories, error) {
	if len(d.listenAddresses) == 0 {
		return nil, errors.New("at least one --listen-addresses is required")
	}

	discovererDB := commonocr.NewDiscovererDatabase(ds, peerID.String(), d.discovererTable)

	d.lggr.Infow("Creating local p2p peer",
		"peerID", peerID.String(),
		"listenAddresses", d.listenAddresses,
		"announceAddresses", d.announceAddresses,
	)

	peer, err := networking.NewPeer(networking.PeerConfig{
		PeerKeyring:          keyring,
		Logger:               commonlogger.NewOCRWrapper(d.lggr, false, func(string) {}),
		V2ListenAddresses:    d.listenAddresses,
		V2AnnounceAddresses:  d.announceAddresses,
		V2DeltaReconcile:     d.deltaReconcile,
		V2DeltaDial:          d.deltaDial,
		V2DiscovererDatabase: discovererDB,
		V2EndpointConfig: networking.EndpointConfigV2{
			IncomingMessageBufferSize: d.incomingBufferSize,
			OutgoingMessageBufferSize: d.outgoingBufferSize,
		},
		MetricsRegisterer:            prometheus.DefaultRegisterer,
		LatencyMetricsServiceConfigs: rageping.DefaultConfigs(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create rage peer: %w", err)
	}

	return &Factories{
		OCR2Endpoint:   peer.OCR2BinaryNetworkEndpointFactory(),
		OCR3_1Endpoint: peer.OCR3_1BinaryNetworkEndpointFactory(),
		PeerGroup:      newNetworkingPeerGroupFactory(peer.PeerGroupFactory()),
		closer:         peer,
	}, nil
}

// proxyFactories delegates rage networking to an out-of-process proxy: no local
// peer is created; the factories are backed by proxy clients connected to
// d.proxyAddress. The node's raw peer ID is passed to the endpoint factories,
// as libocr compares it against the peer IDs in the OCR config.
func (d *dependency) proxyFactories(peerID ragetypes.PeerID) (*Factories, error) {
	endpointFactory, err := creproxy.NewProxyEndpointFactory(peerID.String(), d.proxyAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxy OCR endpoint factory: %w", err)
	}
	endpoint2Factory, err := creproxy.NewProxyEndpoint2Factory(peerID.String(), d.proxyAddress)
	if err != nil {
		_ = endpointFactory.Close()
		return nil, fmt.Errorf("failed to create proxy OCR3.1 endpoint factory: %w", err)
	}
	pgFactory, err := creproxy.NewProxyPeerGroupFactory(d.proxyAddress)
	if err != nil {
		_ = endpointFactory.Close()
		_ = endpoint2Factory.Close()
		return nil, fmt.Errorf("failed to create proxy peer group factory: %w", err)
	}

	d.lggr.Infow("Delegating rage networking to proxy", "proxyAddress", d.proxyAddress, "peerID", peerID.String())

	return &Factories{
		OCR2Endpoint:   endpointFactory,
		OCR3_1Endpoint: endpoint2Factory,
		PeerGroup:      pgFactory,
		closer:         multiCloser{endpointFactory, endpoint2Factory, pgFactory},
	}, nil
}

// multiCloser closes several io.Closers, returning the first error.
type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var err error
	for _, c := range m {
		if cerr := c.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}
