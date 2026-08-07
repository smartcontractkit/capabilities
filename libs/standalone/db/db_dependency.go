package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	// Register the pgx database/sql driver under the name "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"

	"github.com/smartcontractkit/capabilities/libs/standalone"
)

// fakeFlag is the bootstrapper's --fake flag (see standalone.NewBootstrapper). It belongs to a
// different config struct than this one, so the rules below can't be `validate` tags - those
// only see sibling fields - and are checked in Config.validate against the value read back out
// of viper instead.
const fakeFlag = "fake"

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

// validate enforces the --fake relationships described on Config.
func (c Config) validate(fake bool) error {
	if c.UseRealDBForFake && !fake {
		return fmt.Errorf("--database.real-db only applies in fake mode: pass --fake as well, or drop --database.real-db to use a real database normally")
	}
	if (c.UseRealDBForFake || !fake) && c.URL == "" {
		return fmt.Errorf("--database.url is required when a real database is used (it is only optional with --fake and without --real-db)")
	}
	return nil
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

func (d *dependency) Get(ctx context.Context, commonConfig standalone.CommonConfig) (*sql.DB, error) {
	if commonConfig.Fake && !d.cfg.UseRealDBForFake {
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

func (d *dependency) AddCommands(command *cobra.Command) {
	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = d.Namespace()
	if err := flags.RegisterCommandFlags(command, &d.cfg, opts); err != nil {
		panic(err)
	}

	// --fake is owned by the bootstrapper, so bind it into viper to read it back here the same
	// way any other setting is resolved (flag, then CRE_FAKE / CL_FAKE).
	if f := command.PersistentFlags().Lookup(fakeFlag); f != nil {
		_ = viper.BindPFlag(fakeFlag, f)
		_ = viper.BindEnv(fakeFlag, "CRE_FAKE", "CL_FAKE")
	}

	// Chain after whatever is already wired (notably the decode step RegisterCommandFlags just
	// installed), so cfg is populated by the time these cross-flag rules are checked - and
	// checked before any service starts, rather than at connect time.
	prev := command.PersistentPreRunE
	command.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		if prev != nil {
			if err := prev(c, args); err != nil {
				return err
			}
		}
		// help and completion describe the program rather than run it, so they must work
		// without a usable configuration - the library skips its own validation for them, and
		// this check has to do the same.
		if flags.IsBuiltinCommand(c) {
			return nil
		}
		return d.cfg.validate(viper.GetBool(fakeFlag))
	}
}

var _ standalone.BootstrapDependency[*sql.DB] = (*dependency)(nil)
