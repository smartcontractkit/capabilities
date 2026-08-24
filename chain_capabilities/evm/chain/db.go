// Package chain builds the EVM chain this capability reads and writes: an RPC
// client, a head tracker, a log poller and a transaction manager, assembled from
// chainlink-evm's own components rather than reached through a relayer in the
// node's process.
//
// It lives here rather than in chainlink-evm because it is this binary's way of
// starting up, not that module's: what it does is resolve bootstrap dependencies
// - a client, a database, keys - and hand chainlink-evm what it already knows how
// to take.
package chain

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"io/fs"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	goosedb "github.com/pressly/goose/v3/database"
	gooselock "github.com/pressly/goose/v3/lock"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// defaultSchema is where this capability keeps its chain state, and the schema a
// node's own migrations create for it (0303_create_cre_standalone_schemas.sql).
//
// It is not "evm", which is the node's: a node's own log poller and transaction
// manager live there, and two of either over one set of tables would each treat
// the other's rows as its own. Owning a schema instead means this capability can
// run beside a node, beside another instance of itself, or on a database of its
// own, without any of them agreeing about anything first.
//
// One schema holds every chain, not one per chain: chainlink-evm's tables all
// carry an evm_chain_id, which is how a node keeps every chain it runs in its own
// single evm schema. Processes started together therefore migrate the same schema
// at the same time, which is what the lock in migrate is for.
const defaultSchema = "evm_capability"

// DBConfig is the database this capability keeps its chain state in.
type DBConfig struct {
	URL string `validate:"required" usage:"database url" example:"'postgresql://user:password@localhost:5432/chainlink?sslmode=disable'"`

	// Schema is which schema in it is this capability's. Whatever it is called, the
	// tables in it are the ones chainlink-evm's queries name in the evm schema: see
	// qualified.
	Schema string `usage:"database schema this capability's chain state lives in; must not be the node's own evm schema"`
}

// DBDependency returns the database this capability's chain state lives in:
// opened, in a schema of its own, and migrated.
//
// The migrations are this binary's, embedded by the caller, and they are written
// against the evm schema like everything else here - so they land wherever the
// configured schema is, for the same reason the queries do.
func DBDependency(lggr logger.Logger, migrations fs.FS, migrationTable string) standalone.BootstrapDependency[*sql.DB] {
	// Wrapped so one pool is opened and migrated however many services resolve this.
	return standalone.OnceBootstrapper[*sql.DB](&dbDependency{
		lggr:           lggr,
		migrations:     migrations,
		migrationTable: migrationTable,
		cfg:            &DBConfig{Schema: defaultSchema},
	})
}

type dbDependency struct {
	lggr           logger.Logger
	migrations     fs.FS
	migrationTable string
	cfg            *DBConfig

	// instance names this instance of an embedded run, appended to the schema so that
	// instances keep their chain state apart. Empty for a single run.
	instance string
}

var _ standalone.BootstrapDependency[*sql.DB] = (*dbDependency)(nil)

// Namespace groups the settings under database.*, the names every binary in this
// framework gives them.
func (d *dbDependency) Namespace() string { return "database" }

func (d *dbDependency) Config() any { return d.cfg }

func (d *dbDependency) Dependencies() []standalone.BootstrapCommand { return nil }

// ForEmbedding gives instance i a schema of its own, since the instances of an
// embedded run are separate nodes: they must no more share a log poller's blocks
// than they share a peer identity.
//
// The configured schema is kept as the stem, so a run's schemas sit together and
// an operator can see which run they belong to.
func (d *dbDependency) ForEmbedding(i, _ int) standalone.BootstrapDependency[*sql.DB] {
	embedded := *d
	embedded.instance = fmt.Sprintf("_node_%d", i)
	return standalone.OnceBootstrapper[*sql.DB](&embedded)
}

