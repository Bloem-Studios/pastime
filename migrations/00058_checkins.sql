-- Check-ins: an activity a profile has started and not yet finished. Finishing a
-- "play" check-in (films, episodes, videos, podcast episodes, music, board games)
-- records a play with the real time spent; finishing a "session" check-in (books,
-- comics, manga, audiobooks, games) records time and amount read or played and moves
-- progress on, completing the work only when it is done.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE checkins (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile_id bigint NOT NULL REFERENCES tracking_profiles(id) ON DELETE CASCADE,
    entry_id bigint NOT NULL,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('play', 'session')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'finished', 'cancelled')),
    started_at timestamptz NOT NULL,
    expected_end_at timestamptz,
    finished_at timestamptz,
    minutes integer CHECK (minutes BETWEEN 0 AND 100000),
    amount integer CHECK (amount BETWEEN 0 AND 1000000),
    completed boolean NOT NULL DEFAULT false,
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object' AND octet_length(details::text) <= 16384),
    play_id bigint REFERENCES plays(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (profile_id, entry_id) REFERENCES library_entries(profile_id, id) ON DELETE CASCADE,
    CHECK ((status = 'active') = (finished_at IS NULL)),
    CHECK (expected_end_at IS NULL OR expected_end_at >= started_at)
);
-- +goose StatementEnd
CREATE UNIQUE INDEX checkins_one_active ON checkins (profile_id, catalog_item_id) WHERE status = 'active';
CREATE INDEX checkins_due ON checkins (expected_end_at) WHERE status = 'active' AND expected_end_at IS NOT NULL;
CREATE INDEX checkins_profile_recent ON checkins (profile_id, started_at DESC, id DESC);

-- Board game results and the platform of a game session ride on the play they produced.
ALTER TABLE plays ADD COLUMN details jsonb CHECK (details IS NULL OR (jsonb_typeof(details) = 'object' AND octet_length(details::text) <= 16384));

-- A play keeps minutes it was given (a check-in's real time); otherwise, as before, it
-- takes the runtime of its title, or for an episode its show's typical runtime.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plays_classify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT e.media_type INTO STRICT NEW.media_type FROM library_entries e WHERE e.user_id=NEW.user_id AND e.id=NEW.entry_id;
 IF NEW.minutes IS NULL AND current_setting('pastime.native_restore',true) IS DISTINCT FROM 'on' THEN
  SELECT COALESCE(c.runtime_minutes,r.runtime_minutes) INTO NEW.minutes FROM catalog_items c JOIN catalog_items r ON r.id=c.root_id WHERE c.id=NEW.catalog_item_id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plays_classify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT e.media_type INTO STRICT NEW.media_type FROM library_entries e WHERE e.user_id=NEW.user_id AND e.id=NEW.entry_id;
 IF current_setting('pastime.native_restore',true) IS DISTINCT FROM 'on' THEN
  SELECT COALESCE(c.runtime_minutes,r.runtime_minutes) INTO NEW.minutes FROM catalog_items c JOIN catalog_items r ON r.id=c.root_id WHERE c.id=NEW.catalog_item_id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE plays DROP COLUMN details;
DROP TABLE checkins;
