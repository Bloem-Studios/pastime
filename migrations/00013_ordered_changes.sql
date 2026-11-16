-- +goose Up
-- Lock each account before sequence allocation, retaining the lock through commit.
-- Other accounts remain independent. A feed cursor cannot skip a later commit.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION change_log_entries() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('pastime.change.' || user_id::text, 0)) FROM (SELECT DISTINCT user_id FROM old_rows ORDER BY user_id) owners;
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT o.user_id,'entry',o.id,'delete' FROM old_rows o WHERE EXISTS(SELECT 1 FROM users u WHERE u.id=o.user_id);
    ELSE
        PERFORM pg_advisory_xact_lock(hashtextextended('pastime.change.' || user_id::text, 0)) FROM (SELECT DISTINCT user_id FROM new_rows ORDER BY user_id) owners;
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT user_id,'entry',id,'upsert' FROM new_rows;
    END IF;
    RETURN NULL;
END $$;
CREATE OR REPLACE FUNCTION change_log_plays() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('pastime.change.' || user_id::text, 0)) FROM (SELECT DISTINCT user_id FROM old_rows ORDER BY user_id) owners;
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT o.user_id,'play',o.id,'delete' FROM old_rows o WHERE EXISTS(SELECT 1 FROM users u WHERE u.id=o.user_id);
    ELSE
        PERFORM pg_advisory_xact_lock(hashtextextended('pastime.change.' || user_id::text, 0)) FROM (SELECT DISTINCT user_id FROM new_rows ORDER BY user_id) owners;
        INSERT INTO change_log(user_id,entity,entity_id,op) SELECT user_id,'play',id,'upsert' FROM new_rows;
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
