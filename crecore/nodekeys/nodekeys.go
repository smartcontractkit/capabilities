// Package nodekeys is this process's keys: the node's, unlocked from the keystore
// they share, or - for an embedded run, which has no node - derived from the
// instance index.
//
// It lives here rather than in libs because this is the only binary that holds
// keys. Everything else in this framework is on the other side of that: a
// capability asks this process to sign (see libs/standalone/keystore), and what it
// gets is a signature, never a key.
//
// Nothing is looked up until it is asked for. Unlocking the store says the
// password is right and the database is reachable; which keys are in it is only
// discovered by whoever wants one, so a node with no OCR key still serves its peer
// and its chain accounts, and says what is missing to the caller that needed it.
package nodekeys

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/jmoiron/sqlx"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/keystore"
	"github.com/smartcontractkit/chainlink-common/keystore/ocr2offchain"
	"github.com/smartcontractkit/chainlink-common/keystore/pgstore"
	"github.com/smartcontractkit/chainlink-common/keystore/ragep2p"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// namespace groups these settings under keystore.*: they are about keys, not about
// the peer that happens to use one of them.
const namespace = "keystore"

// evmFamily is the signing family the OCR keyring announces under. The
// configurations this process signs for are the capabilities registry's, which
// lists members by family, and this node signs as an EVM one.
const evmFamily = ocr.EVMFamily

// Config is which keystore, and which keys in it.
//
// The keystore names keys rather than typing them, so which key is the peer is a
// convention. These are the conventional names, and the same ones the node's own
// bootstrap copies its keys under; a deployment that chose otherwise says so here.
//
// Nothing is `validate:"required"`: an embedded instance derives its keys and is
// told none of this, so the rules are checked when the configured form resolves.
type Config struct {
	// Password unlocks the keystore. Typed as a SecretString so it redacts itself in
	// logs, docs and generated example configs.
	Password commonconfig.SecretString `usage:"password for the node's keystore, which holds its P2P identity, OCR keys and chain keys; required unless the keys are derived, as they are under embed"`

	// Name is the keystore's row in the shared database. A database holds one per
	// process that has keys of its own, so this names the node's.
	Name string `usage:"name of the node's keystore in the shared database"`

	// PeerKey and OCRKey are the keys in it this process uses.
	PeerKey string `usage:"name of the node's rage P2P key in its keystore"`
	OCRKey  string `usage:"name of the node's OCR keyring in its keystore, whose keys this process signs rounds with"`
}

// Defaults are the conventional names, used for whatever is left unnamed.
var Defaults = Config{Name: "node", PeerKey: "p2p", OCRKey: "ocr2"}

func (c Config) withDefaults() Config {
	if c.Name == "" {
		c.Name = Defaults.Name
	}
	if c.PeerKey == "" {
		c.PeerKey = Defaults.PeerKey
	}
	if c.OCRKey == "" {
		c.OCRKey = Defaults.OCRKey
	}
	return c
}

// Keys is what this process can sign with, asked one key at a time.
//
// Each method looks its key up when it is called and remembers what it found, so a
// key nothing uses is never read and a key that is not there is reported to the
// caller that wanted it rather than to everything at startup.
type Keys interface {
	// Peer is the identity the rage peer announces under. Other DON members expect
	// this node's peer ID at this process's address.
	Peer(ctx context.Context) (ragetypes.PeerKeyring, error)

	// OCR is the identity this process signs rounds with on behalf of oracles that
	// hold no keys: the offchain half for every protocol message, the onchain half for
	// the report at the end.
	OCR(ctx context.Context) (ocr.Keyrings, error)

	// Chain is what a chain capability transmits through, addressed by account name.
	// The store types its own keys and this interface does not, so which accounts
	// exist is a question the store answers per call - which is why this one needs no
	// context and cannot fail.
	//
	// Nil for an embedded run, which has no node to transmit as.
	Chain() core.Keystore
}

// Dependency returns this process's keys, over the keystore in the database it
// shares with the node.
//
// Resolving it unlocks that keystore - which says the password is right - and
// nothing more: see Keys.
func Dependency(lggr commonlogger.Logger, db standalone.BootstrapDependency[*sql.DB]) standalone.BootstrapDependency[Keys] {
	// Wrapped so the keystore is unlocked at most once however many services resolve
	// this: unlocking it is scrypt, which is slow on purpose.
	return standalone.OnceBootstrapper[Keys](&dependency{lggr: lggr, db: db, cfg: Defaults})
}

type dependency struct {
	lggr commonlogger.Logger
	db   standalone.BootstrapDependency[*sql.DB]
	cfg  Config
}

var _ standalone.BootstrapDependency[Keys] = (*dependency)(nil)

func (d *dependency) Namespace() string { return namespace }

func (d *dependency) Config() any { return &d.cfg }

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{d.db}
}

// ForEmbedding returns the derived form: an embedded run has no node, and so no
// keystore to unlock and no password to unlock it with. See derived.
func (d *dependency) ForEmbedding(i, _ int) standalone.BootstrapDependency[Keys] {
	return &embedded{lggr: d.lggr, index: i}
}

