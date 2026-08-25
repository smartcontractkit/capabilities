-- +goose Up
-- Where the HTTP trigger remembers the requests it has already answered.
--
-- It is what makes a retry idempotent: a customer that sends the same request ID
-- again - because a response was lost, or a client retried on a timeout - is given
-- the answer the workflow already produced rather than having it run a second
-- time. A node kept this in its own key-value table; a capability that runs as its
-- own binary keeps it here.
--
-- The shape is libs/standalone/kvstore's, which is what reads and writes it.
--
-- Unqualified, so it lands in the schema this binary was configured with: these
-- values are this instance's, and an embedded run's instances must not answer from
-- each other's.
CREATE TABLE capability_kv_store (
	scope      TEXT        NOT NULL DEFAULT '',
	key        TEXT        NOT NULL,
	value      BYTEA       NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (scope, key)
);

-- Pruning is by age, and it runs on a timer over the whole table.
CREATE INDEX capability_kv_store_updated_at ON capability_kv_store (updated_at);

-- +goose Down
DROP TABLE IF EXISTS capability_kv_store;
