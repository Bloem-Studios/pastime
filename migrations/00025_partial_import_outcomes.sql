-- +goose Up
ALTER TABLE import_runs DROP CONSTRAINT import_runs_status_check;
ALTER TABLE import_runs ADD CONSTRAINT import_runs_status_check CHECK(status IN ('queued','running','succeeded','partial_success','failed','cancelled','rolled_back'));
