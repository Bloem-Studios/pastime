-- +goose Up
-- Complete schema. There is no earlier deployment to migrate from; later changes are new
-- numbered migrations. Rollback requires a reviewed recovery plan, so there is no Down.

-- Installed once per database in public; objects are schema-qualified so a narrow
-- search_path still resolves them.
CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA public;
CREATE EXTENSION IF NOT EXISTS btree_gist SCHEMA public;

-- ---------------------------------------------------------------- durable work
CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL CHECK (length(kind) BETWEEN 1 AND 100),
    dedupe_key text NOT NULL UNIQUE CHECK (length(dedupe_key) BETWEEN 1 AND 200),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    replay_policy text NOT NULL CHECK (replay_policy IN ('idempotent', 'reconcile')),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'blocked')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 20),
    run_after timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_token uuid,
    lease_until timestamptz,
    last_error_code text CHECK (length(last_error_code) <= 100),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (attempts <= max_attempts),
    CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (status = 'running' OR (lease_token IS NULL AND lease_until IS NULL))
);
CREATE INDEX jobs_runnable ON jobs (run_after, created_at, id) WHERE status = 'queued';
CREATE INDEX jobs_expired_lease ON jobs (lease_until, id) WHERE status = 'running';

-- ---------------------------------------------------------------- identity
CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username text COLLATE "C" NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9_.-]{2,47}$'),
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 80),
    email text NOT NULL DEFAULT '' CHECK (length(email) <= 254),
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'member' CHECK (role IN ('member','admin','owner')),
    active boolean NOT NULL DEFAULT true,
    auth_epoch bigint NOT NULL DEFAULT 1,
    version integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_login_at timestamptz
);
CREATE INDEX users_role_active ON users (role, active, id DESC);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    auth_epoch bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX sessions_user ON sessions(user_id, created_at DESC);
CREATE INDEX sessions_expiry ON sessions(expires_at);

CREATE TABLE api_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    scopes text[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 5 AND scopes <@ ARRAY['profile:read','library:read','library:write','lists:read','lists:write']::text[]),
    auth_epoch bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX api_tokens_user ON api_tokens(user_id, created_at DESC);

CREATE TABLE invitations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer_id bigint REFERENCES users(id) ON DELETE SET NULL,
    user_id bigint REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('join','reset')),
    role text NOT NULL CHECK (role IN ('member','admin','owner')),
    recipient_email text NOT NULL DEFAULT '' CHECK (length(recipient_email)<=254),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    revoked_at timestamptz,
    CHECK ((kind='reset') = (user_id IS NOT NULL))
);
CREATE INDEX invitations_recent ON invitations(created_at DESC,id);
CREATE INDEX invitations_issuer ON invitations(issuer_id);

CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id bigint REFERENCES users(id) ON DELETE SET NULL,
    actor_label text NOT NULL,
    action text NOT NULL,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX audit_events_recent ON audit_events(id DESC);
CREATE INDEX audit_events_target ON audit_events(target_kind,target_id,id DESC);

CREATE TABLE rate_buckets (
    bucket_key bytea PRIMARY KEY CHECK (octet_length(bucket_key)=32),
    attempts integer NOT NULL CHECK (attempts BETWEEN 1 AND 100001),
    expires_at timestamptz NOT NULL
);
CREATE INDEX rate_buckets_expiry ON rate_buckets(expires_at);

