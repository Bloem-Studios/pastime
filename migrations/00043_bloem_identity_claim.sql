-- +goose Up
-- Sign-in issuers an operator declared equivalent to an instance's identity issuer.
-- The subject mode states how that provider forms subjects, so a subject of the
-- wrong shape never claims or links a managed account.
CREATE TABLE bloem_login_issuers (
 instance_id text NOT NULL REFERENCES bloem_instances(id),
 login_issuer text NOT NULL CHECK(length(login_issuer) BETWEEN 9 AND 500),
 subject_mode text NOT NULL CHECK(subject_mode IN ('user_uuid','hashed_user_id')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(instance_id,login_issuer)
);
ALTER TABLE bloem_accounts ADD COLUMN claimed_at timestamptz;
-- The identity under which the account was handed to a person, by a sign-in claim or
-- by linking a new account to an existing member. Provisioning refuses any other
-- identity for it, even after a clear, until an operator revokes the claim.
ALTER TABLE bloem_accounts
 ADD COLUMN bound_issuer text NOT NULL DEFAULT '',
 ADD COLUMN bound_subject text NOT NULL DEFAULT '',
 ADD CONSTRAINT bloem_accounts_bound_identity CHECK((bound_issuer='')=(bound_subject=''));
UPDATE bloem_accounts SET bound_issuer=identity_issuer,bound_subject=identity_subject WHERE link_status='linked' AND identity_subject<>'';
-- One managed account per verified identity and instance.
CREATE UNIQUE INDEX bloem_accounts_identity ON bloem_accounts(instance_id,identity_issuer,identity_subject) WHERE identity_subject<>'';
-- Sign-in claims look accounts up by subject across instances.
CREATE INDEX bloem_accounts_identity_subject ON bloem_accounts(identity_subject) WHERE identity_subject<>'';

-- +goose Down
DROP INDEX bloem_accounts_identity_subject;
DROP INDEX bloem_accounts_identity;
ALTER TABLE bloem_accounts DROP CONSTRAINT bloem_accounts_bound_identity, DROP COLUMN bound_subject, DROP COLUMN bound_issuer, DROP COLUMN claimed_at;
DROP TABLE bloem_login_issuers;
