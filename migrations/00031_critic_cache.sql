-- +goose Up
CREATE TABLE critic_scores(
 catalog_item_id bigint PRIMARY KEY REFERENCES catalog_items(id) ON DELETE CASCADE,
 external_id bigint,
 score double precision CHECK(score BETWEEN 0 AND 100),
 recommended double precision CHECK(recommended BETWEEN 0 AND 100),
 tier text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'pending',
 checked_at timestamptz,
 refresh_after timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE critic_quota(
 provider text PRIMARY KEY,
 window_start date NOT NULL,
 used integer NOT NULL DEFAULT 0,
 next_allowed timestamptz NOT NULL DEFAULT clock_timestamp()
);
