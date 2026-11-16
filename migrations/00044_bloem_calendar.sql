-- +goose Up
CREATE TABLE bloem_calendar_snapshots (
 id text PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 128),
 instance_id text NOT NULL REFERENCES bloem_instances(id),
 profile_id bigint NOT NULL,
 source_account_id text NOT NULL,
 source_profile_id text NOT NULL,
 start_date date NOT NULL,
 end_date date NOT NULL CHECK(end_date>=start_date AND end_date-start_date<=30),
 visibility_digest text NOT NULL CHECK(length(visibility_digest) BETWEEN 1 AND 256),
 source_revision bigint NOT NULL CHECK(source_revision>0),
 next_cursor text NOT NULL DEFAULT '',
 complete boolean NOT NULL DEFAULT false,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(id,instance_id,profile_id),
 FOREIGN KEY(instance_id,profile_id) REFERENCES bloem_profiles(instance_id,profile_id)
);
CREATE INDEX bloem_calendar_snapshots_expiry ON bloem_calendar_snapshots(expires_at);
CREATE TABLE bloem_calendar_pages (
 snapshot_id text NOT NULL REFERENCES bloem_calendar_snapshots(id) ON DELETE CASCADE,
 cursor text NOT NULL,
 payload_digest bytea NOT NULL CHECK(octet_length(payload_digest)=32),
 next_cursor text NOT NULL,
 complete boolean NOT NULL,
 PRIMARY KEY(snapshot_id,cursor)
);
CREATE TABLE bloem_calendar_events (
 snapshot_id text NOT NULL REFERENCES bloem_calendar_snapshots(id) ON DELETE CASCADE,
 event_id text NOT NULL CHECK(length(event_id) BETWEEN 1 AND 256),
 event_day date NOT NULL,
 payload jsonb NOT NULL,
 PRIMARY KEY(snapshot_id,event_id)
);
CREATE INDEX bloem_calendar_events_day ON bloem_calendar_events(event_day,snapshot_id);
CREATE TABLE bloem_calendar_windows (
 instance_id text NOT NULL,
 profile_id bigint NOT NULL,
 start_date date NOT NULL,
 end_date date NOT NULL,
 snapshot_id text NOT NULL,
 PRIMARY KEY(instance_id,profile_id,start_date,end_date),
 FOREIGN KEY(snapshot_id,instance_id,profile_id) REFERENCES bloem_calendar_snapshots(id,instance_id,profile_id)
);

-- +goose Down
DROP TABLE bloem_calendar_windows;
DROP TABLE bloem_calendar_events;
DROP TABLE bloem_calendar_pages;
DROP TABLE bloem_calendar_snapshots;
