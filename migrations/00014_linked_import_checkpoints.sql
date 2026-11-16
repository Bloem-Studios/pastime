-- +goose Up
ALTER TABLE service_links ADD COLUMN generation bigint NOT NULL DEFAULT 1;
CREATE TABLE import_batches (
    run_id uuid PRIMARY KEY REFERENCES import_runs(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    link_id bigint NOT NULL REFERENCES service_links(id) ON DELETE CASCADE,
    generation bigint NOT NULL,
    records jsonb NOT NULL CHECK (jsonb_typeof(records)='array')
);
