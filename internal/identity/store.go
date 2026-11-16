package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool      *pgxpool.Pool
	Passwords *Passwords
	// Secrets encrypts authenticator secrets; nil disables authenticator recovery.
	Secrets *sealed.Box
	// ManagedClaim, when set, lets an unlinked provider identity claim its
	// integration-managed account during sign-in.
	ManagedClaim ManagedClaim
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool, Passwords: NewPasswords()} }

// UserColumns selects a User from an aliased users table \"u\" for ScanUser.
const UserColumns = userColumns
const userColumns = "u.id,u.username,u.display_name,u.email,u.role,u.active,(u.password_hash<>''),u.version,u.created_at,u.last_login_at"

func userFields(u *User) []any {
	return []any{&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &u.Active, &u.PasswordReady, &u.Version, &u.CreatedAt, &u.LastLoginAt}
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ScanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(userFields(&u)...)
	return u, err
}
func dbError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return fault.Conflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	return err
}

// authenticate resolves a credential. For a browser session, kind may be either
// session kind: the row decides which one it is, so a caller comparing kinds
// (AuthorizeTx) sees a profile session even when it asked for an account one.
func authenticate(ctx context.Context, q queryer, kind string, hash []byte, lock bool) (Actor, error) {
	if kind == KindFeed {
		return authenticateFeed(ctx, q, hash, lock)
	}
	a := Actor{Kind: kind, Hash: hash}
	table := "sessions"
	scopes := "ARRAY[]::text[]"
	used := "c.last_used_at"
	login := "l.id,l.profile_id,l.login_name,p.name"
	joins := ` LEFT JOIN profile_logins l ON l.id=c.profile_login_id LEFT JOIN tracking_profiles p ON p.id=l.profile_id`
	if kind == KindToken {
		table = "api_tokens"
		scopes = "c.scopes"
		used = "NULL::timestamptz"
		login = "NULL::bigint,NULL::bigint,NULL::text,NULL::text"
		joins = ""
	} else if kind != KindSession && kind != KindProfileSession {
		return a, fault.Unauthenticated
	}
	query := `SELECT ` + userColumns + `,c.id::text,` + scopes + `,` + used + `,` + login + ` FROM ` + table + ` c JOIN users u ON u.id=c.user_id` + joins + `
		WHERE c.token_hash=$1 AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp() AND u.active AND c.auth_epoch=u.auth_epoch AND pastime_account_has_service_access(u.id)`
	if lock {
		query += " FOR SHARE OF u"
	}
	var loginID, loginProfile *int64
	var loginName, profileName *string
	dest := append(userFields(&a.User), &a.CredentialID, &a.Scopes, &a.lastUsed, &loginID, &loginProfile, &loginName, &profileName)
	if err := q.QueryRow(ctx, query, hash).Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return a, fault.Unauthenticated
		}
		return a, fault.Unavailable
	}
	if lock {
		// Always lock the user before its credential; revocation uses the same order.
		// A profile session's profile comes between them: archiving locks the
		// profile and then deletes the login's sessions.
		if loginProfile != nil {
			var exists int
			err := q.QueryRow(ctx, `SELECT 1 FROM tracking_profiles WHERE id=$1 AND archived_at IS NULL FOR SHARE`, *loginProfile).Scan(&exists)
			if errors.Is(err, pgx.ErrNoRows) {
				return a, fault.Unauthenticated
			}
			if err != nil {
				return a, fault.Unavailable
			}
		}
		var exists int
		err := q.QueryRow(ctx, `SELECT 1 FROM `+table+` WHERE id=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp() FOR SHARE`, a.CredentialID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return a, fault.Unauthenticated
		}
		if err != nil {
			return a, fault.Unavailable
		}
	}
	if kind == KindToken {
		var bound int64
		if err := q.QueryRow(ctx, "SELECT profile_id FROM api_tokens WHERE id=$1", a.CredentialID).Scan(&bound); err != nil {
			return Actor{}, fault.Unauthenticated
		}
		return resolveProfile(ctx, q, a, bound, lock)
	}
	if loginID == nil {
		a.Kind = KindSession
		return resolveProfile(ctx, q, a, 0, lock)
	}
	// A profile session is bound to its login's profile; nothing the request
	// says can change that. An archived profile ends the session.
	a.Kind = KindProfileSession
	a.profileLoginID, a.loginName, a.profileName = *loginID, *loginName, *profileName
	bound, err := resolveProfile(ctx, q, a, *loginProfile, lock)
	if errors.Is(err, fault.NotFound) {
		return Actor{}, fault.Unauthenticated
	}
	return bound, err
}
func (s *Store) Authenticate(ctx context.Context, raw, kind string) (Actor, error) {
	prefix := "fn_s_"
	if kind == KindToken {
		prefix = "fn_k_"
	}
	if len(raw) != 48 || !strings.HasPrefix(raw, prefix) {
		return Actor{}, fault.Unauthenticated
	}
	a, err := authenticate(ctx, s.Pool, kind, Digest(raw), false)
	if err == nil && a.Browser() {
		a.CSRF = csrf(raw)
		s.markUsed(ctx, a)
	}
	return a, err
}

