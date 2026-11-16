-- +goose Up
CREATE TABLE library_tombstones(
 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 deleted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(user_id,catalog_item_id)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_library_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('pastime.user_mutation',true)='true' AND OLD.catalog_item_id IS NOT NULL THEN
  INSERT INTO library_tombstones(user_id,catalog_item_id) VALUES(OLD.user_id,OLD.catalog_item_id)
  ON CONFLICT(user_id,catalog_item_id) DO UPDATE SET deleted_at=clock_timestamp();
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER preserve_library_deletion BEFORE DELETE ON library_entries FOR EACH ROW EXECUTE FUNCTION preserve_library_deletion();
-- +goose StatementEnd
