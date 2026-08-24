package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// Config is the database: the connection settings it is opened with, and the schema this binary's
// tables live in.
//
// The connection settings are the database's own (sqlutil.Config, which also knows how to open it),
// inlined rather than nested so they keep the names every other binary gives them.
type Config struct {
	//nolint:revive // struct-tag: "inline" is pkg/config/flags' squash option (Options.SquashTagOption), not a go-toml one
	*sqlutil.Config `toml:",inline"`

	// Schema is where this binary's tables are created and read. Empty leaves the connection's own
	// search path alone, which is right for a database of this binary's own.
	//
	// It is for the other case: a database shared with something else, usually the node's. A binary
	// given a schema there cannot collide with the node's tables however plainly its own are named,
	// and an operator can see at a glance which tables are whose.
	Schema string `usage:"database schema this binary's tables are created in and read from; empty uses the connection's own search path"`
}

// Dependency returns a standalone.BootstrapDependency that resolves an opened, migrated database.
//
// Each embedded instance gets its own schema (see ForEmbedding), so the instances of an embedded
// DON keep their state apart while sharing one server, one URL and one set of credentials.
func Dependency(migrationsFS fs.FS, migrationTable string) standalone.BootstrapDependency[*sql.DB] {
	return standalone.OnceBootstrapper[*sql.DB](&dependency{
		migrationsFS:   migrationsFS,
		migrationTable: migrationTable,
		// The instance the flags are bound to and decoded into, so an unset setting keeps the
		// value it is given here. Fresh per call rather than shared, as every other dependency's
		// config is.
		cfg: &Config{Config: &sqlutil.Config{}},
	})
}

type dependency struct {
	cfg            *Config
	migrationsFS   fs.FS
	migrationTable string
}

func (d *dependency) Config() any {
	return d.cfg
}

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{}
}

// ForEmbedding returns a dependency that keeps its tables in a schema of its own, node_<i>.
//
// A database is the one dependency embedded instances must not share as it is: they are separate
// nodes, and separate nodes with one set of tables would read each other's rows. Partitioning by
// schema rather than by database (or by URL) keeps that to one connection string and one set of
// credentials, and puts every instance's state where an operator can see it side by side.
//
// Instance 0 is partitioned like the rest, into node_0, rather than being left on the configured
// schema: a run of N instances is N nodes, and having one of them live somewhere else would make its
// state the odd one out in exactly the situation the schemas exist to keep tidy. A plain `run` never
// calls this, so it is untouched by any of it.
func (d *dependency) ForEmbedding(i, _ int) standalone.BootstrapDependency[*sql.DB] {
	return &embedded{dependency: d, schema: fmt.Sprintf("node_%d", i)}
}

// Get opens the database as configured, in the configured schema when there is one.
func (d *dependency) Get(ctx context.Context, _ standalone.CommonConfig) (*sql.DB, error) {
	if d.cfg.Schema == "" {
		return d.open(ctx, *d.cfg.Config, nil)
	}
	return d.openInSchema(ctx, d.cfg.Schema)
}

// openInSchema opens the database with schema at the front of its search path, creating the schema if
// it is not already there. A configured schema and an embedded instance's schema both come through
// here: they want the same thing for different reasons.
//
// The schema is usually created by whoever owns the database - the node's migrations, for a binary
// the node launches - so the create is for the other case: a database of this binary's own, and the
// per-instance schemas of an embedded run, which no migration knows about.
func (d *dependency) openInSchema(ctx context.Context, schema string) (*sql.DB, error) {
	cfg := *d.cfg.Config
	var err error
	if cfg.URL, err = withSearchPath(cfg.URL, schema); err != nil {
		return nil, err
	}

	return d.open(ctx, cfg, func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdentifier(schema)); err != nil {
			return fmt.Errorf("failed to create schema %s: %w", schema, err)
		}
		return nil
	})
}

// open opens the database at cfg and applies the migrations, running prepare (when given) on the
// opened database first - which is where an embedded instance creates the schema its migrations
// land in.
func (d *dependency) open(ctx context.Context, cfg sqlutil.Config, prepare func(context.Context, *sql.DB) error) (*sql.DB, error) {
	db, err := sqlutil.OpenDB(cfg)
	if err != nil {
		return nil, err
	}

	if prepare != nil {
		if err := prepare(ctx, db); err != nil {
			return nil, err
		}
	}

	if err := migrate(ctx, db, d.migrationsFS, d.migrationTable); err != nil {
		return nil, err
	}
	return db, nil
}

// Namespace groups the database settings under database.* (--database.url, CRE_DATABASE_URL).
func (d *dependency) Namespace() string { return "database" }

var _ standalone.BootstrapDependency[*sql.DB] = (*dependency)(nil)

// embedded is one embedded instance's database: the configured server, with this instance's tables
// in a schema of its own. Which schema is settled when it is built, so resolving it asks no
// questions about who is resolving it.
type embedded struct {
	*dependency
	schema string
}

var _ standalone.BootstrapDependency[*sql.DB] = (*embedded)(nil)

func (d *embedded) Get(ctx context.Context, _ standalone.CommonConfig) (*sql.DB, error) {
	// An instance's own schema, whatever the configured one was: instances of one run must not share
	// tables, and a schema configured for all of them would be exactly that.
	return d.openInSchema(ctx, d.schema)
}

// withSearchPath returns rawURL with schema at the front of its search path, so unqualified names
// resolve to (and new tables are created in) that schema. public is kept behind it, since anything
// shared by the whole database - extensions, most obviously - lives there and every instance still
// needs to reach it. An operator's own search_path is preserved the same way, with the instance's
// schema taking precedence over it.
//
// pgx passes query parameters it does not recognise to the server as runtime parameters, which is
// how a schema is selected for a pool without having to set it on every connection by hand.
func withSearchPath(rawURL, schema string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse database url: %w", err)
	}

	query := u.Query()
	searchPath := []string{schema}
	if existing := query.Get("search_path"); existing != "" {
		searchPath = append(searchPath, existing)
	} else {
		searchPath = append(searchPath, "public")
	}
	query.Set("search_path", strings.Join(searchPath, ","))
	u.RawQuery = query.Encode()

	return u.String(), nil
}

// quoteIdentifier quotes a postgres identifier. The schema names here are built from an instance
// index and cannot contain anything that needs escaping, but an identifier interpolated into DDL is
// quoted regardless: the next name to be interpolated may not be as safe.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
