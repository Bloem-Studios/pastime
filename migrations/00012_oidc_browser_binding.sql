-- +goose Up
ALTER TABLE oidc_states ADD COLUMN link_session_digest bytea;
-- Pending links created before session binding must be restarted.
DELETE FROM oidc_states WHERE link_user_id IS NOT NULL;
