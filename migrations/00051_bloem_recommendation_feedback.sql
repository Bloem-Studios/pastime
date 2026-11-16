-- +goose Up
CREATE TABLE bloem_recommendation_feedback (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 content_id text NOT NULL,
 title text NOT NULL,
 dismissed boolean NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 acknowledged_revision bigint NOT NULL DEFAULT 0 CHECK(acknowledged_revision>=0),
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_attempt_at timestamptz NOT NULL DEFAULT to_timestamp(0),
 PRIMARY KEY(instance_id,profile_id,content_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
CREATE INDEX bloem_recommendation_feedback_pending ON bloem_recommendation_feedback(instance_id,changed_at) WHERE acknowledged_revision<revision;
-- +goose Down
DROP TABLE bloem_recommendation_feedback;
