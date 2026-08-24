package chain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/scylladb/go-reflectx"

	"github.com/smartcontractkit/chainlink-evm/pkg/chains/legacyevm"
	evmclient "github.com/smartcontractkit/chainlink-evm/pkg/client"
	"github.com/smartcontractkit/chainlink-evm/pkg/config/toml"
	"github.com/smartcontractkit/chainlink-evm/pkg/keys"
	"github.com/smartcontractkit/chainlink-evm/pkg/relay"

	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"
	commontypes "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/mailbox"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// Config is what the chain needs beyond the RPC connection and the database: the
// few settings a deployment chooses rather than inherits.
//
// Everything else comes from chainlink-evm's own defaults for the chain in
// question (toml.Defaults), the same set a node starts from. Exposing all of it
// would mean a flag per field of a configuration that exists to be defaulted; a
// deployment that needs one of those is a reason to add it here, one at a time.
type Config struct {
	// LogPollInterval is how often the log poller asks for new blocks. It is here
	// rather than defaulted because it is the setting a deployment feels: it is the
	// floor on how quickly a log trigger can fire.
	LogPollInterval commonconfig.Duration `usage:"how often the log poller reads new blocks, which is the floor on log trigger latency"`

	// FinalityDepth and FinalityTagEnabled decide when this capability calls a block
	// final, which is what a workflow asking for finalized state is answered from.
	FinalityTagEnabled bool   `usage:"use the finalized block tag instead of a finality depth"`
	FinalityDepth      uint32 `usage:"blocks behind the head that count as final, used when --chain.finality-tag-enabled=false"`

	// NoNewHeadsThreshold is how long without a head before the chain is declared
	// unreachable. Zero leaves chainlink-evm's own answer for this chain.
	NoNewHeadsThreshold commonconfig.Duration `usage:"how long without a new head before the chain is treated as unreachable; 0 keeps the chain's own default"`

	// TransactionsEnabled says whether this process may write to the chain. A
	// capability that only reads and watches logs is better off saying so, since a
	// transaction manager it never uses is still a transaction manager watching
	// heads and holding rows.
	TransactionsEnabled bool `usage:"run the transaction manager, for a capability that writes to the chain"`
}

// defaultConfig is what the settings are bound to, so an unset flag keeps the
// value here rather than a zero.
var defaultConfig = Config{
	LogPollInterval:     *commonconfig.MustNewDuration(time.Second),
	FinalityTagEnabled:  true,
	TransactionsEnabled: true,
}

// Dependency returns the running chain: the client's pool, a head tracker, a log
// poller and - when this capability writes - a transaction manager.
//
// Everything it is built from is a dependency rather than a default: the client
// says where the chain is, the database says where its state goes, and the
// keystore says who signs. This package chooses none of them, so a binary can
// point them wherever it likes and there is one place per thing to look.
//
// The returned chain is a service: the caller starts it, and starting it is what
// starts polling.
func Dependency(
	lggr logger.Logger,
	client standalone.BootstrapDependency[evmclient.Client],
	db standalone.BootstrapDependency[*sql.DB],
	ks standalone.BootstrapDependency[core.Keystore],
	account *string,
) standalone.BootstrapDependency[legacyevm.Chain] {
	cfg := defaultConfig
	// Wrapped so one chain is built however many services resolve this: they are
	// meant to share a log poller and a transaction manager, not run one each.
	return standalone.OnceBootstrapper[legacyevm.Chain](&dependency{
		lggr:    lggr,
		client:  client,
		db:      db,
		ks:      ks,
		account: account,
		// Held by pointer so the form an embedded instance is built from decodes into
		// the same settings the flags were bound to rather than a copy of them.
		cfg: &cfg,
	})
}

type dependency struct {
	lggr   logger.Logger
	client standalone.BootstrapDependency[evmclient.Client]
	db     standalone.BootstrapDependency[*sql.DB]
	ks     standalone.BootstrapDependency[core.Keystore]

	// account is which of the keystore's accounts is this chain's. See narrowed.
	account *string

	cfg *Config
}

var _ standalone.BootstrapDependency[legacyevm.Chain] = (*dependency)(nil)

// Namespace groups these under chain.*, apart from the client's evm.* settings:
// one says where the chain is, the other how this capability follows it.
func (d *dependency) Namespace() string { return "chain" }

func (d *dependency) Config() any { return d.cfg }

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{d.client, d.db, d.ks}
}

