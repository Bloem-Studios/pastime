-- +goose Up
ALTER TABLE play_days ADD COLUMN unknown_runtime integer NOT NULL DEFAULT 0 CHECK(unknown_runtime>=0);
UPDATE play_days d SET unknown_runtime=x.n FROM (SELECT user_id,(played_at AT TIME ZONE 'UTC')::date AS day,media_type,count(*) FILTER(WHERE minutes IS NULL) n FROM plays GROUP BY 1,2,3) x WHERE d.user_id=x.user_id AND d.day=x.day AND d.media_type=x.media_type;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION play_days_add() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO play_days AS d (user_id, day, media_type, plays, minutes, unknown_runtime)
    SELECT user_id, (played_at AT TIME ZONE 'UTC')::date, media_type, count(*), COALESCE(sum(minutes), 0), count(*) FILTER(WHERE minutes IS NULL)
    FROM added GROUP BY 1, 2, 3
    ON CONFLICT (user_id, day, media_type) DO UPDATE SET plays = d.plays + EXCLUDED.plays, minutes = d.minutes + EXCLUDED.minutes, unknown_runtime=d.unknown_runtime+EXCLUDED.unknown_runtime;
    RETURN NULL;
END $$;
CREATE OR REPLACE FUNCTION play_days_remove() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE play_days d SET plays = d.plays - r.plays, minutes = d.minutes - r.minutes, unknown_runtime=d.unknown_runtime-r.unknown_runtime
    FROM (SELECT user_id, (played_at AT TIME ZONE 'UTC')::date AS day, media_type, count(*) AS plays, COALESCE(sum(minutes), 0) AS minutes,count(*) FILTER(WHERE minutes IS NULL) AS unknown_runtime
          FROM removed GROUP BY 1, 2, 3) r
    WHERE d.user_id = r.user_id AND d.day = r.day AND d.media_type = r.media_type;
    DELETE FROM play_days d USING (SELECT DISTINCT user_id FROM removed) u WHERE d.user_id = u.user_id AND d.plays = 0;
    RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION play_days_add() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO play_days AS d (user_id, day, media_type, plays, minutes)
    SELECT user_id, (played_at AT TIME ZONE 'UTC')::date, media_type, count(*), COALESCE(sum(minutes), 0)
    FROM added GROUP BY 1, 2, 3
    ON CONFLICT (user_id, day, media_type) DO UPDATE SET plays = d.plays + EXCLUDED.plays, minutes = d.minutes + EXCLUDED.minutes;
    RETURN NULL;
END $$;
CREATE OR REPLACE FUNCTION play_days_remove() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE play_days d SET plays = d.plays - r.plays, minutes = d.minutes - r.minutes
    FROM (SELECT user_id, (played_at AT TIME ZONE 'UTC')::date AS day, media_type, count(*) AS plays, COALESCE(sum(minutes), 0) AS minutes
          FROM removed GROUP BY 1, 2, 3) r
    WHERE d.user_id = r.user_id AND d.day = r.day AND d.media_type = r.media_type;
    DELETE FROM play_days d USING (SELECT DISTINCT user_id FROM removed) u WHERE d.user_id = u.user_id AND d.plays = 0;
    RETURN NULL;
END $$;
-- +goose StatementEnd
ALTER TABLE play_days DROP COLUMN unknown_runtime;
