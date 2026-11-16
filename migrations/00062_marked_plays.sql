-- Bulk "mark as played": three or more different titles or episodes recorded for one
-- profile in the same minute cannot all have been watched. Media servers record them
-- when someone marks a season or a whole show played, and imports bring them in as
-- dated plays (on 2026-11-04, 31% of the plays Bloem had sent, 26% of their hours).
-- Such plays are marked: they still count toward progress and completion, but not
-- toward time, plays, active days or streaks, so the daily rollup leaves them out.
-- +goose Up
ALTER TABLE plays ADD COLUMN marked boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION play_days_add() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO play_days AS d (profile_id, day, media_type, plays, minutes, unknown_runtime)
    SELECT profile_id, (played_at AT TIME ZONE 'UTC')::date, media_type, count(*), COALESCE(sum(minutes), 0), count(*) FILTER(WHERE minutes IS NULL)
    FROM added WHERE NOT marked GROUP BY 1, 2, 3
    ON CONFLICT (profile_id, day, media_type) DO UPDATE SET plays = d.plays + EXCLUDED.plays, minutes = d.minutes + EXCLUDED.minutes, unknown_runtime=d.unknown_runtime+EXCLUDED.unknown_runtime;
    RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION play_days_remove() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE play_days d SET plays = d.plays - r.plays, minutes = d.minutes - r.minutes, unknown_runtime=d.unknown_runtime-r.unknown_runtime
    FROM (SELECT profile_id, (played_at AT TIME ZONE 'UTC')::date AS day, media_type, count(*) AS plays, COALESCE(sum(minutes), 0) AS minutes,count(*) FILTER(WHERE minutes IS NULL) AS unknown_runtime
          FROM removed WHERE NOT marked GROUP BY 1, 2, 3) r
    WHERE d.profile_id = r.profile_id AND d.day = r.day AND d.media_type = r.media_type;
    DELETE FROM play_days d USING (SELECT DISTINCT profile_id FROM removed) u WHERE d.profile_id = u.profile_id AND d.plays = 0;
    RETURN NULL;
END $$;
-- +goose StatementEnd

-- Runs after play_days_add (triggers fire by name), so a burst completed by this
-- statement takes its earlier plays and the new ones back out of the rollup.
-- +goose StatementBegin
CREATE FUNCTION plays_mark_bursts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    WITH touched AS (SELECT DISTINCT profile_id, date_trunc('minute', played_at) AS m FROM added),
    bursts AS (
        SELECT t.profile_id, t.m FROM touched t
        WHERE (SELECT count(DISTINCT p.catalog_item_id) FROM plays p
               WHERE p.profile_id = t.profile_id AND p.played_at >= t.m AND p.played_at < t.m + interval '1 minute') >= 3),
    newly AS (
        UPDATE plays p SET marked = true FROM bursts b
        WHERE p.profile_id = b.profile_id AND p.played_at >= b.m AND p.played_at < b.m + interval '1 minute' AND NOT p.marked
        RETURNING p.profile_id, p.played_at, p.media_type, p.minutes)
    UPDATE play_days d SET plays = d.plays - r.plays, minutes = d.minutes - r.minutes, unknown_runtime = d.unknown_runtime - r.unknown_runtime
    FROM (SELECT profile_id, (played_at AT TIME ZONE 'UTC')::date AS day, media_type, count(*) AS plays, COALESCE(sum(minutes), 0) AS minutes,
                 count(*) FILTER (WHERE minutes IS NULL) AS unknown_runtime
          FROM newly GROUP BY 1, 2, 3) r
    WHERE d.profile_id = r.profile_id AND d.day = r.day AND d.media_type = r.media_type;
    DELETE FROM play_days d USING (SELECT DISTINCT profile_id FROM added) u WHERE d.profile_id = u.profile_id AND d.plays = 0;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER plays_mark_bursts AFTER INSERT ON plays REFERENCING NEW TABLE AS added FOR EACH STATEMENT EXECUTE FUNCTION plays_mark_bursts();

-- Existing plays: mark every burst, then rebuild the rollup from the plays that count.
WITH bursts AS (
    SELECT profile_id, date_trunc('minute', played_at) AS m FROM plays
    GROUP BY 1, 2 HAVING count(DISTINCT catalog_item_id) >= 3)
UPDATE plays p SET marked = true FROM bursts b
WHERE p.profile_id = b.profile_id AND p.played_at >= b.m AND p.played_at < b.m + interval '1 minute';
DELETE FROM play_days;
INSERT INTO play_days (profile_id, day, media_type, plays, minutes, unknown_runtime)
SELECT profile_id, (played_at AT TIME ZONE 'UTC')::date, media_type, count(*), COALESCE(sum(minutes), 0), count(*) FILTER (WHERE minutes IS NULL)
FROM plays WHERE NOT marked GROUP BY 1, 2, 3;

-- +goose Down
DROP TRIGGER plays_mark_bursts ON plays;
DROP FUNCTION plays_mark_bursts();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION play_days_add() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO play_days AS d (profile_id, day, media_type, plays, minutes, unknown_runtime)
    SELECT profile_id, (played_at AT TIME ZONE 'UTC')::date, media_type, count(*), COALESCE(sum(minutes), 0), count(*) FILTER(WHERE minutes IS NULL)
    FROM added GROUP BY 1, 2, 3
    ON CONFLICT (profile_id, day, media_type) DO UPDATE SET plays = d.plays + EXCLUDED.plays, minutes = d.minutes + EXCLUDED.minutes, unknown_runtime=d.unknown_runtime+EXCLUDED.unknown_runtime;
    RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION play_days_remove() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE play_days d SET plays = d.plays - r.plays, minutes = d.minutes - r.minutes, unknown_runtime=d.unknown_runtime-r.unknown_runtime
    FROM (SELECT profile_id, (played_at AT TIME ZONE 'UTC')::date AS day, media_type, count(*) AS plays, COALESCE(sum(minutes), 0) AS minutes,count(*) FILTER(WHERE minutes IS NULL) AS unknown_runtime
          FROM removed GROUP BY 1, 2, 3) r
    WHERE d.profile_id = r.profile_id AND d.day = r.day AND d.media_type = r.media_type;
    DELETE FROM play_days d USING (SELECT DISTINCT profile_id FROM removed) u WHERE d.profile_id = u.profile_id AND d.plays = 0;
    RETURN NULL;
END $$;
-- +goose StatementEnd
ALTER TABLE plays DROP COLUMN marked;
DELETE FROM play_days;
INSERT INTO play_days (profile_id, day, media_type, plays, minutes, unknown_runtime)
SELECT profile_id, (played_at AT TIME ZONE 'UTC')::date, media_type, count(*), COALESCE(sum(minutes), 0), count(*) FILTER (WHERE minutes IS NULL)
FROM plays GROUP BY 1, 2, 3;
