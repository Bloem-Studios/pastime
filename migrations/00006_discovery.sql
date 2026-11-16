-- +goose Up
-- Discovery: dismissals keyed by provider record, and indexes for personal suggestions.

-- Discover dismissals name a provider record, so a suggestion can be hidden before it
-- has ever been added to the shared catalog.
DROP TABLE discover_dismissals;
CREATE TABLE discover_dismissals (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{1,31}$'),
    kind text NOT NULL CHECK (length(kind) BETWEEN 1 AND 32),
    provider_id text COLLATE "C" NOT NULL CHECK (length(provider_id) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, provider, kind, provider_id)
);
-- Titles a person rated or finished, best first, seed personal suggestions.
CREATE INDEX library_entries_taste ON library_entries(user_id, media_type, rating DESC NULLS LAST, last_activity_at DESC) WHERE catalog_item_id IS NOT NULL AND status IN ('completed','in_progress','rated');
-- Suggestions by genre read the best-rated titles sharing a genre.
CREATE INDEX catalog_items_genres ON catalog_items USING gin (genres) WHERE parent_id IS NULL;
