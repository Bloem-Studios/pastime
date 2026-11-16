-- +goose Up
-- One row per running server or worker process, refreshed by its heartbeat. Rows unseen
-- for a day are pruned by the next heartbeat of any process.
CREATE TABLE runtime_nodes (
    id uuid PRIMARY KEY,
    role text NOT NULL CHECK (role IN ('server','worker')),
    hostname text NOT NULL CHECK (length(hostname) <= 255),
    version text NOT NULL CHECK (length(version) <= 100),
    started_at timestamptz NOT NULL,
    seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    stats jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(stats)='object')
);
CREATE INDEX runtime_nodes_seen ON runtime_nodes (seen_at DESC);

-- Administration reads jobs newest first, by status, and by kind.
CREATE INDEX jobs_recent ON jobs (created_at DESC, id DESC);
CREATE INDEX jobs_by_status ON jobs (status, created_at DESC, id DESC);
CREATE INDEX jobs_by_kind ON jobs (kind, created_at DESC, id DESC);
CREATE INDEX jobs_settled ON jobs (updated_at) WHERE status IN ('succeeded','failed','blocked');
CREATE INDEX import_runs_recent ON import_runs (created_at DESC, id DESC);
