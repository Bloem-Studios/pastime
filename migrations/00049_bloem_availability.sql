-- +goose Up
CREATE TABLE bloem_availability (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 catalog_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 request_id text NOT NULL UNIQUE,
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 checked_at timestamptz,
 expires_at timestamptz,
 available boolean NOT NULL DEFAULT false,
 content_id text NOT NULL DEFAULT '',
 PRIMARY KEY(instance_id,profile_id,catalog_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id) ON DELETE CASCADE,
 CHECK(available OR content_id='')
);
CREATE INDEX bloem_availability_pending ON bloem_availability(instance_id,requested_at) WHERE checked_at IS NULL;
-- +goose Down
DROP TABLE bloem_availability;
