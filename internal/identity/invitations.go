package identity

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

type InviteInput struct {
	Role           string `json:"role"`
	RecipientEmail string `json:"recipient_email"`
	ExpiresInHours int    `json:"expires_in_hours"`
}
type CreateUserInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

func validateInvite(input *InviteInput) error {
	if input.Role == "" {
		input.Role = "member"
	}
	if !ValidRole(input.Role) {
		return fault.Invalid("Invalid role.")
	}
	input.RecipientEmail = strings.TrimSpace(input.RecipientEmail)
	if err := ValidateEmail(input.RecipientEmail); err != nil {
		return err
	}
	if input.ExpiresInHours == 0 {
		input.ExpiresInHours = 72
	}
	if input.ExpiresInHours < 1 || input.ExpiresInHours > 168 {
		return fault.Invalid("Invitation lifetime must be between 1 and 168 hours.")
	}
	return nil
}
func insertInvite(ctx context.Context, tx pgx.Tx, a Actor, input InviteInput, userID *int64, username string) (Invitation, string, error) {
	raw := randomToken("fn_i_")
	kind := "join"
	if userID != nil {
		kind = "reset"
	}
	v := Invitation{Kind: kind, Role: input.Role, RecipientEmail: input.RecipientEmail, Username: username, Status: "pending"}
	err := tx.QueryRow(ctx, `INSERT INTO invitations(issuer_id,user_id,kind,role,recipient_email,token_hash,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()+($7*interval '1 hour')) RETURNING id::text,created_at,expires_at`, a.User.ID, userID, kind, input.Role, input.RecipientEmail, Digest(raw), input.ExpiresInHours).Scan(&v.ID, &v.CreatedAt, &v.ExpiresAt)
	if err != nil {
		return v, "", err
	}
	if err = Audit(ctx, tx, a, "invitation.created", "invitation", v.ID, map[string]any{"kind": kind, "role": input.Role}); err != nil {
		return v, "", err
	}
	return v, raw, nil
}
func (s *Store) Invite(ctx context.Context, a Actor, input InviteInput) (Invitation, string, error) {
	if err := validateInvite(&input); err != nil {
		return Invitation{}, "", err
	}
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return Invitation{}, "", err
	}
	defer tx.Rollback(ctx)
	if !a.CanManage(input.Role) {
		return Invitation{}, "", fault.Forbidden
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE claimed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()`).Scan(&pending); err != nil {
		return Invitation{}, "", err
	}
	if pending >= 100 {
		return Invitation{}, "", fault.Invalid("Revoke or complete an existing invitation before creating more.")
	}
	v, raw, err := insertInvite(ctx, tx, a, input, nil, "")
	if err != nil {
		return v, "", err
	}
	return v, raw, tx.Commit(ctx)
}
func (s *Store) CreateUser(ctx context.Context, a Actor, input CreateUserInput) (User, Invitation, string, error) {
	username, err := ValidateUsername(input.Username)
	if err != nil {
		return User{}, Invitation{}, "", err
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		input.DisplayName = username
	}
	if len([]rune(input.DisplayName)) > 80 {
		return User{}, Invitation{}, "", fault.Invalid("Display name is too long.")
	}
	i := InviteInput{Role: input.Role, RecipientEmail: input.Email, ExpiresInHours: 72}
	if err = validateInvite(&i); err != nil {
		return User{}, Invitation{}, "", err
	}
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return User{}, Invitation{}, "", err
	}
	defer tx.Rollback(ctx)
	if !a.CanManage(i.Role) {
		return User{}, Invitation{}, "", fault.Forbidden
	}
	u, err := ScanUser(tx.QueryRow(ctx, `INSERT INTO users AS u(username,display_name,email,password_hash,role) VALUES($1,$2,$3,'',$4) RETURNING `+userColumns, username, input.DisplayName, i.RecipientEmail, i.Role))
	if err != nil {
		return u, Invitation{}, "", dbError(err)
	}
	v, raw, err := insertInvite(ctx, tx, a, i, &u.ID, u.Username)
	if err != nil {
		return u, v, "", err
	}
	if err = Audit(ctx, tx, a, "user.created", "user", strconv.FormatInt(u.ID, 10), map[string]any{"role": i.Role}); err != nil {
		return u, v, "", err
	}
	return u, v, raw, tx.Commit(ctx)
}
func (s *Store) ResetLink(ctx context.Context, a Actor, userID int64) (Invitation, string, error) {
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return Invitation{}, "", err
	}
	defer tx.Rollback(ctx)
	u, err := ScanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE id=$1 FOR UPDATE", userID))
	if err != nil {
		return Invitation{}, "", dbError(err)
	}
	if !a.CanManage(u.Role) || !u.Active {
		return Invitation{}, "", fault.Forbidden
	}
	// Issuing a reset link does not lock out the account; successful claim revokes all old access.
	if _, err = tx.Exec(ctx, `UPDATE invitations SET revoked_at=clock_timestamp() WHERE user_id=$1 AND kind='reset' AND claimed_at IS NULL AND revoked_at IS NULL`, userID); err != nil {
		return Invitation{}, "", err
	}
	v, raw, err := insertInvite(ctx, tx, a, InviteInput{Role: u.Role, RecipientEmail: u.Email, ExpiresInHours: 1}, &userID, u.Username)
	if err != nil {
		return v, "", err
	}
	return v, raw, tx.Commit(ctx)
}

func (s *Store) Invitations(ctx context.Context, a Actor) ([]Invitation, error) {
	if !a.IsAdmin() {
		return nil, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT i.id::text,i.kind,i.role,i.recipient_email,COALESCE(u.username,''),
		CASE WHEN i.claimed_at IS NOT NULL THEN 'claimed' WHEN i.revoked_at IS NOT NULL THEN 'revoked' WHEN i.expires_at<=clock_timestamp() THEN 'expired' ELSE 'pending' END,
		i.created_at,i.expires_at FROM invitations i LEFT JOIN users u ON u.id=i.user_id ORDER BY i.created_at DESC,i.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		var v Invitation
		if err = rows.Scan(&v.ID, &v.Kind, &v.Role, &v.RecipientEmail, &v.Username, &v.Status, &v.CreatedAt, &v.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) RevokeInvitation(ctx context.Context, a Actor, id string) error {
	if !ValidUUID(id) {
		return fault.NotFound
	}
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	if err = tx.QueryRow(ctx, "SELECT role FROM invitations WHERE id=$1", id).Scan(&role); err != nil {
		return dbError(err)
	}
	if !a.CanManage(role) {
		return fault.Forbidden
	}
	r, err := tx.Exec(ctx, "UPDATE invitations SET revoked_at=clock_timestamp() WHERE id=$1 AND claimed_at IS NULL AND revoked_at IS NULL", id)
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return fault.Conflict
	}
	if err = Audit(ctx, tx, a, "invitation.revoked", "invitation", id, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type claimRecord struct {
	ID                          string
	UserID                      *int64
	IssuerID                    int64
	Role, Kind, Email, Username string
	Expires                     time.Time
}

func readClaim(ctx context.Context, q queryer, raw string, locked bool) (claimRecord, error) {
	var c claimRecord
	if len(raw) != 48 || !strings.HasPrefix(raw, "fn_i_") {
		return c, fault.UnavailableInvite
	}
	query := `SELECT i.id::text,i.user_id,i.issuer_id,i.role,i.kind,i.recipient_email,COALESCE(target.username,''),i.expires_at
		FROM invitations i JOIN users issuer ON issuer.id=i.issuer_id LEFT JOIN users target ON target.id=i.user_id
		WHERE i.token_hash=$1 AND i.revoked_at IS NULL AND i.claimed_at IS NULL AND i.expires_at>clock_timestamp()
		AND issuer.active AND (issuer.role='owner' OR (issuer.role='admin' AND i.role='member'))
		AND (i.kind='join' OR (target.active AND (issuer.role='owner' OR target.role='member')))`
	if locked {
		query += " FOR UPDATE OF i"
	}
	err := q.QueryRow(ctx, query, Digest(raw)).Scan(&c.ID, &c.UserID, &c.IssuerID, &c.Role, &c.Kind, &c.Email, &c.Username, &c.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fault.UnavailableInvite
	}
	return c, err
}
func (s *Store) PreviewInvite(ctx context.Context, raw string) (InvitePreview, error) {
	c, err := readClaim(ctx, s.Pool, raw, false)
	if err != nil {
		return InvitePreview{}, err
	}
	return InvitePreview{Kind: c.Kind, Role: c.Role, Username: c.Username, ExpiresAt: c.Expires}, nil
}
func (s *Store) Claim(ctx context.Context, raw, username, displayName, password string) (User, error) {
	// Reject unavailable links before spending password-hash CPU, then recheck atomically.
	preview, err := s.PreviewInvite(ctx, raw)
	if err != nil {
		return User{}, err
	}
	if preview.Kind == "join" {
		username, err = ValidateUsername(username)
		if err != nil {
			return User{}, err
		}
	} else {
		username = preview.Username
	}
	displayName = strings.TrimSpace(displayName)
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
	c, err := readClaim(ctx, tx, raw, true)
	if err != nil {
		return User{}, err
	}
	var u User
	if c.Kind == "join" {
		u, err = ScanUser(tx.QueryRow(ctx, `INSERT INTO users AS u(username,display_name,email,password_hash,role) VALUES($1,$2,$3,$4,$5) RETURNING `+userColumns, username, displayName, c.Email, hash, c.Role))
	} else {
		u, err = ScanUser(tx.QueryRow(ctx, `UPDATE users AS u SET password_hash=$2,auth_epoch=auth_epoch+1,version=version+1 WHERE id=$1 AND active RETURNING `+userColumns, *c.UserID, hash))
		if err == nil {
			err = revokeAll(ctx, tx, u.ID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE invitations SET revoked_at=clock_timestamp() WHERE user_id=$1 AND id<>$2 AND claimed_at IS NULL AND revoked_at IS NULL", u.ID, c.ID)
		}
	}
	if err != nil {
		return User{}, dbError(err)
	}
	if _, err = tx.Exec(ctx, "UPDATE invitations SET claimed_at=clock_timestamp() WHERE id=$1", c.ID); err != nil {
		return User{}, err
	}
	if err = Audit(ctx, tx, Actor{User: u}, "invitation.claimed", "invitation", c.ID, map[string]any{"kind": c.Kind}); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}
