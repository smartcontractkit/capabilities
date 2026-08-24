package chain

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// dbURL is the database these tests run against, and whether they run at all:
// what they are about is what two connections to a real one do to each other.
const dbURL = "CL_DATABASE_URL"

// childSchema is set on the subprocesses TestMigrateIsConcurrent starts, naming
// the schema they are to migrate. It is what tells one of these test binaries it
// is a child rather than the test.
const childSchema = "TEST_MIGRATE_SCHEMA"

// childStart is when those subprocesses are to begin, so that they begin
// together: what is being tested happens in the moment two of them read an
// unmigrated schema, and processes left to start when they finish loading miss it
// most times out of ten.
const childStart = "TEST_MIGRATE_START"

// TestMigrateIsConcurrent covers the case a node running this capability on two
// chains creates: two processes, one schema, both migrating it at the same time.
//
// Separate processes rather than goroutines, because that is the whole question -
// goose serialises migrations within a process on its own, so goroutines would
// pass with or without the advisory lock the code takes.
func TestMigrateIsConcurrent(t *testing.T) {
	url := os.Getenv(dbURL)
	if url == "" {
		t.Skip("set " + dbURL + " to run this against a database")
	}
	if os.Getenv(childSchema) != "" {
		t.Skip("this process is a child of the test, see TestMigrateChild")
	}

	const schema = "evm_capability_concurrent_test"

	admin, err := sql.Open("pgx", url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	drop := `DROP SCHEMA IF EXISTS ` + quoteIdentifier(schema) + ` CASCADE`
	_, err = admin.ExecContext(t.Context(), drop)
	require.NoError(t, err)
	_, err = admin.ExecContext(t.Context(), `CREATE SCHEMA `+quoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.WithoutCancel(t.Context()), drop)
	})

	// Far enough out that every child is loaded and waiting before any of them moves.
	start := time.Now().Add(2 * time.Second)

	const processes = 4
	output := make([]string, processes)
	errs := make([]error, processes)
	var wg sync.WaitGroup
	for i := range processes {
		wg.Add(1)
		go func() {
			defer wg.Done()

			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestMigrateChild", "-test.v")
			cmd.Env = append(os.Environ(),
				childSchema+"="+schema,
				childStart+"="+start.Format(time.RFC3339Nano),
			)
			out, err := cmd.CombinedOutput()
			output[i], errs[i] = string(out), err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "process %d: %s", i, output[i])
	}

	// Applied once and only once: whichever process won, the others found the history
	// already at that version rather than applying it again.
	var version int
	require.NoError(t, admin.QueryRowContext(t.Context(),
		`SELECT max(version_id) FROM `+quoteIdentifier(schema)+`.`+migrationTable).Scan(&version))
	require.Positive(t, version)

	var tables int
	require.NoError(t, admin.QueryRowContext(t.Context(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'trigger_pending_events'`,
		schema).Scan(&tables))
	require.Equal(t, 1, tables)
}

// migrationTable is what the binary calls its goose history; the name matters
// only in that every process here agrees on it.
const migrationTable = "evm_capability_migrations"

// TestMigrateChild is one of the processes TestMigrateIsConcurrent starts: it
// migrates the schema it was given and says whether that worked. It is a test
// only because that is how this binary is run.
func TestMigrateChild(t *testing.T) {
	url, schema := os.Getenv(dbURL), os.Getenv(childSchema)
	if url == "" || schema == "" {
		t.Skip("started directly rather than by TestMigrateIsConcurrent")
	}

	if at := os.Getenv(childStart); at != "" {
		start, err := time.Parse(time.RFC3339Nano, at)
		require.NoError(t, err)
		time.Sleep(time.Until(start))
	}

	d := &dbDependency{
		lggr:           logger.Test(t),
		migrations:     os.DirFS(".."),
		migrationTable: migrationTable,
		cfg:            &DBConfig{URL: url, Schema: schema},
	}
	db, err := d.open(schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, migrate(t.Context(), db, d.migrations, migrationTable, schema))
}
