package kvstore_test

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/standalone/kvstore"
)

// dbURL is the database these tests run against, and whether they run at all:
// what a store is for is surviving a restart, and only a real one does that.
const dbURL = "CL_DATABASE_URL"

// store returns a database with this package's table in a schema of its own.
func store(t *testing.T) *sqlx.DB {
	t.Helper()

	url := os.Getenv(dbURL)
	if url == "" {
		t.Skip("set " + dbURL + " to run this against a database")
	}

	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })

	schema := "kvstore_test"
	_, err = db.ExecContext(t.Context(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `SET search_path TO `+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), kvstore.Schema)
	require.NoError(t, err)

	return sqlx.NewDb(db, "pgx")
}

// TestStore covers what the request cache needs of this: a value written is a
// value read back, and a key never written is nothing rather than an error.
func TestStore(t *testing.T) {
	db := store(t)
	s := kvstore.New(db, "http_trigger")

	missing, err := s.Get(t.Context(), "never-written")
	require.NoError(t, err)
	assert.Nil(t, missing, "a key that was never written is nothing, not a failure")

	require.NoError(t, s.Store(t.Context(), "request-1", []byte(`{"answered":true}`)))

	got, err := s.Get(t.Context(), "request-1")
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"answered":true}`), got)

	t.Run("writing again replaces", func(t *testing.T) {
		require.NoError(t, s.Store(t.Context(), "request-1", []byte(`{"answered":"again"}`)))

		got, err := s.Get(t.Context(), "request-1")
		require.NoError(t, err)
		assert.Equal(t, []byte(`{"answered":"again"}`), got)
	})
}

// TestStoreScopes is why the table has a scope: two capabilities sharing a
// database must not answer from each other's values.
func TestStoreScopes(t *testing.T) {
	db := store(t)
	trigger := kvstore.New(db, "http_trigger")
	other := kvstore.New(db, "something_else")

	require.NoError(t, trigger.Store(t.Context(), "shared-key", []byte("mine")))

	got, err := other.Get(t.Context(), "shared-key")
	require.NoError(t, err)
	assert.Nil(t, got, "another scope's value must not be visible")

	require.NoError(t, other.Store(t.Context(), "shared-key", []byte("theirs")))

	mine, err := trigger.Get(t.Context(), "shared-key")
	require.NoError(t, err)
	assert.Equal(t, []byte("mine"), mine, "and must not overwrite it")
}

// TestStorePrunes covers the sweep: what has not been touched for long enough is
// forgotten, and what has been is kept.
func TestStorePrunes(t *testing.T) {
	db := store(t)
	s := kvstore.New(db, "http_trigger")

	require.NoError(t, s.Store(t.Context(), "old", []byte("x")))
	require.NoError(t, s.Store(t.Context(), "new", []byte("y")))

	// Aged by hand rather than by waiting: what is under test is the cutoff.
	_, err := db.ExecContext(t.Context(),
		`UPDATE `+kvstore.Table+` SET updated_at = $1 WHERE key = 'old'`, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)

	pruned, err := s.PruneExpiredEntries(t.Context(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned)

	gone, err := s.Get(t.Context(), "old")
	require.NoError(t, err)
	assert.Nil(t, gone)

	kept, err := s.Get(t.Context(), "new")
	require.NoError(t, err)
	assert.Equal(t, []byte("y"), kept)
}
