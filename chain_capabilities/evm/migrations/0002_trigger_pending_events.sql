-- +goose Up
-- Trigger events that have fired but not been acknowledged, so a restart resends what was in flight
-- rather than dropping it. The shape is libs/standalone/eventstore's, which is what reads and writes
-- this.
--
-- Unqualified, so it lands in the schema this capability was configured with, beside its chain state:
-- these events are this instance's, and an embedded run's instances must not answer each other's.
--
-- scope is which chain the events belong to. Every other table here carries an evm_chain_id, which is
-- what lets one schema hold every chain a node runs this capability on; this one is not chainlink-evm's,
-- so it says the same thing in its own words, and the process on chain A neither lists nor deletes what
-- is owed on chain B.
CREATE TABLE trigger_pending_events (
	scope        TEXT        NOT NULL DEFAULT '',
	trigger_id   TEXT        NOT NULL,
	event_id     TEXT        NOT NULL,
	payload      BYTEA       NOT NULL,
	first_at     TIMESTAMPTZ NOT NULL,
	last_sent_at TIMESTAMPTZ NULL,
	attempts     INTEGER     NOT NULL DEFAULT 0,
	org_id       TEXT        NOT NULL DEFAULT '',
	PRIMARY KEY (scope, trigger_id, event_id)
);

-- +goose Down
DROP TABLE IF EXISTS trigger_pending_events;
