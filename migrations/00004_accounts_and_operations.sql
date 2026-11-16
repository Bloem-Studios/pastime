-- +goose Up
-- Accounts and operations: notifications, authenticator recovery, single sign-on,
-- import provenance and repair, scheduled exports and per-entry artwork overrides.

-- ---------------------------------------------------------------- notifications
-- Delivery targets hold webhook URLs and tokens, so their configuration is encrypted
-- with SECRET_KEY; only a redacted description is stored in clear.
CREATE TABLE notification_targets (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('webhook','ntfy','discord','gotify','apprise')),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 200),
    sealed_config bytea NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    last_status text NOT NULL DEFAULT '' CHECK (length(last_status) <= 200),
    last_sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (user_id, id)
);
CREATE INDEX notification_targets_owner ON notification_targets(user_id);
CREATE TABLE notification_settings (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    releases boolean NOT NULL DEFAULT true,
    days_ahead integer NOT NULL DEFAULT 0 CHECK (days_ahead BETWEEN 0 AND 14),
    media_types text[] NOT NULL DEFAULT '{}',
    statuses text[] NOT NULL DEFAULT '{planned,in_progress}',
    digest boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1
);
CREATE TABLE notification_exclusions (
    user_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    PRIMARY KEY (user_id, entry_id),
    FOREIGN KEY (user_id, entry_id) REFERENCES library_entries(user_id, id) ON DELETE CASCADE
);
-- One notice per person per release, however many entries or seasons point at it.
CREATE TABLE notification_log (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    sent_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, catalog_item_id)
);
CREATE INDEX notification_log_age ON notification_log(sent_at);

-- ---------------------------------------------------------------- authenticator recovery
-- An authenticator lets a person reset a forgotten password without an administrator.
-- It is not a second sign-in factor. Recovery codes are stored as SHA-256 digests.
CREATE TABLE user_totp (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    sealed_secret bytea NOT NULL,
    confirmed_at timestamptz,
    last_step bigint NOT NULL DEFAULT 0,
    recovery_digests bytea[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- ---------------------------------------------------------------- single sign-on
CREATE TABLE oidc_identities (
    issuer text NOT NULL CHECK (length(issuer) <= 500),
    subject text NOT NULL CHECK (length(subject) <= 255),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email text NOT NULL DEFAULT '' CHECK (length(email) <= 254),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_login_at timestamptz,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX oidc_identities_user ON oidc_identities(user_id);
CREATE TABLE oidc_states (
    state_digest bytea PRIMARY KEY,
    nonce text NOT NULL,
    verifier text NOT NULL,
    link_user_id bigint REFERENCES users(id) ON DELETE CASCADE,
    return_to text NOT NULL DEFAULT '/' CHECK (length(return_to) <= 500),
    expires_at timestamptz NOT NULL
);
CREATE INDEX oidc_states_expiry ON oidc_states(expires_at);

-- ---------------------------------------------------------------- import provenance and repair
ALTER TABLE library_entries ADD COLUMN import_run uuid REFERENCES import_runs(id) ON DELETE SET NULL;
ALTER TABLE plays ADD COLUMN import_run uuid REFERENCES import_runs(id) ON DELETE SET NULL;
CREATE INDEX library_entries_import ON library_entries(import_run) WHERE import_run IS NOT NULL;
CREATE INDEX plays_import ON plays(import_run) WHERE import_run IS NOT NULL;
-- The last time the run applied anything, including titles matched by hand afterwards.
ALTER TABLE import_runs ADD COLUMN last_applied_at timestamptz;
ALTER TABLE import_runs DROP CONSTRAINT import_runs_status_check;
ALTER TABLE import_runs ADD CONSTRAINT import_runs_status_check CHECK (status IN ('queued','running','succeeded','failed','cancelled','rolled_back'));
ALTER TABLE import_runs DROP CONSTRAINT import_runs_source_check;
ALTER TABLE import_runs ADD CONSTRAINT import_runs_source_check CHECK (source ~ '^[a-z][a-z0-9_]{1,31}$');
-- Every title an import could not match, kept whole so it can be matched by hand later.
CREATE TABLE import_unmatched (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES import_runs(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label text NOT NULL CHECK (length(label) <= 500),
    kind text NOT NULL DEFAULT '' CHECK (length(kind) <= 40),
    record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','resolved','ignored')),
    catalog_item_id bigint REFERENCES catalog_items(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    resolved_at timestamptz
);
CREATE INDEX import_unmatched_pending ON import_unmatched(user_id, id DESC) WHERE status = 'pending';
CREATE INDEX import_unmatched_run ON import_unmatched(run_id, id);

-- ---------------------------------------------------------------- scheduled exports
CREATE TABLE export_schedules (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    frequency text NOT NULL CHECK (frequency IN ('daily','weekly','monthly')),
    format text NOT NULL CHECK (format IN ('jsonl','csv','letterboxd')),
    keep integer NOT NULL DEFAULT 5 CHECK (keep BETWEEN 1 AND 20),
    next_run_at timestamptz NOT NULL,
    version integer NOT NULL DEFAULT 1
);
CREATE INDEX export_schedules_due ON export_schedules(next_run_at);
CREATE TABLE export_files (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    format text NOT NULL CHECK (format IN ('jsonl','csv','letterboxd')),
    trigger text NOT NULL CHECK (trigger IN ('schedule','manual')),
    entries integer NOT NULL DEFAULT 0,
    size_bytes bigint NOT NULL DEFAULT 0,
    -- gzip-compressed; exports are capped well below this.
    data bytea NOT NULL CHECK (octet_length(data) <= 268435456),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX export_files_owner ON export_files(user_id, created_at DESC);

-- ---------------------------------------------------------------- artwork override
ALTER TABLE library_entries ADD COLUMN image_override text NOT NULL DEFAULT '' CHECK (image_override = '' OR (length(image_override) <= 1000 AND image_override ~ '^https://'));