-- A calendar application cannot send headers, so the feed URL carries a random secret.
-- Only its hash is stored; rotating replaces it and revoking deletes it.
CREATE TABLE calendar_feeds (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- ---------------------------------------------------------------- catalog
-- Shared provider metadata; nothing private lives here. Seasons and episodes are children
-- of their show. root_id names the top-level title of every row (itself for a title), so
-- "everything under this show" is one indexed predicate instead of a tree walk.
CREATE TABLE catalog_items (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    root_id bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('movie','series','season','episode','anime','manga','book','audiobook','comic','comic_issue','game','boardgame','music','podcast','podcast_episode')),
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{1,31}$'),
    provider_id text COLLATE "C" NOT NULL CHECK (length(provider_id) BETWEEN 1 AND 200),
    parent_id bigint REFERENCES catalog_items(id) ON DELETE CASCADE,
    season_number integer CHECK (season_number BETWEEN 0 AND 100000),
    episode_number integer CHECK (episode_number BETWEEN 0 AND 1000000),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    original_title text NOT NULL DEFAULT '' CHECK (length(original_title) <= 500),
    overview text NOT NULL DEFAULT '' CHECK (length(overview) <= 20000),
    image_url text NOT NULL DEFAULT '' CHECK (length(image_url) <= 1000),
    backdrop_url text NOT NULL DEFAULT '' CHECK (length(backdrop_url) <= 1000),
    release_date date,
    runtime_minutes integer CHECK (runtime_minutes BETWEEN 0 AND 1000000),
    unit text NOT NULL CHECK (unit IN ('minutes','episodes','pages','chapters','tracks','items')),
    total integer CHECK (total BETWEEN 1 AND 100000000),
    release_status text NOT NULL DEFAULT '' CHECK (length(release_status) <= 60),
    genres text[] NOT NULL DEFAULT '{}' CHECK (cardinality(genres) <= 50),
    credits text[] NOT NULL DEFAULT '{}' CHECK (cardinality(credits) <= 50),
    external_ids jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(external_ids) = 'object'),
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    provider_rating numeric(4,2) CHECK (provider_rating BETWEEN 0 AND 10),
    children_loaded boolean NOT NULL DEFAULT false,
    refreshed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    search_document tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, title || ' ' || original_title)) STORED,
    UNIQUE (provider, kind, provider_id),
    CHECK ((kind IN ('season','episode','comic_issue','podcast_episode')) = (parent_id IS NOT NULL)),
    CHECK (kind <> 'season' OR season_number IS NOT NULL),
    CHECK (kind <> 'episode' OR (season_number IS NOT NULL AND episode_number IS NOT NULL))
);
CREATE INDEX catalog_items_children ON catalog_items (parent_id, season_number, episode_number, id) WHERE parent_id IS NOT NULL;
CREATE INDEX catalog_items_search ON catalog_items USING gin (search_document) WHERE parent_id IS NULL;
-- Fuzzy title search walks this index nearest-first, so it reads only the best matches
-- even when a common word matches most of a media type.
CREATE INDEX catalog_items_title_trgm ON catalog_items USING gist (kind, lower(title) public.gist_trgm_ops) WHERE parent_id IS NULL;
CREATE INDEX catalog_items_refresh ON catalog_items (refreshed_at, id) WHERE parent_id IS NULL;
CREATE INDEX catalog_items_imdb ON catalog_items ((external_ids->>'imdb'), kind) WHERE parent_id IS NULL AND external_ids ? 'imdb';
CREATE INDEX catalog_items_tvdb ON catalog_items ((external_ids->>'tvdb'), kind) WHERE parent_id IS NULL AND external_ids ? 'tvdb';
-- Dated releases across the whole catalog, for calendars: a date range scan joined to
-- what an account follows.
CREATE INDEX catalog_items_releases ON catalog_items (release_date) INCLUDE (root_id)
    WHERE release_date IS NOT NULL AND kind IN ('movie','episode','podcast_episode','comic_issue');
-- A show's regular episodes in watch order, for the next unwatched episode.
CREATE INDEX catalog_items_episode_order ON catalog_items (root_id, season_number, episode_number, id) WHERE kind = 'episode';

-- +goose StatementBegin
CREATE FUNCTION catalog_items_root() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.parent_id IS NULL THEN
        NEW.root_id := NEW.id;
    ELSE
        SELECT root_id INTO STRICT NEW.root_id FROM catalog_items WHERE id = NEW.parent_id;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER catalog_items_root BEFORE INSERT OR UPDATE OF parent_id ON catalog_items
    FOR EACH ROW EXECUTE FUNCTION catalog_items_root();