// sessionUseInterval bounds how often a session's last use is written: once per
// interval at most, so ordinary browsing costs no writes.
const sessionUseInterval = 5 * time.Minute

// markUsed records that a session was used, when its last use is older than
// sessionUseInterval. It is best effort: a failed write leaves the older time.
func (s *Store) markUsed(ctx context.Context, a Actor) {
	if a.lastUsed != nil && time.Since(*a.lastUsed) < sessionUseInterval {
		return
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE sessions SET last_used_at=clock_timestamp() WHERE id=$1::uuid
		AND (last_used_at IS NULL OR last_used_at<clock_timestamp()-make_interval(secs=>$2))`, a.CredentialID, sessionUseInterval.Seconds())
}

type clientKey struct{}

// WithClient carries the User-Agent of a sign-in request to the session it issues.
func WithClient(ctx context.Context, userAgent string) context.Context {
	return context.WithValue(ctx, clientKey{}, userAgent)
}

// clientOf returns the sign-in User-Agent as stored: printable, at most 512 characters.
func clientOf(ctx context.Context) string {
	v, _ := ctx.Value(clientKey{}).(string)
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, strings.ToValidUTF8(v, ""))
	if r := []rune(v); len(r) > 512 {
		v = string(r[:512])
	}
	return strings.TrimSpace(v)
}

// AuthorizeTx rechecks current authority and holds shared row locks until commit.
// Account/session revocation cannot commit ahead of a previously admitted mutation.
func AuthorizeTx(ctx context.Context, tx pgx.Tx, a Actor, scope string) (Actor, error) {
	current, err := authenticate(ctx, tx, a.Kind, a.Hash, true)
	if err != nil {
		return current, err
	}
	if current.Kind != a.Kind {
		return Actor{}, fault.Unauthenticated
	}
	if a.profileID != 0 && a.profileID != current.profileID {
		if !current.Household() {
			return Actor{}, fault.Forbidden
		}
		current, err = resolveProfile(ctx, tx, current, a.profileID, true)
		if err != nil {
			return Actor{}, err
		}
	}
	current.CSRF = a.CSRF
	if scope != "" && !current.Allows(scope) {
		return current, fault.Forbidden
	}
	return current, nil
}

// identityTx admits only the household's own session.
func (s *Store) identityTx(ctx context.Context, a Actor) (pgx.Tx, Actor, error) {
	return s.lockedTx(ctx, a, Actor.Household)
}

// lockedTx serializes identity changes and rechecks a's authority, which allow
// must accept.
func (s *Store) lockedTx(ctx context.Context, a Actor, allow func(Actor) bool) (pgx.Tx, Actor, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, a, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accountLock); err == nil {
		a, err = AuthorizeTx(ctx, tx, a, "")
	}
	if err == nil && !allow(a) {
		err = fault.Forbidden
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, a, err
	}
	return tx, a, nil
}
func Audit(ctx context.Context, tx pgx.Tx, a Actor, action, kind, id string, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var actorID any
	if a.User.ID > 0 {
		actorID = a.User.ID
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,actor_label,action,target_kind,target_id,metadata) VALUES($1,$2,$3,$4,$5,$6)`, actorID, a.User.Username, action, kind, id, data)
	return err
}

func (s *Store) Bootstrap(ctx context.Context, username, displayName, password string) (User, error) {
	username, err := ValidateUsername(username)
	if err != nil {
		return User{}, err
	}
	if displayName == "" {
		displayName = username
	}
	if len([]rune(displayName)) > 80 {
		return User{}, fault.Invalid("Display name is too long.")
	}
	hash, err := s.Passwords.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accountLock); err != nil {
		return User{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
		return User{}, err
	}
	if count != 0 {
		return User{}, fault.Conflict
	}
	u, err := ScanUser(tx.QueryRow(ctx, `INSERT INTO users AS u(username,display_name,password_hash,role) VALUES($1,$2,$3,'owner') RETURNING `+userColumns, username, displayName, hash))
	if err != nil {
		return u, dbError(err)
	}
	if err = Audit(ctx, tx, Actor{User: User{Username: "bootstrap"}}, "owner.bootstrapped", "user", strconv.FormatInt(u.ID, 10), nil); err != nil {
		return u, err
	}
	return u, tx.Commit(ctx)
}

