-- +goose Up
ALTER TABLE users ADD COLUMN transfer_namespace uuid NOT NULL DEFAULT gen_random_uuid();
CREATE TABLE native_restore_receipts(user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,namespace uuid NOT NULL,digest bytea NOT NULL CHECK(octet_length(digest)=32),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(user_id,digest));
-- Preserve exported runtime evidence instead of re-estimating historical durations.
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
