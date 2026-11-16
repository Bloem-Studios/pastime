-- What a browser session signed in with and when it was last used, so a person can tell
-- their sessions apart. last_used_at is written at most once per few minutes; sessions
-- issued before this migration have none until their next use.
-- +goose Up
ALTER TABLE sessions ADD COLUMN user_agent text NOT NULL DEFAULT '' CHECK (length(user_agent) <= 512);
ALTER TABLE sessions ADD COLUMN last_used_at timestamptz;

-- +goose Down
ALTER TABLE sessions DROP COLUMN last_used_at;
ALTER TABLE sessions DROP COLUMN user_agent;
