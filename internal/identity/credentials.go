package identity

import (
	"context"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

type TokenInput struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expires_in_days"`
	Password      string   `json:"password"`
}

// ownTokens limits api_tokens to what a may see: the household sees every
// token of the account, a profile session only its own profile's.
func ownTokens(a Actor) (string, int64) {
	if a.Household() {
		return "user_id=$1 AND $2::bigint=0", 0
	}
	return "user_id=$1 AND profile_id=$2", a.TrackingID()
}

func (s *Store) Tokens(ctx context.Context, a Actor) ([]Token, error) {
	if !a.Browser() {
		return nil, fault.Forbidden
	}
	where, profile := ownTokens(a)
	rows, err := s.Pool.Query(ctx, `SELECT id::text,name,scopes,created_at,expires_at,(revoked_at IS NOT NULL),profile_id FROM api_tokens WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT 100`, a.User.ID, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err = rows.Scan(&t.ID, &t.Name, &t.Scopes, &t.CreatedAt, &t.ExpiresAt, &t.Revoked, &t.ProfileID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) MintToken(ctx context.Context, a Actor, in TokenInput) (Token, string, error) {
	if !a.Browser() {
		return Token{}, "", fault.Forbidden
	}
	in.Name = strings.TrimSpace(in.Name)
	if len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 80 {
		return Token{}, "", fault.Invalid("Give this token a name with 1 to 80 characters.")
	}
	if in.ExpiresInDays < 1 || in.ExpiresInDays > 365 {
		return Token{}, "", fault.Invalid("Token lifetime must be between 1 and 365 days.")
	}
	if len(in.Scopes) < 1 || len(in.Scopes) > 5 {
		return Token{}, "", fault.Invalid("Choose at least one supported scope.")
	}
	seen := map[string]bool{}
	for _, scope := range in.Scopes {
		valid := false
		for _, allowed := range Scopes {
			if scope == allowed {
				valid = true
			}
		}
		if !valid || seen[scope] {
			return Token{}, "", fault.Invalid("Choose unique supported scopes.")
		}
		seen[scope] = true
	}
	// A profile person confirms with their own login's password.
	var hash string
	if err := s.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 AND $2::bigint=0
		UNION ALL SELECT password_hash FROM profile_logins WHERE id=$2 AND account_id=$1`, a.User.ID, a.ProfileLoginID()).Scan(&hash); err != nil {
		return Token{}, "", fault.Unauthenticated
	}
	valid, err := s.Passwords.Verify(ctx, in.Password, hash)
	if err != nil {
		return Token{}, "", err
	}
	if !valid {
		return Token{}, "", fault.Invalid("The password is incorrect.")
	}
	tx, a, err := s.browserTx(ctx, a)
	if err != nil {
		return Token{}, "", err
	}
	defer tx.Rollback(ctx)
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM api_tokens WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp()", a.User.ID).Scan(&count); err != nil {
		return Token{}, "", err
	}
	if count >= 20 {
		return Token{}, "", fault.Invalid("Revoke an existing token before creating more.")
	}
	raw := randomToken("fn_k_")
	t := Token{Name: in.Name, Scopes: in.Scopes, ProfileID: a.TrackingID()}
	err = tx.QueryRow(ctx, `INSERT INTO api_tokens(user_id,name,token_hash,scopes,auth_epoch,expires_at,profile_id)
		SELECT id,$2,$3,$4,auth_epoch,clock_timestamp()+($5*interval '1 day'),$6 FROM users WHERE id=$1
		RETURNING id::text,created_at,expires_at`, a.User.ID, in.Name, Digest(raw), in.Scopes, in.ExpiresInDays, a.TrackingID()).Scan(&t.ID, &t.CreatedAt, &t.ExpiresAt)
	if err != nil {
		return t, "", err
	}
	if err = Audit(ctx, tx, a, "token.created", "token", t.ID, nil); err != nil {
		return t, "", err
	}
	return t, raw, tx.Commit(ctx)
}
func (s *Store) RevokeToken(ctx context.Context, a Actor, id string) error {
	if !ValidUUID(id) {
		return fault.NotFound
	}
	tx, a, err := s.browserTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	where, profile := ownTokens(a)
	r, err := tx.Exec(ctx, "UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$3 AND "+where, a.User.ID, profile, id)
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return fault.NotFound
	}
	if err = Audit(ctx, tx, a, "token.revoked", "token", id, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ownSessions limits sessions to a's own: the primary user's account
// sessions, or one profile login's sessions. Neither sees the other's.
func ownSessions(a Actor) (string, int64) {
	if a.Household() {
		return "user_id=$1 AND profile_login_id IS NULL AND $2::bigint=0", 0
	}
	return "user_id=$1 AND profile_login_id=$2", a.ProfileLoginID()
}

func (s *Store) Sessions(ctx context.Context, a Actor) ([]Session, error) {
	if !a.Browser() {
		return nil, fault.Forbidden
	}
	where, login := ownSessions(a)
	rows, err := s.Pool.Query(ctx, `SELECT id::text,created_at,expires_at,user_agent,last_used_at FROM sessions WHERE `+where+` AND revoked_at IS NULL AND expires_at>clock_timestamp() ORDER BY created_at DESC,id DESC LIMIT 10`, a.User.ID, login)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var v Session
		if err = rows.Scan(&v.ID, &v.CreatedAt, &v.ExpiresAt, &v.UserAgent, &v.LastUsedAt); err != nil {
			return nil, err
		}
		v.Current = v.ID == a.CredentialID
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) RevokeSession(ctx context.Context, a Actor, id string) error {
	if !ValidUUID(id) {
		return fault.NotFound
	}
	tx, a, err := s.browserTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	where, login := ownSessions(a)
	r, err := tx.Exec(ctx, "UPDATE sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$3 AND "+where, a.User.ID, login, id)
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return fault.NotFound
	}
	if err = Audit(ctx, tx, a, "session.revoked", "session", id, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) RevokeUserAccess(ctx context.Context, a Actor, userID int64) error {
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	u, err := ScanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE id=$1 FOR UPDATE", userID))
	if err != nil {
		return dbError(err)
	}
	if !a.CanManage(u.Role) {
		return fault.Forbidden
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1,version=version+1 WHERE id=$1", userID); err != nil {
		return err
	}
	if err = revokeAll(ctx, tx, userID); err != nil {
		return err
	}
	if err = Audit(ctx, tx, a, "user.access_revoked", "user", strconv.FormatInt(userID, 10), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
