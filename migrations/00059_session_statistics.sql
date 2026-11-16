-- Statistics read finished reading and game sessions by profile and finish time.
-- +goose Up
CREATE INDEX checkins_finished_sessions ON checkins (profile_id, finished_at) WHERE kind = 'session' AND status = 'finished';

-- +goose Down
DROP INDEX checkins_finished_sessions;
