package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	// Register the pgx database/sql driver under the name "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/smartcontractkit/capabilities/libs/standalone"
)

// Config is the database configuration. Its two settings are related to --fake, which this
// struct does not own:
//
//   - real-db only means something in fake mode, so it is rejected without --fake.
//   - database-url is needed whenever a real database is actually used: always in normal mode,
//     and in fake mode only when --real-db asks for one.
//
// Neither rule is expressible as a `validate` tag (both reference --fake), so neither shows up
// in the generated docs on its own - the usage text spells them out so the docs still explain
// when each setting applies.
type Config struct {
	URL string `toml:"url" usage:"database url; required unless running with --fake and without --real-db" example:"'postgresql://user:password@localhost:5432/chainlink?sslmode=disable'"`

	// Kept out of the example config: the example shows a normal run against a real database,
	// where this setting does not apply. It is still documented.
	UseRealDBForFake bool `toml:"real-db" usage:"use a real database even though --fake is set; requires --fake, and a url to point at" flagdocs:"noexample"`
}

func Dependency(migrationsFS fs.FS, migrationTable string) standalone.BootstrapDependency[*sql.DB] {
	return standalone.OnceBootstrapper[*sql.DB](&dependency{migrationsFS: migrationsFS, migrationTable: migrationTable})
}

type dependency struct {
	db             *sql.DB
	cfg            Config
	migrationsFS   fs.FS
	migrationTable string
}

func (d *dependency) Config() any {
	return &d.cfg
}

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{}
}

func (d *dependency) Get(ctx context.Context, commonConfig standalone.CommonConfig) (*sql.DB, error) {
	if commonConfig.Fake && !d.cfg.UseRealDBForFake {
		if d.cfg.URL != "" {
			return nil, fmt.Errorf("database url set when using fake database")
		}
		// TODO set db to an in-memory one
		// Also add a subcommand to override with real DB even if fake is used
	}

	var err error
	d.db, err = sql.Open("pgx", d.cfg.URL)
	if err != nil {
		return nil, err
	}

	if err = migrate(ctx, d.db, d.migrationsFS, d.migrationTable); err != nil {
		return nil, err
	}

	return d.db, nil
}

// Namespace groups the database settings under database.* (--database.url, CRE_DATABASE_URL).
func (d *dependency) Namespace() string { return "database" }

var _ standalone.BootstrapDependency[*sql.DB] = (*dependency)(nil)
