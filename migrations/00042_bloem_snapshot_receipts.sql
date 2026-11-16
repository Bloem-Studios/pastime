-- +goose Up
CREATE TABLE bloem_snapshots (
 instance_id text NOT NULL REFERENCES bloem_instances(id),snapshot_id text NOT NULL,
 next_cursor text NOT NULL DEFAULT '',final_cursor text NOT NULL DEFAULT '',pages integer NOT NULL DEFAULT 0,
 max_revision bigint NOT NULL DEFAULT 0,complete boolean NOT NULL DEFAULT false,
 finalized boolean NOT NULL DEFAULT false,expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '10 minutes',
 PRIMARY KEY(instance_id,snapshot_id));
CREATE TABLE bloem_snapshot_pages (
 instance_id text NOT NULL,snapshot_id text NOT NULL,cursor text NOT NULL,
 receipt uuid NOT NULL DEFAULT gen_random_uuid(),payload_digest bytea NOT NULL,
 PRIMARY KEY(instance_id,snapshot_id,cursor),UNIQUE(instance_id,receipt),
 FOREIGN KEY(instance_id,snapshot_id) REFERENCES bloem_snapshots(instance_id,snapshot_id) ON DELETE CASCADE);
CREATE TABLE bloem_snapshot_profiles (
 instance_id text NOT NULL,snapshot_id text NOT NULL,source_account_id text NOT NULL,source_profile_id text NOT NULL,
 PRIMARY KEY(instance_id,snapshot_id,source_account_id,source_profile_id),
 FOREIGN KEY(instance_id,snapshot_id) REFERENCES bloem_snapshots(instance_id,snapshot_id) ON DELETE CASCADE);
-- +goose Down
DROP TABLE bloem_snapshot_profiles,bloem_snapshot_pages,bloem_snapshots;
