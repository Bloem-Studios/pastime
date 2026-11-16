-- +goose Up
CREATE TABLE bloem_instances (
 id text PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 128),
 url text NOT NULL,
 enrollment_scope text NOT NULL CHECK(length(enrollment_scope) BETWEEN 1 AND 128),
 identity_issuer text NOT NULL DEFAULT '',
 provision_digest bytea NOT NULL UNIQUE CHECK(octet_length(provision_digest)=32),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE bloem_accounts (
 instance_id text NOT NULL REFERENCES bloem_instances(id),
 source_account_id text NOT NULL CHECK(length(source_account_id) BETWEEN 1 AND 128),
 account_id bigint NOT NULL REFERENCES users(id),
 default_source_profile_id text NOT NULL,
 source_revision bigint NOT NULL CHECK(source_revision>0),
 source_fingerprint bytea NOT NULL CHECK(octet_length(source_fingerprint)=32),
 active boolean NOT NULL,
 link_status text NOT NULL CHECK(link_status IN ('pending','linked')),
 identity_issuer text NOT NULL DEFAULT '',
 identity_subject text NOT NULL DEFAULT '',
 owns_account boolean NOT NULL,
 PRIMARY KEY(instance_id,source_account_id),
 UNIQUE(instance_id,account_id),
 UNIQUE(instance_id,source_account_id,account_id)
);
CREATE TABLE bloem_profiles (
 instance_id text NOT NULL,
 source_account_id text NOT NULL,
 source_profile_id text NOT NULL CHECK(length(source_profile_id) BETWEEN 1 AND 128),
 account_id bigint NOT NULL,
 profile_id bigint NOT NULL,
 source_revision bigint NOT NULL DEFAULT 0 CHECK(source_revision>=0),
 source_fingerprint bytea,
 active boolean NOT NULL DEFAULT true,
 excluded boolean NOT NULL DEFAULT false,
 owns_profile boolean NOT NULL,
 PRIMARY KEY(instance_id,source_account_id,source_profile_id),
 UNIQUE(instance_id,profile_id),
 FOREIGN KEY(instance_id,source_account_id,account_id) REFERENCES bloem_accounts(instance_id,source_account_id,account_id),
 FOREIGN KEY(account_id,profile_id) REFERENCES tracking_profiles(account_id,id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a protected pre-integration backup with writes paused; managed tracking cannot be destructively downgraded.'; END $$;
-- +goose StatementEnd
