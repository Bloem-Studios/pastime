-- +goose Up
-- Organization: tags, owned copies, richer lists, preferences, rating-only entries and
-- skipped episodes. Every personal row carries user_id and composite foreign keys, so
-- nothing can reference another account's records.

-- ---------------------------------------------------------------- rating-only entries
-- "rated" records an opinion without tracking progress: it never appears in progress views.
ALTER TABLE library_entries DROP CONSTRAINT library_entries_status_check;
ALTER TABLE library_entries ADD CONSTRAINT library_entries_status_check CHECK (status IN ('planned','in_progress','completed','paused','dropped','rated'));
-- Library sort orders. Keyset pagination compares these exact expressions.
CREATE INDEX library_entries_title ON library_entries(user_id, lower(title), id);
CREATE INDEX library_entries_rating ON library_entries(user_id, COALESCE(rating, -1) DESC, id DESC);
CREATE INDEX library_entries_year ON library_entries(user_id, COALESCE(publication_year, 0) DESC, id DESC);
CREATE INDEX library_entries_activity ON library_entries(user_id, COALESCE(last_activity_at, created_at) DESC, id DESC);

-- ---------------------------------------------------------------- tags
CREATE TABLE tags (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 50),
    color text NOT NULL DEFAULT '' CHECK (color = '' OR color ~ '^#[0-9a-f]{6}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (user_id, id)
);
CREATE UNIQUE INDEX tags_name ON tags(user_id, lower(name));
CREATE TABLE entry_tags (
    user_id bigint NOT NULL,
    tag_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    PRIMARY KEY (tag_id, entry_id),
    FOREIGN KEY (user_id, tag_id) REFERENCES tags(user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, entry_id) REFERENCES library_entries(user_id, id) ON DELETE CASCADE
);
CREATE INDEX entry_tags_entry ON entry_tags(user_id, entry_id);

-- ---------------------------------------------------------------- owned copies
CREATE TABLE collection_fields (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
    kind text NOT NULL CHECK (kind IN ('text','number','date','boolean','choice')),
    choices text[] NOT NULL DEFAULT '{}' CHECK (cardinality(choices) <= 50),
    position integer NOT NULL DEFAULT 0,
    UNIQUE (user_id, id)
);
CREATE UNIQUE INDEX collection_fields_name ON collection_fields(user_id, lower(name));
CREATE TABLE owned_copies (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    -- A copy may cover one season or volume of a title rather than all of it.
    catalog_item_id bigint REFERENCES catalog_items(id) ON DELETE SET NULL,
    format text NOT NULL CHECK (format IN ('digital','uhd_bluray','bluray','dvd','vhs','hardcover','paperback','ebook','audiobook','vinyl','cd','cassette','game_physical','game_digital','boxed','other')),
    edition text NOT NULL DEFAULT '' CHECK (length(edition) <= 200),
    resolution text NOT NULL DEFAULT '' CHECK (resolution IN ('','sd','720p','1080p','2160p','4320p')),
    hdr text NOT NULL DEFAULT '' CHECK (hdr IN ('','hdr10','hdr10plus','dolby_vision','hlg')),
    audio text NOT NULL DEFAULT '' CHECK (length(audio) <= 100),
    bitrate_kbps integer CHECK (bitrate_kbps BETWEEN 1 AND 1000000),
    acquired_on date,
    price_cents integer CHECK (price_cents BETWEEN 0 AND 100000000),
    currency text NOT NULL DEFAULT '' CHECK (currency = '' OR currency ~ '^[A-Z]{3}$'),
    store text NOT NULL DEFAULT '' CHECK (length(store) <= 200),
    location text NOT NULL DEFAULT '' CHECK (length(location) <= 200),
    notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 2000),
    custom jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(custom) = 'object'),
    source text NOT NULL DEFAULT 'manual' CHECK (source ~ '^[a-z][a-z0-9_]{1,31}$'),
    external_id text CHECK (length(external_id) <= 200),
    version integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (user_id, entry_id) REFERENCES library_entries(user_id, id) ON DELETE CASCADE
);
CREATE INDEX owned_copies_entry ON owned_copies(user_id, entry_id);
CREATE INDEX owned_copies_recent ON owned_copies(user_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX owned_copies_external ON owned_copies(user_id, source, external_id) WHERE external_id IS NOT NULL;

-- ---------------------------------------------------------------- lists
ALTER TABLE personal_lists
    ADD COLUMN visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','unlisted','public')),
    ADD COLUMN slug text CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,58}[a-z0-9])?$'),
    ADD COLUMN kind text NOT NULL DEFAULT 'manual' CHECK (kind IN ('manual','smart')),
    ADD COLUMN rules jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(rules) = 'object'),
    ADD COLUMN sort text NOT NULL DEFAULT 'position' CHECK (sort IN ('position','added','title','rating','year','activity')),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    -- Public and unlisted lists are addressed by slug.
    ADD CONSTRAINT personal_lists_shared_slug CHECK (visibility = 'private' OR slug IS NOT NULL);
CREATE UNIQUE INDEX personal_lists_slug ON personal_lists(user_id, slug) WHERE slug IS NOT NULL;
ALTER TABLE list_entries
    ADD COLUMN position double precision NOT NULL DEFAULT 0,
    ADD COLUMN note text NOT NULL DEFAULT '' CHECK (length(note) <= 500);
CREATE INDEX list_entries_order ON list_entries(list_id, position, entry_id);

-- ---------------------------------------------------------------- preferences
ALTER TABLE users ADD COLUMN public_profile boolean NOT NULL DEFAULT false;
CREATE TABLE user_preferences (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    prefs jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(prefs) = 'object' AND octet_length(prefs::text) <= 16384),
    version integer NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- ---------------------------------------------------------------- skipped episodes
-- An episode the person chose not to watch. It counts toward completion like a play but
-- records no consumption, so it never appears in history or statistics.
CREATE TABLE skipped_items (
    user_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    cycle integer NOT NULL CHECK (cycle >= 1),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, entry_id, cycle, catalog_item_id),
    FOREIGN KEY (user_id, entry_id) REFERENCES library_entries(user_id, id) ON DELETE CASCADE
);
