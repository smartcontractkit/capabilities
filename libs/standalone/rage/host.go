package rage

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

	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ocrcommon"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// Config is the configuration of a locally hosted libocr peer: the peer's own settings
// (ocrcommon.Config), and nothing else.
//
// The listen addresses are not `validate:"required"`: an embedded instance has no network to listen
// on and resolves a dependency with no settings at all (see ForEmbedding), so a rule that only holds
// for the form a real deployment resolves is checked when that form is resolved, and spelled out in
// the usage text since the generated docs can only show what a tag says.
type Config struct {
	// Embedded as a pointer so this struct adds to the shared peer settings rather than copying
	// them; inline so its fields sit under ocr.* rather than nesting.
	//
	//nolint:revive // struct-tag: "inline" is pkg/config/flags' squash option (Options.SquashTagOption), not a go-toml one
	*ocrcommon.Config `toml:",inline"`
}

// Host returns a standalone.BootstrapDependency that resolves the libocr Factories from a peer this
// process hosts itself.
//
// keyring is the identity the peer announces under, taken as a dependency because a key is not this
// package's business: whoever holds the node's keys resolves it, and this listens and dials with it.
// db backs the OCR discoverer table, whose name discovererTable is - the migration that made it
// belongs to the binary, not to this.
//
// An embedded instance resolves neither: see ForEmbedding.
func Host(
	lggr commonlogger.Logger,
	db standalone.BootstrapDependency[*sql.DB],
	discovererTable string,
	keyring standalone.BootstrapDependency[ragetypes.PeerKeyring],
) standalone.BootstrapDependency[*Factories] {
	// Wrap in OnceBootstrapper so Get (which creates the peer) runs at most once even if several
	// services resolve this dependency.
	return standalone.OnceBootstrapper[*Factories](&hostDependency{
		lggr:            lggr,
		db:              db,
		discovererTable: discovererTable,
		keyring:         keyring,
		cfg:             Config{Config: defaultPeerConfig()},
	})
}

type hostDependency struct {
	lggr            commonlogger.Logger
	db              standalone.BootstrapDependency[*sql.DB]
	discovererTable string
	keyring         standalone.BootstrapDependency[ragetypes.PeerKeyring]

	cfg Config
}

var _ standalone.BootstrapDependency[*Factories] = (*hostDependency)(nil)

// Namespace groups the libocr networking settings under ocr.* (--ocr.listen-addresses,
// CRE_OCR_LISTEN_ADDRESSES) - the same namespace the delegating form uses. A binary hosts a peer or
// delegates to one, never both, so the names never meet, and an operator moving a deployment from
// one to the other keeps the settings it still has a use for.
func (d *hostDependency) Namespace() string { return "ocr" }

func (d *hostDependency) Config() any { return &d.cfg }

func (d *hostDependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{d.db, d.keyring}
}

// ForEmbedding returns the in-process form, which hosts no peer at all: an embedded instance's peers
// are goroutines beside it, so there is nothing for a rage peer to listen on, announce to or
// discover. None of this dependency's settings survive into it - see embedded.
func (d *hostDependency) ForEmbedding(i, _ int) standalone.BootstrapDependency[*Factories] {
	return &embedded{lggr: d.lggr, index: i}
}

func (d *hostDependency) Get(ctx context.Context, cc standalone.CommonConfig) (*Factories, error) {
	if len(d.cfg.ListenAddresses) == 0 {
		return nil, errors.New("--ocr.listen-addresses is required to host a peer")
	}

	keyring, err := d.keyring.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get the peer keyring: %w", err)
	}

	sqlDB, err := d.db.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	// The peer ID libocr keys everything by, derived from the same public half the peer announces.
	return d.localFactories(sqlx.NewDb(sqlDB, "pgx"), keyring, ragetypes.PeerIDFromKeyring(keyring))
}

// localFactories builds a real libocr peer and exposes its factories.
func (d *hostDependency) localFactories(ds *sqlx.DB, keyring ragetypes.PeerKeyring, peerID ragetypes.PeerID) (*Factories, error) {
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
		Factories: ocr.NewFactories(
			peer.OCR2BinaryNetworkEndpointFactory(),
			peer.OCR3_1BinaryNetworkEndpointFactory(),
			peerID,
			peer,
		),
		PeerGroup: peer.PeerGroupFactory(),
		Keyring:   keyring,
	}, nil
}
