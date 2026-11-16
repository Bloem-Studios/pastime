-- +goose Up
CREATE TABLE bloem_list_signing_keys (
 instance_id text PRIMARY KEY REFERENCES bloem_instances(id) ON DELETE CASCADE,
 seed_cipher bytea NOT NULL CHECK(octet_length(seed_cipher)>32)
);

-- +goose Down
DROP TABLE bloem_list_signing_keys;
