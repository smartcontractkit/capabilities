// Package kvstore is the key-value store a capability keeps small facts in.
//
// It is what a node hands a capability today (core's job_kv_store, keyed by job
// ID), in the shape a capability running as its own binary can have: its own
// table, in its own schema, in the database it was pointed at.
//
// What it is for is the things a capability must not forget when it restarts. The
// HTTP trigger's request cache is the example: a customer that retries a request
// has to be given the answer already produced rather than have the workflow run
// again, and a cache that lived in memory would run it again after every deploy.
package kvstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Table is where the values are kept, unqualified so that the connection's search
// path decides which schema that is - the same rule the rest of a capability's
// tables follow, and what keeps the instances of an embedded run out of each
// other's values.
const Table = "capability_kv_store"

// Schema is the DDL this package expects, for a capability's migrations to
// include verbatim.
//
// It is a constant rather than a migration of its own because migrations belong
// to a binary: one goose history per database, owned by whoever runs it. What
// this package can do is say what shape it reads.
//
// updated_at is what expiry is measured from rather than a creation time,
// because a value that was rewritten is a value that is still in use - the same
// rule the node's own store applies.
const Schema = `CREATE TABLE IF NOT EXISTS ` + Table + ` (
	scope      TEXT        NOT NULL DEFAULT '',
	key        TEXT        NOT NULL,
	value      BYTEA       NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (scope, key)
)`

// New returns the store over ds, holding the values of one scope.
//
// A scope is whoever the values belong to when the table is shared: two
// capabilities in one binary, or the instances of an embedded run, would
// otherwise read and expire each other's. A binary with a table to itself passes
// "" and never thinks about it again.
func New(ds sqlutil.DataSource, scope string) core.KeyValueStore {
	return &store{ds: ds, scope: scope}
}

type store struct {
	ds    sqlutil.DataSource
	scope string
}

var _ core.KeyValueStore = (*store)(nil)

// Store writes a value, replacing whatever was there.
func (s *store) Store(ctx context.Context, key string, value []byte) error {
	const q = `INSERT INTO ` + Table + ` (scope, key, value, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (scope, key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`

	if _, err := s.ds.ExecContext(ctx, q, s.scope, key, value, time.Now()); err != nil {
		return fmt.Errorf("failed to store the value at key %s: %w", key, err)
	}
	return nil
}

// Get returns what is at key, or nil for a key that was never written.
//
// Nil rather than an error, because "nothing here" is the ordinary answer for a
// cache and the caller reads it as one. Anything else is a failure to reach the
// database, which is not the same thing at all.
func (s *store) Get(ctx context.Context, key string) ([]byte, error) {
	const q = `SELECT value FROM ` + Table + ` WHERE scope = $1 AND key = $2`

	var value []byte
	if err := s.ds.GetContext(ctx, &value, q, s.scope, key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the value at key %s: %w", key, err)
	}
	return value, nil
}

// PruneExpiredEntries forgets what has not been written for maxAge, and says how
// much it forgot.
func (s *store) PruneExpiredEntries(ctx context.Context, maxAge time.Duration) (int64, error) {
	const q = `DELETE FROM ` + Table + ` WHERE scope = $1 AND updated_at < $2`

	result, err := s.ds.ExecContext(ctx, q, s.scope, time.Now().Add(-maxAge))
	if err != nil {
		return 0, fmt.Errorf("failed to prune expired values: %w", err)
	}

	pruned, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read how many values were pruned: %w", err)
	}
	return pruned, nil
}
