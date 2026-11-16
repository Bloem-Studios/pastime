-- +goose Up
ALTER TABLE personal_lists ADD COLUMN source_uid uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX personal_lists_source_uid ON personal_lists(source_uid);
ALTER TABLE collab_lists ADD COLUMN source_uid uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX collab_lists_source_uid ON collab_lists(source_uid);

CREATE TABLE bloem_list_export_snapshots (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 instance_id text NOT NULL REFERENCES bloem_instances(id) ON DELETE CASCADE,
 profile_native_id bigint NOT NULL REFERENCES tracking_profiles(id) ON DELETE CASCADE,
 account_id text NOT NULL,
 profile_id text NOT NULL,
 source_revision bigint NOT NULL,
 source_id text NOT NULL,
 resolution_token text NOT NULL,
 total integer NOT NULL CHECK(total BETWEEN 0 AND 10000),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 generated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 CHECK(expires_at>generated_at AND expires_at<=generated_at+interval '120 seconds')
);
CREATE INDEX bloem_list_export_expiry ON bloem_list_export_snapshots(profile_native_id,expires_at);
CREATE INDEX bloem_list_export_global_expiry ON bloem_list_export_snapshots(expires_at,id);
CREATE TABLE bloem_list_export_rows (
 snapshot_id uuid NOT NULL REFERENCES bloem_list_export_snapshots(id) ON DELETE CASCADE,
 ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 9999),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(snapshot_id,ordinal)
);

-- +goose Down
DROP TABLE bloem_list_export_rows;
DROP TABLE bloem_list_export_snapshots;
ALTER TABLE collab_lists DROP COLUMN source_uid;
ALTER TABLE personal_lists DROP COLUMN source_uid;
