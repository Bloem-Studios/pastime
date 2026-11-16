-- The import run that wrote an activity event, so the activity log can name the import
-- and its source instead of matching events to runs by time.
-- +goose Up
ALTER TABLE activity_events ADD COLUMN import_run uuid REFERENCES import_runs(id) ON DELETE SET NULL;
CREATE INDEX activity_events_import ON activity_events(import_run) WHERE import_run IS NOT NULL;

-- Earlier imports are known for the titles they added: the entry keeps its run. A native
-- restore copies events with their original times, so its runs are left out.
UPDATE activity_events a SET import_run=e.import_run
FROM library_entries e JOIN import_runs r ON r.id=e.import_run
WHERE a.profile_id=e.profile_id AND a.entry_id=e.id AND a.kind='added' AND a.import_run IS NULL
  AND r.source<>'native' AND a.created_at>=r.created_at;

-- +goose Down
ALTER TABLE activity_events DROP COLUMN import_run;