func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (Keys, error) {
	cfg := d.cfg.withDefaults()
	if cfg.Password == "" {
		return nil, errors.New("--keystore.password is required to unlock the node's keys")
	}

	database, err := d.db.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	ks, err := keystore.LoadKeystore(ctx, pgstore.NewStorage(sqlx.NewDb(database, "pgx"), cfg.Name), string(cfg.Password))
	if err != nil {
		return nil, fmt.Errorf("failed to unlock the keystore %q: %w", cfg.Name, err)
	}

	d.lggr.Infow("Unlocked the node's keystore", "keystore", cfg.Name)

	return &nodeKeys{keystore: ks, cfg: cfg}, nil
}

// nodeKeys is the node's unlocked keystore, read one key at a time.
type nodeKeys struct {
	keystore keystore.Keystore
	cfg      Config

	peer once[ragetypes.PeerKeyring]
	ocr  once[ocr.Keyrings]
}

var _ Keys = (*nodeKeys)(nil)

func (k *nodeKeys) Peer(ctx context.Context) (ragetypes.PeerKeyring, error) {
	return k.peer.do(func() (ragetypes.PeerKeyring, error) {
		keyrings, err := ragep2p.GetPeerKeyrings(ctx, k.keystore, []string{k.cfg.PeerKey})
		if err != nil {
			return nil, fmt.Errorf("failed to read the peer key %q: %w", k.cfg.PeerKey, err)
		}
		if len(keyrings) != 1 {
			return nil, fmt.Errorf("expected one peer key named %q, found %d", k.cfg.PeerKey, len(keyrings))
		}
		return keyrings[0], nil
	})
}

func (k *nodeKeys) OCR(ctx context.Context) (ocr.Keyrings, error) {
	return k.ocr.do(func() (ocr.Keyrings, error) {
		offchain, err := k.offchain(ctx)
		if err != nil {
			return ocr.Keyrings{}, err
		}
		onchain, err := newOnchainKeyring(ctx, k.keystore, k.cfg.OCRKey, evmFamily)
		if err != nil {
			return ocr.Keyrings{}, err
		}
		return ocr.Keyrings{Offchain: offchain, Onchain: onchain}, nil
	})
}

func (k *nodeKeys) offchain(ctx context.Context) (ocrtypes.OffchainKeyring, error) {
	keyrings, err := ocr2offchain.GetOCR2OffchainKeyrings(ctx, k.keystore, []string{k.cfg.OCRKey})
	if err != nil {
		return nil, fmt.Errorf("failed to read the OCR offchain keys %q: %w", k.cfg.OCRKey, err)
	}
	if len(keyrings) != 1 {
		return nil, fmt.Errorf("expected one OCR offchain keyring named %q, found %d", k.cfg.OCRKey, len(keyrings))
	}
	return keyrings[0], nil
}

// Chain is the whole store: which accounts it holds is what a caller asking for
// one finds out, so there is nothing to look up here.
func (k *nodeKeys) Chain() core.Keystore { return keystore.NewCoreKeystore(k.keystore) }

// embedded is one embedded instance's keys, derived from its index: there is no
// node behind an embedded run, so there is nothing to borrow an identity from.
//
// Deriving rather than generating is what makes such a run usable: the peer IDs
// and public keys are known before it starts, so the configuration its instances
// form can be computed from the instance count alone.
type embedded struct {
	lggr  commonlogger.Logger
	index int
}

var _ standalone.BootstrapDependency[Keys] = (*embedded)(nil)

func (d *embedded) Namespace() string { return namespace }

// Config is nothing at all: an embedded instance cannot be told a keystore
// password, and there is no keystore to name.
func (d *embedded) Config() any { return nil }

func (d *embedded) Dependencies() []standalone.BootstrapCommand { return nil }

func (d *embedded) ForEmbedding(i, _ int) standalone.BootstrapDependency[Keys] {
	return &embedded{lggr: d.lggr, index: i}
}

func (d *embedded) Get(context.Context, standalone.CommonConfig) (Keys, error) {
	return &derivedKeys{index: d.index}, nil
}

// derivedKeys are instance i's, computed from i when asked for.
type derivedKeys struct {
	index int

	peer once[ragetypes.PeerKeyring]
	ocr  once[ocr.Keyrings]
}

var _ Keys = (*derivedKeys)(nil)

func (k *derivedKeys) Peer(context.Context) (ragetypes.PeerKeyring, error) {
	return k.peer.do(func() (ragetypes.PeerKeyring, error) { return ocr.DeterministicKeyring(k.index) })
}

func (k *derivedKeys) OCR(context.Context) (ocr.Keyrings, error) {
	return k.ocr.do(func() (ocr.Keyrings, error) { return ocr.EmbeddedKeyrings(k.index) })
}

// Chain is nil: an embedded instance transmits as nobody, so it has no account to
// lend a capability.
func (k *derivedKeys) Chain() core.Keystore { return nil }

// once resolves a key the first time it is wanted and remembers the answer,
// including a failure: a key that is not in the store will not appear between one
// call and the next, and re-reading it per signature would put scrypt on the path
// of every round.
type once[T any] struct {
	once  sync.Once
	value T
	err   error
}

func (o *once[T]) do(resolve func() (T, error)) (T, error) {
	o.once.Do(func() { o.value, o.err = resolve() })
	return o.value, o.err
}