-- ---------------------------------------------------------------- library
-- One account's tracked titles. Catalog-backed entries copy title, type and total so
-- lists never join the catalog for text; manual entries keep private metadata here.
CREATE TABLE library_entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id bigint REFERENCES catalog_items(id) ON DELETE RESTRICT,
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    media_type text NOT NULL CHECK (media_type IN ('movie','series','anime','book','manga','comic','game','boardgame','podcast','music','audiobook')),
    publication_year integer CHECK (publication_year BETWEEN 1 AND 9999),
    unit text NOT NULL CHECK (unit IN ('minutes','episodes','pages','chapters','tracks','items')),
    total integer CHECK (total BETWEEN 1 AND 100000000),
    status text NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','in_progress','completed','paused','dropped')),
    progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100000000),
    rating integer CHECK (rating BETWEEN 0 AND 10),
    notes text NOT NULL DEFAULT '' CHECK (length(notes)<=10000),
    cycle integer NOT NULL DEFAULT 1 CHECK (cycle BETWEEN 1 AND 100000000),
    completed_cycles integer NOT NULL DEFAULT 0 CHECK (completed_cycles BETWEEN 0 AND cycle),
    -- The next unwatched regular episode in the current cycle, maintained on every play
    -- change and whenever the show's episode list grows.
    next_item_id bigint REFERENCES catalog_items(id) ON DELETE SET NULL,
    version integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    last_activity_at timestamptz,
    search_document tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig,title)) STORED,
    UNIQUE (user_id,id),
    CHECK (total IS NULL OR progress<=total),
    CHECK (status<>'completed' OR total IS NULL OR progress=total)
);
CREATE INDEX library_entries_owner ON library_entries(user_id,id DESC);
CREATE INDEX library_entries_status ON library_entries(user_id,status,id DESC);
CREATE INDEX library_entries_type ON library_entries(user_id,media_type,id DESC);
CREATE INDEX library_entries_search ON library_entries USING gin(search_document);
CREATE INDEX library_entries_recent ON library_entries(user_id,last_activity_at DESC NULLS LAST,id DESC);
CREATE INDEX library_entries_finished ON library_entries(user_id,finished_at DESC NULLS LAST,id DESC) WHERE status='completed';
CREATE UNIQUE INDEX library_entries_catalog ON library_entries(user_id,catalog_item_id) WHERE catalog_item_id IS NOT NULL;
CREATE INDEX library_entries_catalog_ref ON library_entries(catalog_item_id) WHERE catalog_item_id IS NOT NULL;
CREATE INDEX library_entries_up_next ON library_entries(user_id,last_activity_at DESC NULLS LAST,id DESC) WHERE next_item_id IS NOT NULL AND status IN ('in_progress','planned');

CREATE TABLE activity_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('added','updated','progress','restarted','played','unplayed')),
    unit text NOT NULL,
    status text NOT NULL,
    progress integer NOT NULL,
    cycle integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(user_id,entry_id) REFERENCES library_entries(user_id,id) ON DELETE CASCADE
);
CREATE INDEX activity_events_owner ON activity_events(user_id,id DESC);
CREATE INDEX activity_events_entry ON activity_events(user_id,entry_id,id DESC);

-- A play is a consumption fact. A repeat is a new row, never an incremented counter, so a
-- retried request cannot double count; external deliveries carry a source identity for
-- duplicate suppression. media_type and minutes are copied at insert so statistics and
-- history never join back to the catalog to classify or time a play.
CREATE TABLE plays (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE RESTRICT,
    cycle integer NOT NULL CHECK (cycle >= 1),
    played_at timestamptz NOT NULL,
    media_type text NOT NULL,
    minutes integer CHECK (minutes BETWEEN 0 AND 1000000),
    source text NOT NULL DEFAULT 'manual' CHECK (source ~ '^[a-z][a-z0-9_]{1,31}$'),
    external_id text CHECK (length(external_id) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (user_id, entry_id) REFERENCES library_entries(user_id, id) ON DELETE CASCADE
);
CREATE INDEX plays_entry ON plays (user_id, entry_id, cycle, catalog_item_id);
CREATE INDEX plays_history ON plays (user_id, played_at DESC, id DESC) INCLUDE (entry_id, media_type, minutes);
CREATE INDEX plays_history_type ON plays (user_id, media_type, played_at DESC, id DESC);
CREATE UNIQUE INDEX plays_external ON plays (user_id, source, external_id) WHERE external_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION plays_classify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    SELECT e.media_type INTO STRICT NEW.media_type FROM library_entries e WHERE e.user_id = NEW.user_id AND e.id = NEW.entry_id;
    -- An episode without its own runtime takes its show's typical episode runtime.
    SELECT COALESCE(c.runtime_minutes, r.runtime_minutes) INTO NEW.minutes
        FROM catalog_items c JOIN catalog_items r ON r.id = c.root_id WHERE c.id = NEW.catalog_item_id;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER plays_classify BEFORE INSERT ON plays FOR EACH ROW EXECUTE FUNCTION plays_classify();

-- Daily totals per account and media type, kept exact by statement-level triggers so a
-- ten-year statistics window reads thousands of rows instead of every play. Days are UTC.
CREATE TABLE play_days (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day date NOT NULL,
    media_type text NOT NULL,
    plays integer NOT NULL CHECK (plays >= 0),
    minutes bigint NOT NULL CHECK (minutes >= 0),
    PRIMARY KEY (user_id, day, media_type)
);

-- +goose StatementBegin
CREATE FUNCTION play_days_add() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO play_days AS d (user_id, day, media_type, plays, minutes)
    SELECT user_id, (played_at AT TIME ZONE 'UTC')::date, media_type, count(*), COALESCE(sum(minutes), 0)
    FROM added GROUP BY 1, 2, 3
    ON CONFLICT (user_id, day, media_type) DO UPDATE SET plays = d.plays + EXCLUDED.plays, minutes = d.minutes + EXCLUDED.minutes;
    RETURN NULL;
END $$;
CREATE FUNCTION play_days_remove() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE play_days d SET plays = d.plays - r.plays, minutes = d.minutes - r.minutes
    FROM (SELECT user_id, (played_at AT TIME ZONE 'UTC')::date AS day, media_type, count(*) AS plays, COALESCE(sum(minutes), 0) AS minutes
          FROM removed GROUP BY 1, 2, 3) r
    WHERE d.user_id = r.user_id AND d.day = r.day AND d.media_type = r.media_type;
    DELETE FROM play_days d USING (SELECT DISTINCT user_id FROM removed) u WHERE d.user_id = u.user_id AND d.plays = 0;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER play_days_add AFTER INSERT ON plays REFERENCING NEW TABLE AS added FOR EACH STATEMENT EXECUTE FUNCTION play_days_add();
CREATE TRIGGER play_days_remove AFTER DELETE ON plays REFERENCING OLD TABLE AS removed FOR EACH STATEMENT EXECUTE FUNCTION play_days_remove();

CREATE TABLE personal_lists (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    description text NOT NULL DEFAULT '' CHECK (length(description)<=2000),
    version integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(user_id,id)
);
CREATE INDEX personal_lists_owner ON personal_lists(user_id,id DESC);
CREATE TABLE list_entries (
    user_id bigint NOT NULL,
    list_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    added_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(list_id,entry_id),
    FOREIGN KEY(user_id,list_id) REFERENCES personal_lists(user_id,id) ON DELETE CASCADE,
    FOREIGN KEY(user_id,entry_id) REFERENCES library_entries(user_id,id) ON DELETE CASCADE
);
CREATE INDEX list_entries_entry ON list_entries(user_id,entry_id);

-- Idempotency receipts keep only an intent hash and acknowledgement, never private
-- content. No time-based deletion: deleting keys would let old retries run again.
CREATE TABLE mutation_receipts (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key uuid NOT NULL,
    intent_hash bytea NOT NULL CHECK (octet_length(intent_hash)=32),
    response jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(user_id,idempotency_key)
);

-- Resume position for a movie or episode: mutable state, separate from plays.
CREATE TABLE playback_progress (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    position_seconds integer NOT NULL CHECK (position_seconds BETWEEN 0 AND 10000000),
    duration_seconds integer CHECK (duration_seconds BETWEEN 1 AND 10000000),
    source text NOT NULL CHECK (source ~ '^[a-z][a-z0-9_]{1,31}$'),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, catalog_item_id)
);
CREATE INDEX playback_progress_recent ON playback_progress (user_id, updated_at DESC, catalog_item_id);

