package ocr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/smartcontractkit/libocr/networking"
	"github.com/smartcontractkit/libocr/networking/rageping"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ocrcommon"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// Config is the configuration of a locally hosted libocr peer: the peer's own settings
// (ocrcommon.Config, shared with anything else that runs one) plus the keystore password.
//
// The password is this struct's rather than the shared one's because needing it is this
// framework's choice, not the peer's: the peer needs an identity, and this process happens to get
// one by unlocking the keystore in the database it shares with the node it fronts. An embedded
// instance derives its identity instead and resolves a dependency with no settings at all (see
// ForEmbedding), so neither this nor the listen addresses is `validate:"required"` - a rule that
// only holds for the form a real deployment resolves is checked when that form is resolved, and
// spelled out in the usage text since the generated docs can only show what a tag says.
type Config struct {
	// Embedded as a pointer so this struct adds to the shared peer settings rather than copying
	// them; inline so its fields sit alongside keystore-password under ocr.* instead of nesting.
	//
	//nolint:revive // struct-tag: "inline" is pkg/config/flags' squash option (Options.SquashTagOption), not a go-toml one
	*ocrcommon.Config `toml:",inline"`

	// KeystorePassword unlocks the keystore holding the P2P identity the peer announces under.
	// Typed as a SecretString so it redacts itself in logs, docs and generated example configs.
	KeystorePassword config.SecretString `usage:"password for the node keystore holding the shared P2P identity; required unless the identity is derived, as it is under embed"`

	// KeyBundleID names the OCR2 key bundle to sign with on behalf of the capabilities this
	// process signs for. Only needed when the node holds more than one.
	KeyBundleID string `usage:"OCR2 key bundle in the node keystore to sign with; only needed when the node holds more than one"`
}

// Host returns a standalone.BootstrapDependency that resolves the libocr Factories from a peer
// this process hosts itself. It wraps the database dependency, which it uses to load the node's
// P2P identity and to back the OCR discoverer table. discovererTable is the migration-created
// table holding p2p announcements.
//
// An embedded instance resolves neither: see ForEmbedding.
func Host(lggr commonlogger.Logger, db standalone.BootstrapDependency[*sql.DB], discovererTable string) standalone.BootstrapDependency[*RageFactories] {
	// Wrap in OnceBootstrapper so Get (which creates the peer) runs at most once even if several
	// services resolve this dependency.
	return standalone.OnceBootstrapper[*RageFactories](&hostDependency{
		lggr:            lggr,
		db:              db,
		discovererTable: discovererTable,
		cfg:             Config{Config: defaultPeerConfig()},
	})
}

type hostDependency struct {
	lggr            commonlogger.Logger
	db              standalone.BootstrapDependency[*sql.DB]
	discovererTable string

	cfg Config
}

var _ standalone.BootstrapDependency[*RageFactories] = (*hostDependency)(nil)

// Namespace groups the libocr networking settings under ocr.* (--ocr.listen-addresses,
// CRE_OCR_LISTEN_ADDRESSES).
func (d *hostDependency) Namespace() string { return "ocr" }

func (d *hostDependency) Config() any { return &d.cfg }

func (d *hostDependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{d.db}
}

// ForEmbedding returns the in-process form, which hosts no peer at all: an embedded instance's peers
// are goroutines beside it, so there is nothing for a rage peer to listen on, announce to or
// discover. None of this dependency's settings survive into it - see embedded.
func (d *hostDependency) ForEmbedding(i int) standalone.BootstrapDependency[*RageFactories] {
	return &embedded{lggr: d.lggr, index: i}
}

func (d *hostDependency) Get(ctx context.Context, cc standalone.CommonConfig) (*RageFactories, error) {
	if len(d.cfg.ListenAddresses) == 0 {
		return nil, errors.New("--ocr.listen-addresses is required to host a peer")
	}

	ds, keyring, peerID, err := d.keystoreIdentity(ctx, cc)
	if err != nil {
		return nil, err
	}

	// From the same key ring, unlocked once: this process is the only one given the password, so
	// it is the only one that can sign as this node - with its P2P key on the wire, and with its
	// OCR keys for whoever it signs on behalf of.
	bundle, err := loadOCR2Bundle(ctx, ds, string(d.cfg.KeystorePassword), d.cfg.KeyBundleID)
	if err != nil {
		return nil, err
	}

	factories, err := d.localFactories(ds, keyring, peerID)
	if err != nil {
		return nil, err
	}
	factories.OCR2 = bundle
	// Held here, so an oracle in this process signs with the same bundle this serves to oracles
	// elsewhere rather than going out over gRPC to reach a key it already has.
	onchain, err := ocr2key.NewOCR3Keyring(evmFamily, bundle)
	if err != nil {
		return nil, err
	}
	// The bundle is already an offchain keyring, so only the onchain half is adapted.
	factories.Keyrings = Keyrings{Offchain: bundle, Onchain: onchain}
	return factories, nil
}

// keystoreIdentity loads the node's P2P identity from the keystore in the database this process
// shares with the node, so this process is the same peer as the node it fronts. The returned
// sqlx.DB is the same connection, which localFactories also needs for the discoverer table.
//
// Only a process hosting a peer does this: it is the one that needs the private key, and so the
// only one that is given the password to it.
func (d *hostDependency) keystoreIdentity(ctx context.Context, cc standalone.CommonConfig) (*sqlx.DB, ragetypes.PeerKeyring, ragetypes.PeerID, error) {
	if d.cfg.KeystorePassword == "" {
		return nil, nil, ragetypes.PeerID{}, errors.New("--ocr.keystore-password is required to unlock the node's P2P identity")
	}

	sqlDB, err := d.db.Get(ctx, cc)
	if err != nil {
		return nil, nil, ragetypes.PeerID{}, fmt.Errorf("failed to get database: %w", err)
	}
	ds := sqlx.NewDb(sqlDB, "pgx")

	keyring, err := loadPeerKeyring(ctx, ds, string(d.cfg.KeystorePassword))
	if err != nil {
		return nil, nil, ragetypes.PeerID{}, err
	}
	return ds, keyring, ragetypes.PeerIDFromKeyring(keyring), nil
}

// localFactories builds a real libocr peer and exposes its factories.
func (d *hostDependency) localFactories(ds *sqlx.DB, keyring ragetypes.PeerKeyring, peerID ragetypes.PeerID) (*RageFactories, error) {
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

	return &RageFactories{
		OCRFactories: OCRFactories{
			OCR2Endpoint:   peer.OCR2BinaryNetworkEndpointFactory(),
			OCR3_1Endpoint: peer.OCR3_1BinaryNetworkEndpointFactory(),
			PeerID:         peerID,
			closer:         peer,
		},
		PeerGroup: peer.PeerGroupFactory(),
		Keyring:   keyring,
	}, nil
}
