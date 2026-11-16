-- +goose Up
-- Device sign-in (RFC 8628 style): a TV or phone app starts a request, a signed-in
-- person approves its short code in the browser, and the app's polling receives an
-- ordinary scoped API token. Both codes are stored only as digests. An approval is
-- bound to the account's authentication epoch, so a password change or "sign out
-- everywhere" between approval and pickup voids it.
CREATE TABLE device_authorizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code_hash bytea NOT NULL UNIQUE CHECK (octet_length(device_code_hash)=32),
    user_code_hash bytea NOT NULL UNIQUE CHECK (octet_length(user_code_hash)=32),
    client_name text NOT NULL CHECK (length(client_name) BETWEEN 1 AND 60),
    scopes text[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 5 AND scopes <@ ARRAY['profile:read','library:read','library:write','lists:read','lists:write']::text[]),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied','issued')),
    user_id bigint REFERENCES users(id) ON DELETE CASCADE,
    approved_epoch bigint,
    poll_interval integer NOT NULL DEFAULT 5 CHECK (poll_interval BETWEEN 5 AND 60),
    last_poll_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    CHECK ((status IN ('approved','issued')) = (user_id IS NOT NULL AND approved_epoch IS NOT NULL) OR status = 'denied')
);
CREATE INDEX device_authorizations_expiry ON device_authorizations(expires_at);

-- +goose Down
DROP TABLE device_authorizations;
