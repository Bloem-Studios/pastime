-- +goose Up
CREATE TABLE tracking_profiles (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 80),
 is_default boolean NOT NULL DEFAULT false,
 public_profile boolean NOT NULL DEFAULT false,
 archived_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,id),
 CHECK(NOT is_default OR archived_at IS NULL)
);
CREATE UNIQUE INDEX tracking_profiles_default ON tracking_profiles(account_id) WHERE is_default;
INSERT INTO tracking_profiles(account_id,name,is_default,public_profile) SELECT id,left(display_name,80),true,public_profile FROM users ORDER BY id;

-- +goose StatementBegin
CREATE FUNCTION create_default_tracking_profile() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO tracking_profiles(account_id,name,is_default) VALUES(NEW.id,left(NEW.display_name,80),true);
 RETURN NEW;
END $$;
CREATE FUNCTION sync_default_profile_privacy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE tracking_profiles SET public_profile=NEW.public_profile WHERE account_id=NEW.id AND is_default;
 RETURN NEW;
END $$;
CREATE TRIGGER sync_default_profile_privacy AFTER UPDATE OF public_profile ON users FOR EACH ROW EXECUTE FUNCTION sync_default_profile_privacy();
CREATE TRIGGER create_default_tracking_profile AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION create_default_tracking_profile();

-- Both IDs are retained. Old inserts specifying only an account target its default
-- profile. Native inserts specify profile_id; its owning account is stamped here.
CREATE FUNCTION tracking_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner bigint;
BEGIN
 IF NEW.profile_id IS NULL THEN
  SELECT id INTO STRICT NEW.profile_id FROM tracking_profiles WHERE account_id=NEW.user_id AND is_default;
 END IF;
 SELECT account_id INTO STRICT owner FROM tracking_profiles WHERE id=NEW.profile_id;
 IF NEW.user_id IS NULL THEN NEW.user_id:=owner;
 ELSIF NEW.user_id<>owner THEN RAISE EXCEPTION 'tracking owner mismatch' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END $$;

DO $$
DECLARE tbl text;
 tables text[]:=ARRAY['notification_targets','calendar_feeds','library_entries','activity_events','plays','play_days','personal_lists','list_entries','mutation_receipts','playback_progress','connections','import_runs','tags','entry_tags','collection_fields','owned_copies','user_preferences','skipped_items','notification_settings','notification_exclusions','notification_log','import_unmatched','export_schedules','export_files','service_links','service_authorizations','kosync_accounts','kosync_documents','discover_dismissals','change_log','import_batches','notification_deliveries','native_restore_receipts','saved_views','entry_status_history','episode_ratings','library_tombstones','list_tiers'];
 rec record;
 state record;
