-- +goose Up
CREATE TABLE notification_deliveries (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 target_id bigint NOT NULL REFERENCES notification_targets(id) ON DELETE CASCADE,
 notice jsonb NOT NULL,
 attempts integer NOT NULL DEFAULT 0,
 leased_until timestamptz,
 delivered_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX notification_deliveries_pending ON notification_deliveries(user_id,id) WHERE delivered_at IS NULL;

-- +goose Down
DROP TABLE notification_deliveries;
