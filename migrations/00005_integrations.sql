-- +goose Up
-- Integrations and catalog breadth: linked services that sync on a schedule, more
-- webhook kinds, a KOReader sync server, music tracks, Discover feedback,
-- recommendations between people and lists that follow an outside source.

-- ---------------------------------------------------------------- catalog
ALTER TABLE catalog_items DROP CONSTRAINT catalog_items_kind_check;
ALTER TABLE catalog_items ADD CONSTRAINT catalog_items_kind_check CHECK (kind IN ('movie','series','season','episode','anime','manga','book','audiobook','comic','comic_issue','game','boardgame','music','track','podcast','podcast_episode'));
-- Lookups by another service's identifier (MyAnimeList, Steam, MusicBrainz release...).
CREATE INDEX catalog_items_external ON catalog_items USING gin (external_ids jsonb_path_ops) WHERE parent_id IS NULL;

-- ---------------------------------------------------------------- linked services
-- Credentials for pull integrations. A linked service runs an ordinary import on its
-- schedule, so syncing inherits imports' merge-forward and idempotency rules.
CREATE TABLE service_links (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_]{1,31}$'),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 200),
    sealed_config bytea NOT NULL,
    -- Non-secret sync position, such as the newest scrobble already imported.
    state jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(state) = 'object'),
    schedule text NOT NULL DEFAULT 'daily' CHECK (schedule IN ('manual','hourly','daily','weekly')),
    next_run_at timestamptz,
    last_run uuid REFERENCES import_runs(id) ON DELETE SET NULL,
    last_run_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (user_id, id)
);
CREATE INDEX service_links_due ON service_links(next_run_at) WHERE schedule <> 'manual';
CREATE UNIQUE INDEX service_links_kind ON service_links(user_id, kind);
-- Device and PIN sign-in flows for Trakt and Simkl, held for a few minutes.
CREATE TABLE service_authorizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL,
    sealed_state bytea NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX service_authorizations_expiry ON service_authorizations(expires_at);

-- ---------------------------------------------------------------- webhooks
ALTER TABLE connections DROP CONSTRAINT connections_kind_check;
ALTER TABLE connections ADD CONSTRAINT connections_kind_check CHECK (kind IN ('plex','jellyfin','emby','generic','seerr'));

-- ---------------------------------------------------------------- KOReader sync
-- KOReader's sync protocol authenticates with a username and an MD5 key; the key's
-- SHA-256 digest is stored. Progress is keyed by the document's fingerprint and can be
-- linked to a book in the library.
CREATE TABLE kosync_accounts (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    key_digest bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE kosync_documents (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    document text NOT NULL CHECK (length(document) BETWEEN 1 AND 128),
    progress text NOT NULL DEFAULT '' CHECK (length(progress) <= 500),
    percentage double precision NOT NULL DEFAULT 0 CHECK (percentage BETWEEN 0 AND 1),
    device text NOT NULL DEFAULT '' CHECK (length(device) <= 100),
    device_id text NOT NULL DEFAULT '' CHECK (length(device_id) <= 100),
    entry_id bigint,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, document),
    FOREIGN KEY (user_id, entry_id) REFERENCES library_entries(user_id, id) ON DELETE SET NULL (entry_id)
);

-- ---------------------------------------------------------------- Discover and recommendations
CREATE TABLE discover_dismissals (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, catalog_item_id)
);
CREATE TABLE recommendations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_user bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    note text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','dismissed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (from_user <> to_user)
);
CREATE UNIQUE INDEX recommendations_once ON recommendations(from_user, to_user, catalog_item_id);
CREATE INDEX recommendations_inbox ON recommendations(to_user, status, created_at DESC);

-- ---------------------------------------------------------------- lists that follow a source
ALTER TABLE personal_lists
    ADD COLUMN source_kind text CHECK (source_kind IN ('trakt','mdblist','letterboxd')),
    ADD COLUMN source_ref text CHECK (length(source_ref) <= 500),
    ADD COLUMN synced_at timestamptz,
    ADD CONSTRAINT personal_lists_source CHECK ((source_kind IS NULL) = (source_ref IS NULL));
CREATE INDEX personal_lists_sourced ON personal_lists(synced_at) WHERE source_kind IS NOT NULL;