func (d *dbDependency) Get(ctx context.Context, _ standalone.CommonConfig) (*sql.DB, error) {
	schema := d.schema()
	if !identifier.MatchString(schema) {
		return nil, fmt.Errorf("invalid --database.schema %q: expected a lowercase identifier", schema)
	}
	if schema == chainlinkEVMSchema {
		return nil, fmt.Errorf("--database.schema must not be %q: that schema is the node's, and this capability's log poller and transaction manager would run over its tables", chainlinkEVMSchema)
	}

	db, err := d.open(schema)
	if err != nil {
		return nil, err
	}

	// Usually already there, created by the migrations of whoever owns the database.
	// This is for the other cases - a database of this capability's own, and the
	// per-instance schemas of an embedded run, which no migration knows about.
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdentifier(schema)); err != nil {
		return nil, fmt.Errorf("failed to create schema %s: %w", schema, err)
	}

	if err := migrate(ctx, db, d.migrations, d.migrationTable, schema); err != nil {
		return nil, err
	}

	d.lggr.Infow("Opened the capability's database", "schema", schema)
	return db, nil
}

// schema is this instance's schema: the configured one, plus which instance of an
// embedded run this is.
func (d *dbDependency) schema() string {
	schema := d.cfg.Schema
	if schema == "" {
		schema = defaultSchema
	}
	return schema + d.instance
}

// open opens the pool, with every query rewritten into schema and unqualified
// names resolving there too.
//
// The search path covers what this binary writes itself - the migration history,
// and its own tables - and the rewriting covers what chainlink-evm writes, which
// names its schema in every query.
func (d *dbDependency) open(schema string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(d.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the database url: %w", err)
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	// public stays behind it: extensions and anything else shared by the database
	// live there, and an operator's own search path is preserved the same way.
	searchPath := schema + ",public"
	if existing := config.RuntimeParams["search_path"]; existing != "" {
		searchPath = schema + "," + existing
	}
	config.RuntimeParams["search_path"] = searchPath

	return sql.OpenDB(&connector{
		Connector: stdlib.GetConnector(*config),
		rewrite:   rewriteInto(schema),
	}), nil
}

// chainlinkEVMSchema is the schema chainlink-evm's queries are written against,
// and the one a node keeps its own chain state in.
const chainlinkEVMSchema = "evm"

// qualified matches that schema wherever a query names it: `evm.` followed by the
// table, function or type it qualifies. The leading boundary keeps it from
// matching the tail of a longer identifier, so a column called something_evm.x -
// were there one - is left alone.
var qualified = regexp.MustCompile(`\bevm\.`)

// identifier is what a schema may be called. The name is spliced into SQL rather
// than bound as a parameter, so it is checked rather than escaped: a schema
// needing quotes is a sign of a caller doing something other than naming one.
var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// rewriteInto returns the rewrite applied to every query on this pool: the evm
// schema chainlink-evm names becomes the schema this capability owns.
//
// This is what lets a capability use another module's ORM without that module
// having a schema parameter in several hundred query strings, and without this
// one sharing a table with whatever else is on the database. It is not a parser
// and does not pretend to be one; it moves a schema qualifier and nothing else.
func rewriteInto(schema string) func(string) string {
	if schema == chainlinkEVMSchema {
		return nil
	}
	replacement := schema + "."
	return func(query string) string {
		return qualified.ReplaceAllLiteralString(query, replacement)
	}
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// migrate applies the embedded migrations, tracking them in a table named for
// this binary so that a database holding more than one binary's tables keeps
// their histories apart.
func migrate(ctx context.Context, db *sql.DB, migrations fs.FS, table, schema string) error {
	migrations, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}

	store, err := goosedb.NewStore(goose.DialectPostgres, table)
	if err != nil {
		return fmt.Errorf("failed to create the goose store: %w", err)
	}

	// The advisory lock is named for what it protects - this history, in this schema -
	// rather than taken on goose's default ID, which every goose user on the database
	// shares: a node migrating its own tables has no reason to wait for this.
	locker, err := gooselock.NewPostgresSessionLocker(gooselock.WithLockID(lockID(schema, table)))
	if err != nil {
		return fmt.Errorf("failed to create the migration locker: %w", err)
	}

	// Go migrations are registered globally in goose; reset before building the
	// provider so repeated calls (a test, or an embedded run's instances) do not
	// accumulate duplicates. See https://github.com/pressly/goose/issues/782
	goose.ResetGlobalMigrations()

	// Locked, because the processes sharing this schema start together: a node running
	// this capability on two chains runs two of them, both pointed at the same tables,
	// both applying this history on boot. goose serialises nothing across processes on
	// its own - its provider mutex is one process's - so without this the second one
	// creates a table the first is already creating.
	provider, err := goose.NewProvider("", db, migrations,
		goose.WithStore(store),
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return fmt.Errorf("failed to create the goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}
	return nil
}

// lockID is the advisory lock these migrations are applied under: one number per
// schema and history, so processes sharing them queue and processes that do not
// are unaffected.
func lockID(schema, table string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(schema + "." + table))
	return int64(h.Sum64()) //#nosec G115 - an advisory lock ID is any 64 bits, signed by definition
}

// connector opens connections whose queries are rewritten.
//
// The rewriting is here, at the driver, rather than over the DataSource above it,
// because a transaction runs its statements on the connection it was begun on: a
// wrapper further up would rewrite the queries it was handed and miss every one
// inside a transaction, which for a transaction manager is most of them.
type connector struct {
	driver.Connector
	rewrite func(string) string
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil || c.rewrite == nil {
		return conn, err
	}
	return &rewritingConn{Conn: conn, rewrite: c.rewrite}, nil
}

// rewritingConn is one connection, rewriting the text of everything asked of it.
//
// Every statement reaches a connection as one of these three - prepared, queried
// or executed - whether it was written by hand, built by an ORM, or run inside a
// transaction, so this is the whole surface.
type rewritingConn struct {
	driver.Conn
	rewrite func(string) string
}

var (
	_ driver.ConnPrepareContext = (*rewritingConn)(nil)
	_ driver.QueryerContext     = (*rewritingConn)(nil)
	_ driver.ExecerContext      = (*rewritingConn)(nil)
	_ driver.ConnBeginTx        = (*rewritingConn)(nil)
)

func (c *rewritingConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(c.rewrite(query))
}

func (c *rewritingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	preparer, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return c.Prepare(query)
	}
	return preparer.PrepareContext(ctx, c.rewrite(query))
}

func (c *rewritingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		// database/sql falls back to Prepare when a connection cannot query directly,
		// and that path is rewritten too.
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, c.rewrite(query), args)
}

