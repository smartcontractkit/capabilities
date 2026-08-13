-- +goose Up
-- Registry snapshots, so a restart can answer registry lookups from the last known state while its
-- first on-chain read is still in flight. data is the snapshot as JSON; data_hash is what makes an
-- unchanged registry not write a new row.
CREATE TABLE proxy_registry_snapshots (
	id BIGSERIAL PRIMARY KEY,
	data jsonb NOT NULL,
	data_hash text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);
-- Only ever read newest-first, and pruned the same way.
CREATE INDEX idx_proxy_registry_snapshots_id_desc ON proxy_registry_snapshots (id DESC);
-- +goose Down
DROP TABLE proxy_registry_snapshots;
