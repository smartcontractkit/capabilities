// Package ocr provides a standalone.BootstrapDependency that supplies the
// libocr rage networking factories (OCR endpoint, OCR3.1 endpoint, and
// DON-to-DON peer group) a standalone binary needs.
//
// It has two mutually exclusive modes, mirroring core's SingletonPeerWrapper:
//
//   - create: build a local libocr peer (networking.NewPeer) and expose its
//     factories. Requires --ocr.listen-addresses and uses the node's P2P identity
//     and OCR discoverer table from the database.
//   - proxy:  delegate rage networking to an out-of-process proxy at
//     --ocr.proxy-address, exposing proxy-client-backed factories instead of a
//     local peer.
//
// Exactly one of --ocr.listen-addresses / --ocr.proxy-address may (and must) be
// provided; the peer's own settings come from chainlink-common's ocrcommon.Config,
// which this package's Config embeds and adds proxy mode to.
package ocr

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/smartcontractkit/libocr/networking"
	"github.com/smartcontractkit/libocr/networking/rageping"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ocrcommon"
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

	// PeerID is the node's rage P2P identity, loaded from the keystore. Both
	// modes resolve it, and consumers other than libocr need it: the on-chain
	// CapabilitiesRegistry keys node records by peer ID, so anything reading
	// registry metadata must know which node it is.
	PeerID ragetypes.PeerID

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
	return standalone.OnceBootstrapper[*Factories](&dependency{lggr: lggr, db: db, discovererTable: discovererTable, cfg: defaultConfig()})
}

// Config is the libocr networking configuration: the settings of the local peer itself
// (ocrcommon.Config, shared with anything else that runs one), plus proxy mode, which is this
// framework's own alternative to running that peer.
//
// The two modes are expressed as validator tags rather than cobra's
// MarkFlagsMutuallyExclusive/MarkFlagsOneRequired: those only inspect whether a flag was
// literally typed on the command line, so they would reject a mode that was selected via a
// config file or env var. required_without/excluded_with are checked against the decoded
// values instead, so exactly-one-of holds no matter which source supplied it. Both rules sit on
// ProxyAddress, the field this struct owns: a rule on the embedded ListenAddresses could not
// name a field outside its own struct.
type Config struct {
	// Embedded as a pointer so this struct adds proxy mode to the shared peer settings rather
	// than copying them; inline so its fields sit alongside proxy-address under ocr.* instead
	// of nesting.
	*ocrcommon.Config `toml:",inline"`

	// proxy-mode config
	ProxyAddress string `usage:"delegate rage networking to a proxy at this gRPC address instead of creating a local peer" validate:"required_without=ListenAddresses,excluded_with=ListenAddresses"`
}

// defaultConfig is the instance the flags are bound to and decoded into, so an unset setting
// keeps the value it is given here. The embedded pointer is fresh per call rather than a shared
// package-level value, so two dependencies can never decode into the same peer settings. Proxy
// mode has no default: an unset proxy address is what selects a local peer.
func defaultConfig() Config {
	return Config{Config: &ocrcommon.Config{
		DeltaReconcile:     *config.MustNewDuration(time.Minute),
		DeltaDial:          *config.MustNewDuration(5 * time.Second),
		IncomingBufferSize: 100,
		OutgoingBufferSize: 100,
	}}
}

type dependency struct {
	lggr            commonlogger.Logger
	db              standalone.BootstrapDependency[*sql.DB]
	discovererTable string

	cfg Config
}

var _ standalone.BootstrapDependency[*Factories] = (*dependency)(nil)

// Namespace groups the libocr networking settings under ocr.* (--ocr.listen-addresses,
// CRE_OCR_LISTEN_ADDRESSES).
func (d *dependency) Namespace() string { return "ocr" }

func (d *dependency) Config() any {
	return &d.cfg
}

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{d.db}
}

func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (*Factories, error) {
	sqlDB, err := d.db.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}
	ds := sqlx.NewDb(sqlDB, "pgx")

	// Both modes use the node's own P2P identity so this process is the same
	// peer as the node it fronts.
	keyring, err := loadPeerKeyring(ctx, ds, string(d.cfg.KeystorePassword))
	if err != nil {
		return nil, err
	}
	peerID := ragetypes.PeerIDFromKeyring(keyring)

	if d.cfg.ProxyAddress != "" {
		return d.proxyFactories(peerID)
	}
	return d.localFactories(ds, keyring, peerID)
}

// localFactories builds a real libocr peer and exposes its factories.
func (d *dependency) localFactories(ds *sqlx.DB, keyring ragetypes.PeerKeyring, peerID ragetypes.PeerID) (*Factories, error) {
	discovererDB := ocrcommon.NewDiscovererDatabase(ds, peerID.String(), d.discovererTable)

	d.lggr.Infow("Creating local p2p peer",
		"peerID", peerID.String(),
		"listenAddresses", d.cfg.ListenAddresses,
		"announceAddresses", d.cfg.AnnounceAddresses,
	)

	peer, err := networking.NewPeer(networking.PeerConfig{
		PeerKeyring:          keyring,
		Logger:               commonlogger.NewOCRWrapper(d.lggr, false, func(string) {}),
		V2ListenAddresses:    d.cfg.ListenAddresses,
		V2AnnounceAddresses:  d.cfg.AnnounceAddresses,
		V2DeltaReconcile:     d.cfg.DeltaReconcile.Duration(),
		V2DeltaDial:          d.cfg.DeltaDial.Duration(),
		V2DiscovererDatabase: discovererDB,
		V2EndpointConfig: networking.EndpointConfigV2{
			IncomingMessageBufferSize: d.cfg.IncomingBufferSize,
			OutgoingMessageBufferSize: d.cfg.OutgoingBufferSize,
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
		PeerID:         peerID,
		closer:         peer,
	}, nil
}

// proxyFactories delegates rage networking to an out-of-process proxy: no local
// peer is created; the factories are backed by proxy clients connected to
// d.cfg.ProxyAddress. The node's raw peer ID is passed to the endpoint factories,
// as libocr compares it against the peer IDs in the OCR config.
func (d *dependency) proxyFactories(peerID ragetypes.PeerID) (*Factories, error) {
	endpointFactory, err := creproxy.NewProxyEndpointFactory(peerID.String(), d.cfg.ProxyAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxy OCR endpoint factory: %w", err)
	}
	endpoint2Factory, err := creproxy.NewProxyEndpoint2Factory(peerID.String(), d.cfg.ProxyAddress)
	if err != nil {
		_ = endpointFactory.Close()
		return nil, fmt.Errorf("failed to create proxy OCR3.1 endpoint factory: %w", err)
	}
	pgFactory, err := creproxy.NewProxyPeerGroupFactory(d.cfg.ProxyAddress)
	if err != nil {
		_ = endpointFactory.Close()
		_ = endpoint2Factory.Close()
		return nil, fmt.Errorf("failed to create proxy peer group factory: %w", err)
	}

	d.lggr.Infow("Delegating rage networking to proxy", "proxyAddress", d.cfg.ProxyAddress, "peerID", peerID.String())

	return &Factories{
		OCR2Endpoint:   endpointFactory,
		OCR3_1Endpoint: endpoint2Factory,
		PeerGroup:      pgFactory,
		PeerID:         peerID,
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
