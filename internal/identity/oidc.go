package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// OIDCState is one pending sign-in: the values bound to the provider round trip.
type OIDCState struct {
	State, Nonce, Verifier, ReturnTo string
	LinkUserID                       *int64
	LinkSessionHash                  []byte
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeReturn keeps redirects inside the application.
func safeReturn(v string) string {
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, "/\\") || len(v) > 500 || strings.HasPrefix(v, "/login") || strings.HasPrefix(v, "/auth/") {
		return "/"
	}
	return v
}

// BeginOIDC stores a single-use state for ten minutes. linkUserID is set when a signed-in
// person is attaching the provider account to their own.
func (s *Store) BeginOIDC(ctx context.Context, returnTo string, link *Actor) (OIDCState, error) {
	st := OIDCState{State: randomString(32), Nonce: randomString(24), Verifier: randomString(48), ReturnTo: safeReturn(returnTo)}
	if link != nil {
		st.LinkUserID = &link.User.ID
		st.LinkSessionHash = link.Hash
	}
	_, err := s.Pool.Exec(ctx, "DELETE FROM oidc_states WHERE expires_at < clock_timestamp()")
	if err != nil {
		return st, err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO oidc_states(state_digest,nonce,verifier,link_user_id,return_to,link_session_digest,expires_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()+interval '10 minutes')`,
		Digest(st.State), st.Nonce, st.Verifier, st.LinkUserID, st.ReturnTo, st.LinkSessionHash)
	return st, err
}

var oidcExpired = &fault.Error{Status: 400, Code: "sign_in_expired", Message: "That sign-in link expired or was already used. Start again."}

// ConsumeOIDC returns and deletes a pending state; each state works once.
func (s *Store) ConsumeOIDC(ctx context.Context, state string) (OIDCState, error) {
	st := OIDCState{State: state}
	err := s.Pool.QueryRow(ctx, "DELETE FROM oidc_states WHERE state_digest=$1 AND expires_at > clock_timestamp() RETURNING nonce,verifier,link_user_id,return_to,link_session_digest", Digest(state)).
		Scan(&st.Nonce, &st.Verifier, &st.LinkUserID, &st.ReturnTo, &st.LinkSessionHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, oidcExpired
	}
	return st, err
}

// OIDCClaims are the verified identity-token claims used to find or create an account.
type OIDCClaims struct {
	Issuer, Subject, Email, PreferredUsername, Name string
	EmailVerified                                   bool
}

var usernameCleaner = regexp.MustCompile(`[^a-z0-9_.-]+`)

// ManagedClaim claims an integration-managed account for a verified provider
// identity that matches no sign-in. It runs inside the sign-in transaction after the
// account lock and returns the claimed account; ok is false when nothing matched.
// The caller links the identity and signs the account in. Injecting it keeps this
// package free of the integration that owns managed accounts.
type ManagedClaim func(ctx context.Context, tx pgx.Tx, c OIDCClaims) (userID int64, ok bool, err error)

var claimRefused = &fault.Error{Status: 403, Code: "identity_claim_conflict", Message: "This sign-in matches a managed Pastime account that cannot be claimed automatically. Ask your administrator for help."}

// ClaimConflict refuses a sign-in that matches a managed account which cannot be
// claimed. It carries only a reason and identifiers, never issuer, subject or email.
type ClaimConflict struct {
	Reason          string
	InstanceID      string
	SourceAccountID string
	UserID          int64
}

func (c *ClaimConflict) Error() string { return "identity_claim_conflict: " + c.Reason }
func (c *ClaimConflict) Unwrap() error { return claimRefused }

// OIDCSignIn signs a verified provider identity in. A linked identity signs in to its
// account; a link request attaches it; an unlinked identity first claims a matching
// managed account through ManagedClaim; otherwise a new member account is created
// only when autoCreate allows it. A refused claim never falls back to creation.
// Disabled accounts never sign in.
func (s *Store) OIDCSignIn(ctx context.Context, c OIDCClaims, link *Actor, autoCreate bool) (User, string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accountLock); err != nil {
		return User{}, "", err
	}
	var linkUserID *int64
	if link != nil {
		current, err := AuthorizeTx(ctx, tx, *link, "")
		if err != nil {
			return User{}, "", err
		}
		if !current.Household() {
			return User{}, "", fault.Forbidden
		}
		linkUserID = &current.User.ID
	}
	var userID int64
	err = tx.QueryRow(ctx, "SELECT user_id FROM oidc_identities WHERE issuer=$1 AND subject=$2", c.Issuer, c.Subject).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) && linkUserID == nil && s.ManagedClaim != nil {
		claimed, ok, claimErr := s.ManagedClaim(ctx, tx, c)
		if claimErr != nil {
			return User{}, "", claimErr
		}
		if ok {
			if _, err = tx.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id,email) VALUES($1,$2,$3,$4)", c.Issuer, c.Subject, claimed, clip(c.Email, 254)); err != nil {
				return User{}, "", err
			}
			userID = claimed
		}
	}
	switch {
	case err == nil:
		if linkUserID != nil && *linkUserID != userID {
			return User{}, "", &fault.Error{Status: 409, Code: "identity_linked", Message: "That account is already linked to another Pastime account."}
		}
	case errors.Is(err, pgx.ErrNoRows) && linkUserID != nil:
		userID = *linkUserID
		if _, err = tx.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id,email) VALUES($1,$2,$3,$4)", c.Issuer, c.Subject, userID, clip(c.Email, 254)); err != nil {
			return User{}, "", err
		}
	case errors.Is(err, pgx.ErrNoRows) && autoCreate:
		base := usernameCleaner.ReplaceAllString(strings.ToLower(firstNonEmpty(c.PreferredUsername, strings.Split(c.Email, "@")[0], "member")), "")
		if len(base) < 3 {
			base += "user"
		}
		base = clip(strings.Trim(base, "._-"), 40)
		if len(base) < 3 {
			base = "member"
		}
		name := base
		for i := 2; ; i++ {
			var taken bool
			// Account usernames and profile login names share one namespace.
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=$1) OR EXISTS(SELECT 1 FROM profile_logins WHERE login_name=$1)", name).Scan(&taken); err != nil {
				return User{}, "", err
			}
			if !taken {
				break
			}
			name = base + strconv.Itoa(i)
		}
		display := clip(firstNonEmpty(c.Name, c.PreferredUsername, name), 80)
		email := ""
		if c.EmailVerified {
			email = clip(c.Email, 254)
		}
		if err = tx.QueryRow(ctx, "INSERT INTO users(username,display_name,email,password_hash,role) VALUES($1,$2,$3,'','member') RETURNING id", name, display, email).Scan(&userID); err != nil {
			return User{}, "", err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id,email) VALUES($1,$2,$3,$4)", c.Issuer, c.Subject, userID, clip(c.Email, 254)); err != nil {
			return User{}, "", err
		}
	case errors.Is(err, pgx.ErrNoRows):
		return User{}, "", &fault.Error{Status: 403, Code: "identity_unlinked", Message: "This sign-in is not linked to a Pastime account. Sign in with your password and link it from Account, or ask your administrator."}
	default:
		return User{}, "", err
	}
	var epoch int64
	var active bool
	if err = tx.QueryRow(ctx, "SELECT auth_epoch,active FROM users WHERE id=$1 FOR UPDATE", userID).Scan(&epoch, &active); err != nil {
		return User{}, "", err
	}
	if !active {
		return User{}, "", fault.Unauthenticated
	}
	if _, err = tx.Exec(ctx, "UPDATE oidc_identities SET last_login_at=clock_timestamp(),email=$3 WHERE issuer=$1 AND subject=$2", c.Issuer, c.Subject, clip(c.Email, 254)); err != nil {
		return User{}, "", err
	}
	u, raw, err := issueSession(ctx, tx, userID, epoch)
	if err != nil {
		return User{}, "", err
	}
	if err = Audit(ctx, tx, Actor{Kind: "oidc", User: u}, "session.oidc", "user", strconv.FormatInt(userID, 10), map[string]any{"issuer": c.Issuer}); err != nil {
		return User{}, "", err
	}
	return u, raw, tx.Commit(ctx)
}

type OIDCIdentity struct {
	Issuer      string     `json:"issuer"`
	Email       string     `json:"email"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

func (s *Store) OIDCIdentities(ctx context.Context, a Actor) ([]OIDCIdentity, error) {
	out := []OIDCIdentity{}
	if !a.Household() {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, "SELECT issuer,email,created_at,last_login_at FROM oidc_identities WHERE user_id=$1 ORDER BY created_at", a.User.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var i OIDCIdentity
		if err = rows.Scan(&i.Issuer, &i.Email, &i.CreatedAt, &i.LastLoginAt); err != nil {
			return out, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// UnlinkOIDC removes the caller's link to an issuer. An account with no password keeps
// at least one way to sign in.
func (s *Store) UnlinkOIDC(ctx context.Context, a Actor, issuer string) error {
	if !a.Household() {
		return fault.Forbidden
	}
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var hasPassword bool
	var links int
	if err = tx.QueryRow(ctx, "SELECT password_hash<>'',(SELECT count(*) FROM oidc_identities WHERE user_id=$1 AND issuer<>$2) FROM users WHERE id=$1", a.User.ID, issuer).Scan(&hasPassword, &links); err != nil {
		return err
	}
	if !hasPassword && links == 0 {
		return fault.Invalid("Set a password before removing your only sign-in method.")
	}
	tag, err := tx.Exec(ctx, "DELETE FROM oidc_identities WHERE user_id=$1 AND issuer=$2", a.User.ID, issuer)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fault.NotFound
	}
	if err = Audit(ctx, tx, a, "oidc.unlinked", "user", strconv.FormatInt(a.User.ID, 10), map[string]any{"issuer": issuer}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var aliasTaken = &fault.Error{Status: 409, Code: "identity_linked", Message: "That account is already linked to another Pastime account."}

func validSubject(v string, max int) bool {
	return v != "" && len(v) <= max && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
}

// AliasOIDCSubject adds a second subject under issuer for the account that already
// signs in with existing, and keeps the existing one. An operator uses it before a
// provider changes how it forms subjects, so the account keeps its sign-in. Only an
// administrator may alias, and the audit event records the issuer but no subject.
func (s *Store) AliasOIDCSubject(ctx context.Context, a Actor, issuer, existing, alias string, audit map[string]any) (User, error) {
	if !validSubject(issuer, 500) || !validSubject(existing, 255) || !validSubject(alias, 255) || existing == alias {
		return User{}, fault.Invalid("Invalid sign-in subject alias.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accountLock); err != nil {
		return User{}, err
	}
	fresh, err := AuthorizeTx(ctx, tx, a, "")
	if err != nil {
		return User{}, err
	}
	if !fresh.IsAdmin() {
		return User{}, fault.Forbidden
	}
	var userID int64
	var email string
	err = tx.QueryRow(ctx, "SELECT user_id,email FROM oidc_identities WHERE issuer=$1 AND subject=$2 FOR UPDATE", issuer, existing).Scan(&userID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, fault.NotFound
	}
	if err != nil {
		return User{}, err
	}
	tag, err := tx.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id,email) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", issuer, alias, userID, email)
	if err != nil {
		return User{}, err
	}
	if tag.RowsAffected() == 0 {
		return User{}, aliasTaken
	}
	u, err := ScanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1", userID))
	if err != nil {
		return User{}, err
	}
	metadata := map[string]any{}
	for k, v := range audit {
		metadata[k] = v
	}
	metadata["issuer"] = issuer
	if err = Audit(ctx, tx, fresh, "oidc.subject_alias", "user", strconv.FormatInt(userID, 10), metadata); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