// Login signs in with an account username or a profile login name. Both share
// one namespace (login_name_unique), so at most one row matches, and every
// path costs one password verification.
func (s *Store) Login(ctx context.Context, username, password string) (User, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var id, epoch int64
	var loginID *int64
	var hash string
	var active bool
	err := s.Pool.QueryRow(ctx, `SELECT id,NULL::bigint,password_hash,auth_epoch,active FROM users WHERE username=$1
		UNION ALL
		SELECT l.account_id,l.id,l.password_hash,u.auth_epoch,u.active AND p.archived_at IS NULL FROM profile_logins l
		JOIN users u ON u.id=l.account_id JOIN tracking_profiles p ON p.id=l.profile_id WHERE l.login_name=$1`, username).Scan(&id, &loginID, &hash, &epoch, &active)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", fault.Unavailable
	}
	valid, e := s.Passwords.Verify(ctx, password, hash)
	if e != nil {
		return User{}, "", e
	}
	if err != nil || hash == "" || !active || !valid {
		return User{}, "", fault.Unauthenticated
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accountLock); err != nil {
		return User{}, "", err
	}
	var u User
	var raw string
	if loginID != nil {
		u, raw, err = issueProfileSession(ctx, tx, id, *loginID, epoch, hash)
		if err != nil {
			return User{}, "", err
		}
		if err = tx.Commit(ctx); err != nil {
			return User{}, "", err
		}
		return u, raw, nil
	}
	var currentHash string
	var currentEpoch int64
	var currentActive bool
	if err = tx.QueryRow(ctx, "SELECT password_hash,auth_epoch,active FROM users WHERE id=$1 FOR UPDATE", id).Scan(&currentHash, &currentEpoch, &currentActive); err != nil {
		return User{}, "", fault.Unauthenticated
	}
	if !currentActive || currentHash != hash || currentEpoch != epoch {
		return User{}, "", fault.Unauthenticated
	}
	u, raw, err = issueSession(ctx, tx, id, epoch)
	if err != nil {
		return User{}, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, "", err
	}
	return u, raw, nil
}

func (s *Store) Logout(ctx context.Context, a Actor) error {
	tx, a, err := s.browserTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "UPDATE sessions SET revoked_at=clock_timestamp() WHERE id=$1", a.CredentialID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Limit(ctx context.Context, key string, max int, window time.Duration) error {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `INSERT INTO rate_buckets(bucket_key,attempts,expires_at) VALUES($1,1,clock_timestamp()+($3*interval '1 second'))
		ON CONFLICT(bucket_key) DO UPDATE SET
		attempts=CASE WHEN rate_buckets.expires_at<=clock_timestamp() THEN 1 ELSE LEAST(100001,rate_buckets.attempts+1) END,
		expires_at=CASE WHEN rate_buckets.expires_at<=clock_timestamp() THEN clock_timestamp()+($3*interval '1 second') ELSE rate_buckets.expires_at END
		RETURNING attempts<=$2`, Digest(key), max, int(window.Seconds())).Scan(&allowed)
	if err != nil {
		return fault.Unavailable
	}
	if !allowed {
		return fault.Limited
	}
	return nil
}

// LimitReached reports a Limit bucket that is already full, without counting an
// attempt. A caller that charges the bucket only on failure checks it first, so a
// client past the limit costs one read.
func (s *Store) LimitReached(ctx context.Context, key string, max int) error {
	var full bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rate_buckets WHERE bucket_key=$1 AND expires_at>clock_timestamp() AND attempts>=$2)`, Digest(key), max).Scan(&full)
	if err != nil {
		return fault.Unavailable
	}
	if full {
		return fault.Limited
	}
	return nil
}
func (s *Store) Sweep(ctx context.Context) error {
	// Small batches bound lock time. Audit and mutation receipts have separate retention.
	for _, table := range []string{"sessions", "api_tokens"} {
		if _, err := s.Pool.Exec(ctx, "DELETE FROM "+table+" WHERE id IN (SELECT id FROM "+table+" WHERE expires_at<clock_timestamp()-interval '1 day' OR revoked_at<clock_timestamp()-interval '30 days' ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)"); err != nil {
			return err
		}
	}

	_, err := s.Pool.Exec(ctx, `DELETE FROM rate_buckets WHERE bucket_key IN (SELECT bucket_key FROM rate_buckets WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)`)
	return err
}

// issueSession creates a browser session, keeps at most ten per account and records the
// sign-in. Profile sessions have their own limit (issueProfileSession) and are
// neither counted nor revoked here. The caller holds the account row lock.
func issueSession(ctx context.Context, tx pgx.Tx, id, epoch int64) (User, string, error) {
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT pastime_account_has_service_access($1)`, id).Scan(&allowed); err != nil {
		return User{}, "", err
	}
	if !allowed {
		return User{}, "", ErrServiceAccessRequired
	}
	raw := randomToken("fn_s_")
	if _, err := tx.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,auth_epoch,expires_at,user_agent,last_used_at) VALUES($1,$2,$3,clock_timestamp()+interval '7 days',$4,clock_timestamp())`, id, Digest(raw), epoch, clientOf(ctx)); err != nil {
		return User{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE id IN
		(SELECT id FROM sessions WHERE user_id=$1 AND profile_login_id IS NULL AND revoked_at IS NULL ORDER BY created_at DESC,id OFFSET 10)`, id); err != nil {
		return User{}, "", err
	}
	u, err := ScanUser(tx.QueryRow(ctx, `UPDATE users AS u SET last_login_at=clock_timestamp() WHERE id=$1 RETURNING `+userColumns, id))
	return u, raw, err
}