// ForEmbedding embeds what this is built from: instance i's database, which is a
// schema of its own and so a log poller and a transaction manager of its own, and
// instance i's keys. The chain settings are shared, because the instances of one
// run are nodes on one chain and follow it the same way.
func (d *dependency) ForEmbedding(i, instances int) standalone.BootstrapDependency[legacyevm.Chain] {
	embedded := *d
	embedded.client = d.client.ForEmbedding(i, instances)
	embedded.db = d.db.ForEmbedding(i, instances)
	embedded.ks = d.ks.ForEmbedding(i, instances)
	return standalone.OnceBootstrapper[legacyevm.Chain](&embedded)
}

func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (legacyevm.Chain, error) {
	client, err := d.client.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get the EVM client: %w", err)
	}

	// Which chain this is comes from the client rather than from a setting of its
	// own: the client was configured with it, and a second place to say it is a
	// second place for it to be wrong.
	chainID := client.ConfiguredChainID()
	if chainID == nil || chainID.Sign() <= 0 {
		return nil, fmt.Errorf("the EVM client is configured for chain %v, which is not a chain ID", chainID)
	}

	database, err := d.db.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get the database: %w", err)
	}
	keystore, err := d.ks.Get(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to get the keystore: %w", err)
	}
	keystore, err = narrowed(ctx, keystore, d.account)
	if err != nil {
		return nil, err
	}

	cfg := d.chainTOML(chainID)
	// Validated here rather than left to the chain: a node validates its chains on
	// boot and NewTOMLChain trusts that, so this is where that check happens for a
	// process with no node around it.
	if err := cfg.ValidateConfig(); err != nil {
		return nil, fmt.Errorf("invalid configuration for chain %s: %w", chainID, err)
	}

	chain, err := legacyevm.NewTOMLChain(cfg, legacyevm.ChainRelayOpts{
		Logger: d.lggr,
		// The node's keys, reached through whatever the caller resolved: what signs a
		// transaction is the account the registry knows this node by, and this process
		// holds neither it nor a copy.
		KeyStore: keys.NewChainStore(keystore, chainID),
		ChainOpts: legacyevm.ChainOpts{
			ChainConfigs:   toml.EVMConfigs{cfg},
			DatabaseConfig: databaseConfig{},
			FeatureConfig:  featureConfig{},
			ListenerConfig: listenerConfig{},
			MailMon:        mailbox.NewMonitor("EVM", logger.Named(d.lggr, "Mailbox")),
			DS:             dataSource(database),
			// The client is the resolved dependency, dialled once by whoever opened it.
			// dialled wraps it so that starting the chain does not dial it again.
			GenEthClient: func(*big.Int) evmclient.Client { return dialled{client} },
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create chain %s: %w", chainID, err)
	}

	d.lggr.Infow("Built the EVM chain", "chainID", chainID, "transactions", d.cfg.TransactionsEnabled)
	return chain, nil
}

// dataSource is the database as chainlink-evm's ORMs read it.
//
// The column mapping has to be set here because the pool was opened by this
// binary rather than by the module whose queries run over it: sqlx maps a field by
// lowercasing its name, which turns ParentHash into parenthash and finds no such
// column.
func dataSource(db *sql.DB) sqlutil.DataSource {
	ds := sqlx.NewDb(db, "pgx")
	ds.MapperFunc(reflectx.CamelToSnakeASCII)
	return ds
}

// chainTOML is the chain configuration chainlink-evm already knows how to build
// itself, with the handful of things this capability was asked about written into
// it.
//
// It starts from that module's defaults for this chain ID - including whatever is
// specific to the chain in question - so what is configured here is a difference
// from those rather than a second set of values to keep in step with them.
func (d *dependency) chainTOML(chainID *big.Int) *toml.EVMConfig {
	id := sqlutil.Big(*chainID)
	chain := toml.Defaults(&id)

	chain.LogPollInterval = &d.cfg.LogPollInterval
	chain.Transactions.Enabled = &d.cfg.TransactionsEnabled
	chain.FinalityTagEnabled = &d.cfg.FinalityTagEnabled
	if d.cfg.FinalityDepth > 0 {
		chain.FinalityDepth = &d.cfg.FinalityDepth
	}
	if d.cfg.NoNewHeadsThreshold.Duration() > 0 {
		chain.NoNewHeadsThreshold = &d.cfg.NoNewHeadsThreshold
	}

	// The log broadcaster is a node's, not this capability's: what watches for logs
	// here is the log poller, and the broadcaster is the reason a node insists on a
	// websocket per RPC.
	broadcaster := false
	chain.LogBroadcasterEnabled = &broadcaster

	// Said so that the placeholder node below validates: a primary node needs a
	// websocket unless heads are polled over HTTP. It decides nothing, because the
	// pool it describes is never built - the client dependency's is - and how that
	// one follows heads is its own setting.
	chain.NodePool.NewHeadsPollInterval = commonconfig.MustNewDuration(time.Second)

	// One node, standing for the client this chain was given: the pool behind it is
	// the client's business, and the chain only needs to see that it has an RPC at
	// all. The URL is the loopback placeholder rather than a real one, since nothing
	// dials it - see dialled.
	name, placeholder := "client", "http://localhost"
	sendOnly, loadBalanced, order := false, false, int32(100)
	url, err := commonconfig.ParseURL(placeholder)
	if err != nil {
		// Unreachable: the string above is a constant.
		panic(err)
	}

	enabled := true
	return &toml.EVMConfig{
		ChainID: &id,
		Enabled: &enabled,
		Chain:   chain,
		Nodes: toml.EVMNodes{{
			Name:              &name,
			HTTPURL:           url,
			SendOnly:          &sendOnly,
			IsLoadBalancedRPC: &loadBalanced,
			Order:             &order,
		}},
	}
}

// dialled is a client that has already been dialled, for a chain that would
// otherwise dial it again.
//
// The client is a resolved dependency: whoever opened it owns its connections and
// closes them. A chain built over it still calls Dial when it starts - it expects
// to own the client - and a second dial of the same pool is an error, so this
// answers that call with the yes it already deserves.
type dialled struct {
	evmclient.Client
}

func (dialled) Dial(context.Context) error { return nil }

// Close is not forwarded either, for the same reason: the pool outlives this
// chain, and closing it here would take it out from under anything else holding
// the same dependency.
func (dialled) Close() {}

// EVMService is what a capability calls the chain through: the reads, the log
// tracking and the transaction submission a relayer would have handed it.
//
// It is built from a relayer, over the chain above, but that relayer is never
// started: what starting it adds is a node's business - mercury, LLO, the write
// target it configures from its own TOML - and none of it is on the path a
// capability takes.
//
// registry is what the relayer resolves capabilities through, and ks is how it
// chooses which of this node's accounts to send from.
func EVMService(lggr logger.Logger, chain legacyevm.Chain, db *sql.DB, ks core.Keystore, registry core.CapabilitiesRegistry) (commontypes.EVMService, error) {
	relayer, err := relay.NewRelayer(lggr, chain, relay.RelayerOpts{
		DS:                   dataSource(db),
		CSAKeystore:          unusedKeystore{},
		EVMKeystore:          keys.NewChainStore(ks, chain.ID()),
		CapabilitiesRegistry: registry,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create the EVM service for chain %s: %w", chain.ID(), err)
	}
	return relayer.EVM()
}

// unusedKeystore stands in for the CSA keys a relayer wants for mercury, which is
// a path a capability never takes. It is refused rather than left nil because nil
// is what the relayer checks for, and an error says which of the two this is: not
// forgotten, not available.
type unusedKeystore struct{}

var _ core.Keystore = unusedKeystore{}

func (unusedKeystore) Accounts(context.Context) ([]string, error) {
	return nil, errors.New("this capability holds no CSA keys: they are a node's, for mercury, which a capability does not serve")
}

func (unusedKeystore) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("this capability holds no CSA keys: they are a node's, for mercury, which a capability does not serve")
}

func (unusedKeystore) Decrypt(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("this capability holds no CSA keys: they are a node's, for mercury, which a capability does not serve")
}

// databaseConfig, featureConfig and listenerConfig are the three small
// configurations a chain takes that are the process's rather than the chain's.
//
// A node reads them from its own configuration file, where they are shared by
// everything it runs. Here there is nothing to share them with, so they are the
// values that configuration defaults to.
type databaseConfig struct{}

func (databaseConfig) DefaultQueryTimeout() time.Duration { return 10 * time.Second }

func (databaseConfig) LogSQL() bool { return false }

type featureConfig struct{}

// LogPoller is always on: a chain capability that watches for logs is the reason
// this exists, and the poller is what watches.
func (featureConfig) LogPoller() bool { return true }

type listenerConfig struct{}

// FallbackPollInterval is how often the transaction manager checks for work it was
// not woken for. A node defaults this to a minute; nothing here is different.
func (listenerConfig) FallbackPollInterval() time.Duration { return time.Minute }
