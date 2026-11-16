-- +goose Up
ALTER TABLE personal_lists ADD COLUMN share_token text, ADD COLUMN share_expires_at timestamptz;
UPDATE personal_lists SET share_token=gen_random_uuid()::text,share_expires_at=clock_timestamp()+interval '90 days' WHERE visibility='unlisted';
CREATE UNIQUE INDEX personal_lists_share_token ON personal_lists(share_token) WHERE share_token IS NOT NULL;
-- +goose StatementBegin
CREATE FUNCTION list_share_prepare() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.visibility<>'unlisted' THEN
  NEW.share_token:=NULL; NEW.share_expires_at:=NULL;
 ELSIF NEW.share_token IS NULL THEN
  NEW.share_token:=gen_random_uuid()::text;
  NEW.share_expires_at:=clock_timestamp()+interval '90 days';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER list_share_prepare BEFORE INSERT OR UPDATE OF visibility,share_token ON personal_lists FOR EACH ROW EXECUTE FUNCTION list_share_prepare();

-- +goose Down
DROP TRIGGER list_share_prepare ON personal_lists;
DROP FUNCTION list_share_prepare();
DROP INDEX personal_lists_share_token;
ALTER TABLE personal_lists DROP COLUMN share_token,DROP COLUMN share_expires_at;
