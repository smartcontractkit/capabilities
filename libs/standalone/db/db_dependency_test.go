package db

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"
)

func TestWithSearchPath(t *testing.T) {
	const dsn = "postgresql://user:password@localhost:5432/chainlink?sslmode=disable"

	t.Run("the instance's schema is searched before public", func(t *testing.T) {
		withSchema, err := withSearchPath(dsn, "node_1")
		require.NoError(t, err)

		// public stays reachable behind it: extensions and anything else shared by the whole
		// database live there, and every instance still needs them.
		assert.Equal(t, "node_1,public", queryParam(t, withSchema, "search_path"))
		// Everything else about the connection is left as configured.
		assert.Equal(t, "disable", queryParam(t, withSchema, "sslmode"))
		assert.Equal(t, "/chainlink", mustParse(t, withSchema).Path)
		assert.Equal(t, "user:password", mustParse(t, withSchema).User.String())
	})

	t.Run("a configured search path is kept, behind the instance's schema", func(t *testing.T) {
		withSchema, err := withSearchPath(dsn+"&search_path=shared", "node_2")
		require.NoError(t, err)

		assert.Equal(t, "node_2,shared", queryParam(t, withSchema, "search_path"))
	})

	t.Run("an unparseable url is an error", func(t *testing.T) {
		_, err := withSearchPath("postgresql://user@%zz/db", "node_0")
		require.ErrorContains(t, err, "failed to parse database url")
	})
}

func TestForEmbedding(t *testing.T) {
	const dsn = "postgresql://localhost:5432/chainlink"
	template := &dependency{cfg: &sqlutil.Config{URL: dsn}, migrationTable: "migrations"}

	// Instance 0 is partitioned like every other instance, so a run of N instances has N schemas and
	// none of them is the odd one out. A single instance never calls this, and keeps the database as
	// configured.
	assert.Equal(t, "node_0", template.ForEmbedding(0, 2).(*embedded).schema)
	assert.Equal(t, "node_1", template.ForEmbedding(1, 2).(*embedded).schema)
}

func mustParse(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u
}

func queryParam(t *testing.T, rawURL, key string) string {
	t.Helper()
	return mustParse(t, rawURL).Query().Get(key)
}