func (c *rewritingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, c.rewrite(query), args)
}

// BeginTx hands back the underlying transaction as it is: what a transaction
// carries is statements, and those come back through this connection.
func (c *rewritingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		//nolint:staticcheck // SA1019: the fallback for a driver that predates ConnBeginTx
		return c.Conn.Begin()
	}
	return beginner.BeginTx(ctx, opts)
}

// The rest of this file is the optional half of driver.Conn, forwarded.
//
// database/sql asks a connection what it can do by type-asserting, so anything a
// wrapper does not implement is a thing the wrapped connection is taken not to do.
// That is not a lost optimisation: without CheckNamedValue below, database/sql
// converts arguments with its own rules instead of the driver's, and rejects
// everything the driver would have handled - a query passing an array of
// addresses, say, which is how the transaction manager asks about several at once.

var (
	_ driver.NamedValueChecker = (*rewritingConn)(nil)
	_ driver.SessionResetter   = (*rewritingConn)(nil)
	_ driver.Validator         = (*rewritingConn)(nil)
	_ driver.Pinger            = (*rewritingConn)(nil)
)

// CheckNamedValue lets the driver decide what an argument may be, which for pgx
// is a good deal more than database/sql's default converter allows.
func (c *rewritingConn) CheckNamedValue(value *driver.NamedValue) error {
	checker, ok := c.Conn.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return checker.CheckNamedValue(value)
}

// ResetSession is how a pooled connection is made ready for its next user.
func (c *rewritingConn) ResetSession(ctx context.Context) error {
	resetter, ok := c.Conn.(driver.SessionResetter)
	if !ok {
		return nil
	}
	return resetter.ResetSession(ctx)
}

// IsValid is how a connection says it should not be reused. Answering yes for a
// connection that says no would put a broken one back in the pool.
func (c *rewritingConn) IsValid() bool {
	validator, ok := c.Conn.(driver.Validator)
	if !ok {
		return true
	}
	return validator.IsValid()
}

func (c *rewritingConn) Ping(ctx context.Context) error {
	pinger, ok := c.Conn.(driver.Pinger)
	if !ok {
		return nil
	}
	return pinger.Ping(ctx)
}
