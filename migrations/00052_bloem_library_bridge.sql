-- +goose Up
CREATE TABLE bloem_library_requests (
 id text PRIMARY KEY,
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 query_digest bytea NOT NULL,
 query jsonb NOT NULL CHECK(jsonb_typeof(query)='object'),
 account_revision bigint NOT NULL,
 profile_revision bigint NOT NULL,
 pending boolean NOT NULL DEFAULT true,
 failure text NOT NULL DEFAULT '' CHECK(failure IN('','denied','invalid')),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_attempt_at timestamptz NOT NULL DEFAULT to_timestamp(0),
 expires_at timestamptz,
 payload jsonb,
 UNIQUE(instance_id,profile_id,query_digest),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
CREATE INDEX bloem_library_requests_pending ON bloem_library_requests(instance_id,last_attempt_at) WHERE pending;
CREATE TABLE bloem_library_mutations (
 id text PRIMARY KEY,
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 event_id text NOT NULL,
 intent jsonb NOT NULL CHECK(jsonb_typeof(intent)='object'),
 account_revision bigint NOT NULL,
 profile_revision bigint NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','applied','no_change','removed','conflict','denied','invalid')),
 receipt jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_attempt_at timestamptz NOT NULL DEFAULT to_timestamp(0),
 UNIQUE(instance_id,profile_id,event_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
CREATE INDEX bloem_library_mutations_pending ON bloem_library_mutations(instance_id,last_attempt_at) WHERE status='pending';
-- +goose Down
DROP TABLE bloem_library_mutations;
DROP TABLE bloem_library_requests;
