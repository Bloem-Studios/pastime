-- +goose Up
CREATE TABLE playback_deliveries (
 link_id bigint NOT NULL REFERENCES service_links(id) ON DELETE CASCADE,
 generation bigint NOT NULL,
 play_id bigint NOT NULL REFERENCES plays(id) ON DELETE CASCADE,
 attempts integer NOT NULL DEFAULT 1,
 next_attempt_at timestamptz NOT NULL,
 delivered_at timestamptz,
 PRIMARY KEY(link_id,generation,play_id)
);
CREATE INDEX plays_created ON plays(user_id,created_at,id);
-- +goose Down
DROP INDEX plays_created;
DROP TABLE playback_deliveries;
