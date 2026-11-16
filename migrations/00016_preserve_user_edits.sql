-- +goose Up
-- A user mutation adopts an imported entry. Later rollback cannot delete it,
-- even if the edit happened before the import run finished.
-- +goose StatementBegin
CREATE FUNCTION adopt_imported_entry() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('pastime.user_mutation',true)='true' THEN NEW.import_run=NULL; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION adopt_related_entry() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('pastime.user_mutation',true)='true' THEN
  IF TG_OP='DELETE' THEN
   UPDATE library_entries SET import_run=NULL WHERE user_id=OLD.user_id AND id=OLD.entry_id AND import_run IS NOT NULL;
  ELSE
   UPDATE library_entries SET import_run=NULL WHERE user_id=NEW.user_id AND id=NEW.entry_id AND import_run IS NOT NULL;
  END IF;
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER adopt_entry BEFORE UPDATE ON library_entries FOR EACH ROW EXECUTE FUNCTION adopt_imported_entry();
CREATE TRIGGER adopt_copy AFTER INSERT OR UPDATE OR DELETE ON owned_copies FOR EACH ROW EXECUTE FUNCTION adopt_related_entry();
CREATE TRIGGER adopt_tag AFTER INSERT OR DELETE ON entry_tags FOR EACH ROW EXECUTE FUNCTION adopt_related_entry();
CREATE TRIGGER adopt_list_entry AFTER INSERT OR DELETE ON list_entries FOR EACH ROW EXECUTE FUNCTION adopt_related_entry();
