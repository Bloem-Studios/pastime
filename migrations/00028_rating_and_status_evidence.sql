-- +goose Up
ALTER TABLE library_entries ADD COLUMN scored_at timestamptz;
CREATE TABLE entry_status_history(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 entry_id bigint NOT NULL REFERENCES library_entries(id) ON DELETE CASCADE,
 previous_status text,
 status text NOT NULL,
 source text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX entry_status_history_owner ON entry_status_history(user_id,entry_id,id DESC);
CREATE TABLE episode_ratings(
 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 entry_id bigint NOT NULL REFERENCES library_entries(id) ON DELETE CASCADE,
 catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 rating integer CHECK(rating BETWEEN 0 AND 10),
 scored_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version integer NOT NULL DEFAULT 1,
 PRIMARY KEY(user_id,entry_id,catalog_item_id)
);
-- +goose StatementBegin
CREATE FUNCTION stamp_rating_time() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('pastime.native_restore',true)='on' THEN RETURN NEW; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.rating IS NOT NULL THEN NEW.scored_at=COALESCE(NEW.scored_at,clock_timestamp()); END IF;
 ELSIF NEW.rating IS DISTINCT FROM OLD.rating THEN
  IF NEW.scored_at IS NULL OR NEW.scored_at IS NOT DISTINCT FROM OLD.scored_at THEN NEW.scored_at=clock_timestamp(); END IF;
 ELSE NEW.scored_at=OLD.scored_at;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER stamp_rating_time BEFORE INSERT OR UPDATE ON library_entries FOR EACH ROW EXECUTE FUNCTION stamp_rating_time();
CREATE FUNCTION record_status_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE origin text;
BEGIN
 IF current_setting('pastime.native_restore',true)='on' THEN RETURN NEW; END IF;
 origin=COALESCE(NULLIF(current_setting('pastime.change_source',true),''),CASE WHEN current_setting('pastime.user_mutation',true)='true' THEN 'user' ELSE 'tracking' END);
 IF TG_OP='INSERT' THEN
  INSERT INTO entry_status_history(user_id,entry_id,status,source) VALUES(NEW.user_id,NEW.id,NEW.status,origin);
 ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
  INSERT INTO entry_status_history(user_id,entry_id,previous_status,status,source) VALUES(NEW.user_id,NEW.id,OLD.status,NEW.status,origin);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER record_status_change AFTER INSERT OR UPDATE ON library_entries FOR EACH ROW EXECUTE FUNCTION record_status_change();
-- +goose StatementEnd
