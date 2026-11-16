-- +goose Up
CREATE TABLE bloem_sync_credentials (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 issuance_id text NOT NULL CHECK(length(issuance_id) BETWEEN 1 AND 128),
 token_digest bytea NOT NULL UNIQUE CHECK(octet_length(token_digest)=32),
 credential_ciphertext bytea NOT NULL,
 revoked_at timestamptz,
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '90 days',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(instance_id,profile_id,issuance_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
CREATE INDEX bloem_sync_credentials_profile ON bloem_sync_credentials(instance_id,profile_id);
-- +goose Down
DROP TABLE bloem_sync_credentials;
