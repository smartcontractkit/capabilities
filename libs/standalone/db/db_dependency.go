package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"

	// Register the pgx database/sql driver under the name "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/spf13/cobra"
)

const dbURLEnvVar = "CL_DATABASE_URL"

func Dependency(migrationsFS fs.FS, migrationTable string) standalone.BootstrapDependency[*sql.DB] {
	// Wrap in OnceBootstrapper so Get (which opens the DB and runs migrations)
	// runs at most once even if several services resolve this dependency.
	return standalone.OnceBootstrapper[*sql.DB](&dependency{migrationsFS: migrationsFS, migrationTable: migrationTable})
}

type dependency struct {
	db               *sql.DB
	useRealDbForFake bool
	migrationsFS     fs.FS
	migrationTable   string
}

func (d *dependency) Get(ctx context.Context, commonConfig standalone.CommonConfig) (*sql.DB, error) {
	dbFn := pgDb
	if commonConfig.Fake && !d.useRealDbForFake {
		// TODO set db to an in-memory one
		// Also add a subcommand to override with real DB even if fake is used
	}

	var err error
	d.db, err = dbFn()
	if err != nil {
		return nil, err
	}

	if err = migrate(ctx, d.db, d.migrationsFS, d.migrationTable); err != nil {
		return nil, err
	}

	return d.db, nil
}

func (d *dependency) AddCommands(command *cobra.Command) {
	command.PersistentFlags().BoolVar(&d.useRealDbForFake, "real_db", false, "uses a real db even if fake is set for the program")
}

func (d *dependency) Close() {
	_ = d.db.Close()
}

func pgDb() (*sql.DB, error) {
	dbURL := os.Getenv(dbURLEnvVar)
	if dbURL == "" {
		return nil, fmt.Errorf("%s must be set", dbURLEnvVar)
	}

	return sql.Open("pgx", dbURL)
}

var _ standalone.BootstrapDependency[*sql.DB] = (*dependency)(nil)
