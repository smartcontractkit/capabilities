package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"

	"github.com/smartcontractkit/capabilities/libs/standalone"
)

// Config is the database configuration: the connection settings that belong to the database
// itself (sqlutil.Config, which also knows how to open it), plus the one setting that only
// exists because this framework has a --fake mode.
//
// Both settings are related to --fake, which this struct does not own:
//
//   - real-db only means something in fake mode, so it is rejected without --fake.
//   - url is needed whenever a real database is actually used: always in normal mode, and in
//     fake mode only when --real-db asks for one.
//
// Neither rule is expressible as a `validate` tag (both reference --fake), so neither shows up
// in the generated docs on its own - the usage text spells them out so the docs still explain
// when each setting applies.
type Config struct {
	// Embedded as a pointer so this struct adds to the shared settings rather than copying
	// them; inline so its fields sit alongside real-db under database.* instead of nesting.
	*sqlutil.Config `toml:",inline"`

	// Kept out of the example config: the example shows a normal run against a real database,
	// where this setting does not apply. It is still documented.
	RealDB bool `usage:"use a real database even though --fake is set; requires --fake, and a url to point at" flagdocs:"noexample"`
}

func Dependency(migrationsFS fs.FS, migrationTable string) standalone.BootstrapDependency[*sql.DB] {
	return standalone.OnceBootstrapper[*sql.DB](&dependency{
		migrationsFS:   migrationsFS,
		migrationTable: migrationTable,
		// The embedded pointer is non-nil so the shared settings are bound and defaulted from
		// here, the same way every other dependency starts from its config's defaults.
		cfg: Config{Config: &sqlutil.Config{}},
	})
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
	if commonConfig.Fake && !d.cfg.RealDB {
		if d.cfg.URL != "" {
			return nil, fmt.Errorf("database url set when using fake database")
		}
		// TODO set db to an in-memory one
		// Also add a subcommand to override with real DB even if fake is used
	}

	var err error
	d.db, err = sqlutil.OpenDB(*d.cfg.Config)
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
