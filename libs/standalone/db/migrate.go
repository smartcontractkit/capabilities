package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

// migrate applies all pending migrations found in migrationsFS to db.
//
// migrationsFS must be rooted at the directory containing the goose `.sql`
// migration files (e.g. the result of fs.Sub on an embedded filesystem).
//
// migrationsTable is the name of the table goose uses to track which
// migrations have been applied. Pass a binary-specific name so multiple
// schemas can coexist in the same database without clobbering each other's
// migration history.
func migrate(ctx context.Context, db *sql.DB, migrationsFS fs.FS, migrationsTable string) error {
	migrationsFS, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	store, err := database.NewStore(goose.DialectPostgres, migrationsTable)
	if err != nil {
		return fmt.Errorf("failed to create goose store: %w", err)
	}

	// Go migrations are registered globally in goose; reset before building the
	// provider so repeated calls (e.g. in tests) don't accumulate duplicates.
	// See: https://github.com/pressly/goose/issues/782
	goose.ResetGlobalMigrations()

	p, err := goose.NewProvider("", db, migrationsFS, goose.WithStore(store))
	if err != nil {
		return fmt.Errorf("failed to create goose provider: %w", err)
	}

	if _, err = p.Up(ctx); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}
	log.Printf("migrations applied (history table %q)", migrationsTable)
	return nil
}
