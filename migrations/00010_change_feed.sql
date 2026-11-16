-- +goose Up
-- A per-account change feed for sync clients. Statement triggers record which entries
-- and plays changed, including deletions, so a client can catch up from a cursor
-- instead of downloading the whole library. Kept for 90 days.
CREATE TABLE change_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity text NOT NULL CHECK (entity IN ('entry','play')),
    entity_id bigint NOT NULL,
    op text NOT NULL CHECK (op IN ('upsert','delete')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX change_log_feed ON change_log(user_id, id);
CREATE INDEX change_log_age ON change_log(created_at);

-- Deletions cascading from a removed account are not logged; its feed goes with it.
-- +goose StatementBegin
CREATE FUNCTION change_log_entries() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT o.user_id,'entry',o.id,'delete' FROM old_rows o WHERE EXISTS(SELECT 1 FROM users u WHERE u.id=o.user_id);
    ELSE
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT user_id,'entry',id,'upsert' FROM new_rows;
    END IF;
    RETURN NULL;
END $$;
CREATE FUNCTION change_log_plays() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT o.user_id,'play',o.id,'delete' FROM old_rows o WHERE EXISTS(SELECT 1 FROM users u WHERE u.id=o.user_id);
    ELSE
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT user_id,'play',id,'upsert' FROM new_rows;
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER library_entries_changes_insert AFTER INSERT ON library_entries REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION change_log_entries();
CREATE TRIGGER library_entries_changes_update AFTER UPDATE ON library_entries REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION change_log_entries();
CREATE TRIGGER library_entries_changes_delete AFTER DELETE ON library_entries REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION change_log_entries();
CREATE TRIGGER plays_changes_insert AFTER INSERT ON plays REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION change_log_plays();
CREATE TRIGGER plays_changes_update AFTER UPDATE ON plays REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION change_log_plays();
CREATE TRIGGER plays_changes_delete AFTER DELETE ON plays REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION change_log_plays();
