-- +goose Up
-- Tracking data belongs to profiles. Existing public URLs use account username
-- plus list slug, so their human-readable link namespace remains account-wide.
CREATE UNIQUE INDEX personal_lists_account_slug ON personal_lists(user_id,slug) WHERE slug IS NOT NULL;
-- +goose Down
DROP INDEX personal_lists_account_slug;
