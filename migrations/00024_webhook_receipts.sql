-- +goose Up
CREATE TABLE webhook_receipts (
 connection_id uuid NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
 event_digest bytea NOT NULL CHECK(octet_length(event_digest)=32),
 intent_digest bytea NOT NULL CHECK(octet_length(intent_digest)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(connection_id,event_digest)
);
-- +goose Down
DROP TABLE webhook_receipts;
