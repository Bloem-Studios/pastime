-- +goose Up
ALTER TABLE export_schedules ADD COLUMN anchor_day integer;
UPDATE export_schedules SET anchor_day=extract(day FROM next_run_at AT TIME ZONE 'UTC');
ALTER TABLE export_schedules ALTER COLUMN anchor_day SET NOT NULL;
ALTER TABLE export_schedules ADD CONSTRAINT export_anchor_day CHECK(anchor_day BETWEEN 1 AND 31);
ALTER TABLE export_files ADD COLUMN schedule_due timestamptz;
CREATE UNIQUE INDEX export_files_schedule_receipt ON export_files(user_id,schedule_due) WHERE schedule_due IS NOT NULL;
