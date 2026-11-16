-- +goose Up
-- Membership is independent of progress, status, ratings, notes and history.
CREATE TABLE profile_catalog_memberships (
 profile_id bigint NOT NULL REFERENCES tracking_profiles(id) ON DELETE CASCADE,
 catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('favorite','watchlist')),
 present boolean NOT NULL,
 position integer CHECK(position>=0),
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 source text NOT NULL,
 PRIMARY KEY(profile_id,catalog_item_id,kind)
);
CREATE INDEX profile_catalog_memberships_page ON profile_catalog_memberships(profile_id,kind,position,catalog_item_id) WHERE present;
CREATE TABLE bloem_personal_entity_states (
 instance_id text NOT NULL REFERENCES bloem_instances(id),
 profile_id bigint NOT NULL REFERENCES tracking_profiles(id) ON DELETE CASCADE,
 entity_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('favorite','watchlist','rating')),
 target_catalog_id bigint NOT NULL REFERENCES catalog_items(id),
 source_revision bigint NOT NULL CHECK(source_revision>0),
 payload_digest bytea NOT NULL,
 PRIMARY KEY(instance_id,profile_id,entity_id)
);
-- Preserve existing native planned titles as watchlist membership.
INSERT INTO profile_catalog_memberships(profile_id,catalog_item_id,kind,present,changed_at,source)
 SELECT profile_id,catalog_item_id,'watchlist',true,created_at,'native'
 FROM library_entries WHERE catalog_item_id IS NOT NULL AND status='planned';

CREATE TABLE bloem_personal_read_snapshots (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 instance_id text NOT NULL REFERENCES bloem_instances(id),
 profile_id bigint NOT NULL REFERENCES tracking_profiles(id) ON DELETE CASCADE,
 credential_id text NOT NULL,
 kinds text[] NOT NULL,
 page_size integer NOT NULL CHECK(page_size BETWEEN 1 AND 100),
 complete_snapshot boolean NOT NULL,
 warnings jsonb NOT NULL,
 items jsonb NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '10 minutes'
);
CREATE INDEX bloem_personal_read_expiry ON bloem_personal_read_snapshots(expires_at);
CREATE INDEX bloem_personal_read_owner ON bloem_personal_read_snapshots(instance_id,profile_id,expires_at);
CREATE TABLE bloem_personal_read_pages (
 token uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 snapshot_id uuid NOT NULL REFERENCES bloem_personal_read_snapshots(id) ON DELETE CASCADE,
 ordinal integer NOT NULL CHECK(ordinal>=0),
 UNIQUE(snapshot_id,ordinal)
);

-- +goose Down
DROP TABLE bloem_personal_read_pages;
DROP TABLE bloem_personal_read_snapshots;
DROP TABLE bloem_personal_entity_states;
DROP TABLE profile_catalog_memberships;
