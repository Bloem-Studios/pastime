-- +goose Up
-- Media depth: the edition a person owns or reads, music tracks and podcast feeds.

-- The chosen edition of a book or audiobook: its page count and cover replace the
-- work's for this person only.
ALTER TABLE library_entries ADD COLUMN edition jsonb CHECK (edition IS NULL OR jsonb_typeof(edition) = 'object');

-- Album tracks are children of the album, like episodes of a show.
ALTER TABLE catalog_items DROP CONSTRAINT catalog_items_check;
ALTER TABLE catalog_items ADD CONSTRAINT catalog_items_parent_kind CHECK ((kind IN ('season','episode','comic_issue','podcast_episode','track')) = (parent_id IS NOT NULL));
