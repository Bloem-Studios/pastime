-- +goose Up
CREATE TABLE bloem_native_copy_bindings (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 source_copy_id text NOT NULL,
 native_copy_id bigint REFERENCES owned_copies(id) ON DELETE SET NULL,
 source_revision bigint NOT NULL CHECK(source_revision>0),
 native_version integer NOT NULL CHECK(native_version>0),
 payload_digest bytea NOT NULL CHECK(octet_length(payload_digest)=32),
 PRIMARY KEY(instance_id,profile_id,source_copy_id),
 UNIQUE(native_copy_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
-- +goose Down
DROP TABLE bloem_native_copy_bindings;
