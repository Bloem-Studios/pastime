-- +goose Up
-- Videos (for example YouTube) are a media type of their own, tracked like movies.
ALTER TABLE catalog_items DROP CONSTRAINT catalog_items_kind_check;
ALTER TABLE catalog_items ADD CONSTRAINT catalog_items_kind_check CHECK (kind IN ('movie','series','season','episode','anime','manga','book','audiobook','comic','comic_issue','game','boardgame','music','track','podcast','podcast_episode','video'));
ALTER TABLE library_entries DROP CONSTRAINT library_entries_media_type_check;
ALTER TABLE library_entries ADD CONSTRAINT library_entries_media_type_check CHECK (media_type IN ('movie','series','anime','book','manga','comic','game','boardgame','podcast','music','audiobook','video'));

-- +goose Down
DELETE FROM library_entries WHERE media_type='video';
DELETE FROM catalog_items WHERE kind='video';
ALTER TABLE library_entries DROP CONSTRAINT library_entries_media_type_check;
ALTER TABLE library_entries ADD CONSTRAINT library_entries_media_type_check CHECK (media_type IN ('movie','series','anime','book','manga','comic','game','boardgame','podcast','music','audiobook'));
ALTER TABLE catalog_items DROP CONSTRAINT catalog_items_kind_check;
ALTER TABLE catalog_items ADD CONSTRAINT catalog_items_kind_check CHECK (kind IN ('movie','series','season','episode','anime','manga','book','audiobook','comic','comic_issue','game','boardgame','music','track','podcast','podcast_episode'));