-- ---------------------------------------------------------------- connections and imports
-- One account's link to a media server. Webhooks authenticate with a hashed path secret;
-- settings hold only non-secret filters.
CREATE TABLE connections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('plex','jellyfin','emby')),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(settings)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_event_at timestamptz,
    last_outcome text NOT NULL DEFAULT '' CHECK (length(last_outcome) <= 200),
    events_recorded integer NOT NULL DEFAULT 0 CHECK (events_recorded >= 0),
    UNIQUE (user_id, kind)
);

-- A durable import. The upload is held only until the run finishes, and every write it
-- causes carries a source identity, so rerunning an import adds nothing twice.
CREATE TABLE import_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source text NOT NULL CHECK (source IN ('trakt','letterboxd','imdb','goodreads','storygraph','anilist','native')),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','cancelled')),
    cancel_requested boolean NOT NULL DEFAULT false,
    options jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(options)='object'),
    upload bytea CHECK (octet_length(upload) <= 67108864),
    upload_name text NOT NULL DEFAULT '' CHECK (length(upload_name) <= 200),
    total integer NOT NULL DEFAULT 0 CHECK (total >= 0),
    processed integer NOT NULL DEFAULT 0 CHECK (processed >= 0),
    imported integer NOT NULL DEFAULT 0 CHECK (imported >= 0),
    plays_added integer NOT NULL DEFAULT 0 CHECK (plays_added >= 0),
    unchanged integer NOT NULL DEFAULT 0 CHECK (unchanged >= 0),
    unmatched integer NOT NULL DEFAULT 0 CHECK (unmatched >= 0),
    failed integer NOT NULL DEFAULT 0 CHECK (failed >= 0),
    unmatched_sample jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(unmatched_sample)='array'),
    error text NOT NULL DEFAULT '' CHECK (length(error) <= 500),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz
);
CREATE INDEX import_runs_user ON import_runs (user_id, created_at DESC);
CREATE UNIQUE INDEX import_runs_one_active ON import_runs (user_id) WHERE status IN ('queued','running');
