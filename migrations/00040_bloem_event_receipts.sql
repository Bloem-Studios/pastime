-- +goose Up
CREATE TABLE bloem_event_receipts (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 event_id text NOT NULL CHECK(length(event_id) BETWEEN 1 AND 128),
 payload_digest bytea NOT NULL CHECK(octet_length(payload_digest)=32),
 result text NOT NULL CHECK(result IN ('applied','stale')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(instance_id,profile_id,event_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
CREATE TABLE bloem_entity_states (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 entity_id text NOT NULL CHECK(length(entity_id) BETWEEN 1 AND 128),
 target_catalog_id bigint NOT NULL REFERENCES catalog_items(id),
 source_revision bigint NOT NULL CHECK(source_revision>0),
 payload_digest bytea NOT NULL CHECK(octet_length(payload_digest)=32),
 PRIMARY KEY(instance_id,profile_id,entity_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
CREATE TABLE bloem_consumption_receipts (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 consumption_id text NOT NULL CHECK(length(consumption_id) BETWEEN 1 AND 128),
 payload_digest bytea NOT NULL CHECK(octet_length(payload_digest)=32),
 PRIMARY KEY(instance_id,profile_id,consumption_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a protected pre-integration backup with writes paused; replay receipts cannot be destructively downgraded.'; END $$;
-- +goose StatementEnd
