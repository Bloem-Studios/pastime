-- +goose Up
-- A complete personal AI projection has a short host-authority lease.
CREATE TABLE bloem_ai_recommendations (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 generated_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='array'),
 PRIMARY KEY(instance_id,profile_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
-- +goose Down
DROP TABLE bloem_ai_recommendations;
