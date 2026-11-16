-- A household's primary user can give a non-default profile its own Pastime
-- sign-in. A profile login is a credential owned by one profile of an account,
-- not a new account. Browser sessions opened with it carry profile_login_id and
-- go away with the login. Login names share one namespace with account
-- usernames so the single sign-in form always knows which one is meant.
-- +goose Up
CREATE TABLE profile_logins (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id bigint NOT NULL,
    profile_id bigint NOT NULL UNIQUE,
    login_name text COLLATE "C" NOT NULL UNIQUE CHECK (login_name = lower(login_name) AND length(login_name) BETWEEN 3 AND 254),
    password_hash text NOT NULL CHECK (password_hash <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_login_at timestamptz,
    FOREIGN KEY (account_id, profile_id) REFERENCES tracking_profiles(account_id, id) ON DELETE CASCADE
);
CREATE INDEX profile_logins_account ON profile_logins(account_id);

ALTER TABLE sessions ADD COLUMN profile_login_id bigint REFERENCES profile_logins(id) ON DELETE CASCADE;
CREATE INDEX sessions_profile_login ON sessions(profile_login_id, created_at DESC) WHERE profile_login_id IS NOT NULL;

-- A device approved from a profile session yields a token bound to that profile.
ALTER TABLE device_authorizations ADD COLUMN profile_id bigint;
ALTER TABLE device_authorizations ADD CONSTRAINT device_authorizations_profile_owner
    FOREIGN KEY (user_id, profile_id) REFERENCES tracking_profiles(account_id, id) ON DELETE CASCADE;

-- +goose StatementBegin
CREATE FUNCTION login_name_unique() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE name text;
BEGIN
 -- One lock for both tables, so two transactions writing the same name to
 -- different tables can't both pass the check before either commits.
 PERFORM pg_advisory_xact_lock(7351942002);
 IF TG_TABLE_NAME = 'users' THEN
  name := NEW.username;
  PERFORM 1 FROM profile_logins WHERE login_name = name;
 ELSE
  name := NEW.login_name;
  PERFORM 1 FROM users WHERE username = name;
 END IF;
 IF FOUND THEN
  RAISE EXCEPTION 'login name already in use' USING ERRCODE = 'unique_violation', CONSTRAINT = 'login_names_unique';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER login_name_unique BEFORE INSERT OR UPDATE OF username ON users FOR EACH ROW EXECUTE FUNCTION login_name_unique();
CREATE TRIGGER login_name_unique BEFORE INSERT OR UPDATE OF login_name ON profile_logins FOR EACH ROW EXECUTE FUNCTION login_name_unique();

-- Archiving a profile turns its login off for good. Bloem can bring an archived
-- profile back, and the old login must not come back with it.
-- +goose StatementBegin
CREATE FUNCTION profile_login_archived() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM profile_logins WHERE profile_id = NEW.id;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER profile_login_archived AFTER UPDATE OF archived_at ON tracking_profiles FOR EACH ROW
    WHEN (OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL) EXECUTE FUNCTION profile_login_archived();

-- +goose Down
DROP TRIGGER profile_login_archived ON tracking_profiles;
DROP FUNCTION profile_login_archived();
DROP TRIGGER login_name_unique ON profile_logins;
DROP TRIGGER login_name_unique ON users;
DROP FUNCTION login_name_unique();
ALTER TABLE device_authorizations DROP COLUMN profile_id;
ALTER TABLE sessions DROP COLUMN profile_login_id;
DROP TABLE profile_logins;