BEGIN
 FOREACH tbl IN ARRAY tables LOOP
  EXECUTE format('ALTER TABLE %I ADD COLUMN profile_id bigint',tbl);
 END LOOP;
 CREATE TEMP TABLE tracking_trigger_modes(relation_name text,tgname text,tgenabled "char") ON COMMIT DROP;
 FOREACH tbl IN ARRAY tables LOOP
  INSERT INTO tracking_trigger_modes SELECT tbl,tgname,tgenabled FROM pg_trigger WHERE tgrelid=tbl::regclass AND NOT tgisinternal;
  FOR state IN SELECT tgname,tgenabled FROM pg_trigger WHERE tgrelid=tbl::regclass AND NOT tgisinternal AND tgenabled<>'D' LOOP
   EXECUTE format('ALTER TABLE %I DISABLE TRIGGER %I',tbl,state.tgname);
  END LOOP;
  EXECUTE format('UPDATE %I t SET profile_id=p.id FROM tracking_profiles p WHERE p.account_id=t.user_id AND p.is_default',tbl);
  FOR state IN SELECT m.tgname,m.tgenabled FROM tracking_trigger_modes m WHERE m.relation_name=tbl LOOP
   IF state.tgenabled='O' THEN EXECUTE format('ALTER TABLE %I ENABLE TRIGGER %I',tbl,state.tgname);
   ELSIF state.tgenabled='A' THEN EXECUTE format('ALTER TABLE %I ENABLE ALWAYS TRIGGER %I',tbl,state.tgname);
   ELSIF state.tgenabled='R' THEN EXECUTE format('ALTER TABLE %I ENABLE REPLICA TRIGGER %I',tbl,state.tgname);
   END IF;
  END LOOP;
  EXECUTE format('ALTER TABLE %I ALTER COLUMN profile_id SET NOT NULL',tbl);
 END LOOP;
 -- Capture definitions before removing dependent FKs and owner uniqueness.
 CREATE TEMP TABLE profile_constraints ON COMMIT DROP AS
 SELECT c.conrelid::regclass::text AS tbl,c.conname,c.contype,pg_get_constraintdef(c.oid) AS def
 FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=current_schema() AND r.relname=ANY(tables) AND c.contype IN ('p','u','f')
 AND pg_get_constraintdef(c.oid) ~ '\muser_id\M' AND NOT(c.contype='f' AND c.confrelid='users'::regclass);
 CREATE TEMP TABLE profile_indexes(indexname text,indexdef text) ON COMMIT DROP;
 FOREACH tbl IN ARRAY tables LOOP
  FOR rec IN SELECT i.indexrelid,c.relname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
   WHERE i.indrelid=tbl::regclass AND NOT EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conindid=i.indexrelid) LOOP
   INSERT INTO profile_indexes SELECT rec.relname,pg_get_indexdef(rec.indexrelid)
    WHERE pg_get_indexdef(rec.indexrelid) ~ '\muser_id\M';
  END LOOP;
 END LOOP;
 FOR rec IN SELECT * FROM profile_constraints ORDER BY CASE WHEN contype='f' THEN 0 ELSE 1 END LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',rec.tbl,rec.conname);
 END LOOP;
 FOR rec IN SELECT * FROM profile_constraints ORDER BY CASE WHEN contype='f' THEN 1 ELSE 0 END LOOP
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',rec.tbl,rec.conname,replace(rec.def,'user_id','profile_id'));
 END LOOP;
 FOR rec IN SELECT * FROM profile_indexes LOOP
  EXECUTE format('DROP INDEX %I',rec.indexname);
  EXECUTE replace(rec.indexdef,'user_id','profile_id');
 END LOOP;
 FOREACH tbl IN ARRAY tables LOOP
  EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY(user_id,profile_id) REFERENCES tracking_profiles(account_id,id) ON DELETE CASCADE',tbl,tbl||'_tracking_owner');
  EXECUTE format('CREATE TRIGGER a_tracking_owner BEFORE INSERT OR UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION tracking_owner()',tbl);
 END LOOP;
 -- Statement-level rollups and history must follow profile ownership too.
 FOR rec IN SELECT p.oid,pg_get_functiondef(p.oid) AS def FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND p.proname IN ('play_days_add','play_days_remove','change_log_entries','change_log_plays','record_status_change','preserve_library_deletion') LOOP
  EXECUTE replace(replace(rec.def,'user_id','profile_id'),'FROM users u WHERE u.id=o.profile_id','FROM tracking_profiles u WHERE u.id=o.profile_id');
 END LOOP;
END $$;
-- +goose StatementEnd

-- Durable jobs queued before this migration used account IDs. Preserve JSON
-- number/string representation and bind them explicitly to the migrated default.
UPDATE jobs j SET payload=(j.payload-'user_id')||jsonb_build_object('profile_id',
 CASE WHEN jsonb_typeof(j.payload->'user_id')='string' THEN to_jsonb(p.id::text) ELSE to_jsonb(p.id) END)
FROM tracking_profiles p WHERE p.is_default AND p.account_id::text=j.payload->>'user_id'
 AND j.kind IN ('statistics_warm','scheduled_export','scrobble','playback_push','listens','list_sync') AND j.payload ? 'user_id';
UPDATE maintenance_cursors SET last_id=0 WHERE name='statistics_warm';

ALTER TABLE api_tokens ADD COLUMN profile_id bigint;
UPDATE api_tokens t SET profile_id=p.id FROM tracking_profiles p WHERE p.account_id=t.user_id AND p.is_default;
ALTER TABLE api_tokens ALTER COLUMN profile_id SET NOT NULL;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_profile_owner FOREIGN KEY(user_id,profile_id) REFERENCES tracking_profiles(account_id,id) ON DELETE CASCADE;
CREATE TRIGGER a_tracking_owner BEFORE INSERT OR UPDATE ON api_tokens FOR EACH ROW EXECUTE FUNCTION tracking_owner();

-- +goose Down
-- Profile data cannot be collapsed into accounts safely. Restore a protected
-- pre-migration backup with writes paused instead of destructive rollback.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'restore the protected pre-profile backup to roll back'; END $$;
-- +goose StatementEnd
