-- +goose Up
-- announcements table for RageP2P discovery, used by DiscovererDatabase.
CREATE TABLE proxy_ocr_discoverer_announcements (
	local_peer_id text NOT NULL,
	remote_peer_id text NOT NULL,
	ann bytea NOT NULL,
	created_at timestamptz not null,
	updated_at timestamptz not null,
	PRIMARY KEY(local_peer_id, remote_peer_id)
);
-- +goose Down
DROP TABLE proxy_ocr_discoverer_announcements;
