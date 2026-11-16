-- +goose Up
-- Optional paid deployment policy. Other installations remain unchanged.
CREATE TABLE service_access_policy (
 id smallint PRIMARY KEY CHECK(id=1),
 issuer text NOT NULL CHECK(issuer LIKE 'https://%' AND length(issuer)<=2048),
 enabled boolean NOT NULL DEFAULT false,
 generated_at timestamptz NOT NULL,
 fresh_until timestamptz NOT NULL CHECK(fresh_until>generated_at AND fresh_until<=generated_at+interval '120 seconds')
);
CREATE TABLE service_access_grants (
 subject text PRIMARY KEY CHECK(subject ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 expires_at timestamptz NOT NULL
);
-- +goose StatementBegin
CREATE FUNCTION pastime_account_has_service_access(account bigint) RETURNS boolean LANGUAGE sql VOLATILE AS $$
 SELECT COALESCE((SELECT u.role IN ('owner','admin') OR NOT EXISTS(SELECT 1 FROM service_access_policy WHERE enabled)
 OR EXISTS(SELECT 1 FROM service_access_policy p JOIN service_access_grants g ON g.expires_at>clock_timestamp()
 WHERE p.id=1 AND p.enabled AND p.fresh_until>clock_timestamp() AND (
 EXISTS(SELECT 1 FROM oidc_identities o WHERE o.user_id=u.id AND o.issuer=p.issuer AND o.subject=g.subject)
 OR EXISTS(SELECT 1 FROM bloem_accounts a JOIN bloem_instances i ON i.id=a.instance_id AND i.active
 JOIN bloem_login_issuers l ON l.instance_id=i.id AND l.login_issuer=p.issuer AND l.subject_mode='user_uuid'
 WHERE a.account_id=u.id AND a.active AND a.identity_issuer=i.identity_issuer AND a.identity_subject=g.subject)
 )) FROM users u WHERE u.id=account),false)
$$;
-- Backend-only publisher: no HTTP endpoint or application-role permission.
CREATE FUNCTION pastime_apply_service_access(provider text, observed timestamptz, valid_until timestamptz, grants jsonb) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE old_issuer text; old_observed timestamptz; previous bigint[]; expired_previous bigint[]; removed integer;
BEGIN
 IF provider NOT LIKE 'https://%' OR length(provider)>2048 OR observed>clock_timestamp()+interval '5 seconds'
 OR observed<clock_timestamp()-interval '120 seconds' OR valid_until<=clock_timestamp() OR valid_until<=observed OR valid_until>observed+interval '120 seconds'
 OR grants IS NULL OR jsonb_typeof(grants)<>'array' OR jsonb_array_length(grants)>10000 OR octet_length(grants::text)>1048576 THEN
  RAISE EXCEPTION 'invalid service access snapshot';
 END IF;
 PERFORM pg_advisory_xact_lock(791442510048);
 PERFORM pg_advisory_xact_lock(7351942001);
 SELECT issuer,generated_at INTO old_issuer,old_observed FROM service_access_policy WHERE id=1 FOR UPDATE;
 IF FOUND AND (old_issuer<>provider OR old_observed>=observed) THEN RAISE EXCEPTION 'stale or foreign service access snapshot'; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_to_recordset(grants) AS g(subject text,expires_at timestamptz)
 WHERE subject IS NULL OR subject !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR expires_at IS NULL OR expires_at<=observed OR expires_at>valid_until) THEN RAISE EXCEPTION 'invalid service access grant'; END IF;
 -- Lock accounts before replacing the grants, matching normal authentication.
 PERFORM 1 FROM users WHERE role='member' ORDER BY id FOR UPDATE;
 SELECT array_agg(id) INTO previous FROM users WHERE role='member' AND (
 pastime_account_has_service_access(id)
 OR EXISTS(SELECT 1 FROM service_access_grants g JOIN oidc_identities o ON o.subject=g.subject WHERE o.user_id=users.id AND o.issuer=provider)
 OR EXISTS(SELECT 1 FROM service_access_grants g JOIN bloem_accounts a ON a.identity_subject=g.subject WHERE a.account_id=users.id)
 );
 SELECT array_agg(id) INTO expired_previous FROM users WHERE id=ANY(previous) AND NOT pastime_account_has_service_access(id);
 DELETE FROM service_access_grants;
 INSERT INTO service_access_grants SELECT subject,expires_at FROM jsonb_to_recordset(grants) AS g(subject text,expires_at timestamptz);
 INSERT INTO service_access_policy(id,issuer,enabled,generated_at,fresh_until) VALUES(1,provider,true,observed,valid_until)
 ON CONFLICT(id) DO UPDATE SET enabled=true,generated_at=EXCLUDED.generated_at,fresh_until=EXCLUDED.fresh_until;
 UPDATE users SET auth_epoch=auth_epoch+1,version=version+1 WHERE id=ANY(previous) AND (NOT pastime_account_has_service_access(id) OR id=ANY(expired_previous));
 GET DIAGNOSTICS removed=ROW_COUNT;
 DELETE FROM calendar_feeds WHERE profile_id IN (SELECT id FROM tracking_profiles WHERE account_id=ANY(previous) AND (NOT pastime_account_has_service_access(account_id) OR account_id=ANY(expired_previous)));
 RETURN removed;
END $$;
REVOKE ALL ON FUNCTION pastime_apply_service_access(text,timestamptz,timestamptz,jsonb) FROM PUBLIC;
-- +goose StatementEnd
-- +goose Down
DROP FUNCTION pastime_apply_service_access(text,timestamptz,timestamptz,jsonb);
DROP FUNCTION pastime_account_has_service_access(bigint);
DROP TABLE service_access_grants,service_access_policy;
