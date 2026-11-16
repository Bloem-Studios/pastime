package identity

import (
	"context"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
)

type UserFilter struct {
	Query, Role, Status, Cursor string
	Limit                       int
}

func (s *Store) Users(ctx context.Context, a Actor, f UserFilter) (page.Result[User], error) {
	out := page.Result[User]{Items: []User{}}
	if !a.IsAdmin() {
		return out, fault.Forbidden
	}
	limit, err := page.Limit(f.Limit)
	if err != nil {
		return out, err
	}
	f.Query = strings.ToLower(strings.TrimSpace(f.Query))
	if len(f.Query) > 48 || (f.Role != "" && !ValidRole(f.Role)) || (f.Status != "" && f.Status != "active" && f.Status != "disabled") {
		return out, fault.Invalid("Invalid user filters.")
	}
	scope := "users|" + f.Query + "|" + f.Role + "|" + f.Status
	last, err := page.Decode(scope, f.Cursor)
	if err != nil {
		return out, err
	}
	query := "SELECT " + userColumns + " FROM users u WHERE true"
	args := []any{}
	add := func(clause string, v any) {
		args = append(args, v)
		query += " AND " + strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args)))
	}
	if last > 0 {
		add("u.id<?", last)
	}
	if f.Role != "" {
		add("u.role=?", f.Role)
	}
	if f.Status != "" {
		add("u.active=?", f.Status == "active")
	}
	if f.Query != "" {
		add(`u.username LIKE ? ESCAPE '\'`, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query)+"%")
	}
	args = append(args, limit+1)
	query += " ORDER BY u.id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		u, e := ScanUser(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, u)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode(scope, out.Items[len(out.Items)-1].ID)
		out.NextCursor = &next
	}
	return out, nil
}
func (s *Store) User(ctx context.Context, a Actor, id int64) (User, error) {
	if !a.IsAdmin() && a.User.ID != id {
		return User{}, fault.Forbidden
	}
	u, err := ScanUser(s.Pool.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE id=$1", id))
	return u, dbError(err)
}

type UserPatch struct {
	Version     int     `json:"version"`
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
	Role        *string `json:"role,omitempty"`
	Active      *bool   `json:"active,omitempty"`
}

func (s *Store) UpdateUser(ctx context.Context, a Actor, id int64, p UserPatch, personal bool) (User, error) {
	if p.Version < 1 || (p.DisplayName == nil && p.Email == nil && p.Role == nil && p.Active == nil) {
		return User{}, fault.Invalid("Provide a change and the current version.")
	}
	if p.DisplayName != nil {
		v := strings.TrimSpace(*p.DisplayName)
		p.DisplayName = &v
		if len([]rune(v)) < 1 || len([]rune(v)) > 80 {
			return User{}, fault.Invalid("Display name must contain 1 to 80 characters.")
		}
	}
	if p.Email != nil {
		*p.Email = strings.TrimSpace(*p.Email)
		if err := ValidateEmail(*p.Email); err != nil {
			return User{}, err
		}
	}
	if p.Role != nil && !ValidRole(*p.Role) {
		return User{}, fault.Invalid("Invalid role.")
	}
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	if personal {
		if a.User.ID != id || p.Role != nil || p.Active != nil {
			return User{}, fault.Forbidden
		}
	} else if !a.IsAdmin() {
		return User{}, fault.Forbidden
	}
	u, err := ScanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return u, dbError(err)
	}
	if !personal && (!a.CanManage(u.Role) || (p.Role != nil && !a.CanManage(*p.Role))) {
		return u, fault.Forbidden
	}
	if u.Version != p.Version {
		return u, fault.Conflict
	}
	role, active := u.Role, u.Active
	if p.Role != nil {
		role = *p.Role
	}
	if p.Active != nil {
		active = *p.Active
	}
	if u.Role == "owner" && u.Active && u.PasswordReady && (role != "owner" || !active) {
		var owners int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='owner' AND active AND password_hash<>''").Scan(&owners); err != nil {
			return u, err
		}
		if owners <= 1 {
			return u, fault.LastOwner
		}
	}
	display, email := u.DisplayName, u.Email
	if p.DisplayName != nil {
		display = *p.DisplayName
	}
	if p.Email != nil {
		email = *p.Email
	}
	// Role/status changes terminate existing sessions and tokens; reactivation never revives them.
	invalidate := role != u.Role || active != u.Active
	u, err = ScanUser(tx.QueryRow(ctx, `UPDATE users AS u SET display_name=$2,email=$3,role=$4,active=$5,version=version+1,
		auth_epoch=auth_epoch+CASE WHEN $6 THEN 1 ELSE 0 END WHERE id=$1 RETURNING `+userColumns, id, display, email, role, active, invalidate))
	if err != nil {
		return u, dbError(err)
	}
	if invalidate {
		if err = revokeAll(ctx, tx, id); err != nil {
			return u, err
		}
	}
	if err = Audit(ctx, tx, a, "user.updated", "user", strconv.FormatInt(id, 10), map[string]any{"role": role, "active": active}); err != nil {
		return u, err
	}
	return u, tx.Commit(ctx)
}
func revokeAll(ctx context.Context, tx pgx.Tx, id int64) error {
	for _, statement := range []string{
		"UPDATE sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL",
		"UPDATE api_tokens SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL",
		"UPDATE invitations SET revoked_at=clock_timestamp() WHERE (issuer_id=$1 OR user_id=$1) AND claimed_at IS NULL AND revoked_at IS NULL",
		"DELETE FROM calendar_feeds WHERE user_id=$1",
		"DELETE FROM connections WHERE user_id=$1",
		// Standing keys the person set up end with their sessions: the KOReader key and
		// Connect Silo keys (issuance self:...). Keys Silo provisioning issues stay with
		// the provisioning-key holder.
		"DELETE FROM kosync_accounts WHERE user_id=$1",
		"UPDATE bloem_sync_credentials c SET revoked_at=clock_timestamp() FROM tracking_profiles t WHERE t.id=c.profile_id AND t.account_id=$1 AND c.issuance_id LIKE 'self:%' AND (c.revoked_at IS NULL OR c.revoked_at>clock_timestamp())",
	} {
		if _, err := tx.Exec(ctx, statement, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) ChangePassword(ctx context.Context, a Actor, current, password string) error {
	if a.ProfileLoginID() != 0 {
		return s.ChangeProfileLoginPassword(ctx, a, current, password)
	}
	if !a.Household() {
		return fault.Forbidden
	}
	var old string
	if err := s.Pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1", a.User.ID).Scan(&old); err != nil {
		return fault.Unauthenticated
	}
	valid, err := s.Passwords.Verify(ctx, current, old)
	if err != nil {
		return err
	}
	if !valid {
		return fault.Invalid("The current password is incorrect.")
	}
	hash, err := s.Passwords.Hash(ctx, password)
	if err != nil {
		return err
	}
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	r, err := tx.Exec(ctx, "UPDATE users SET password_hash=$2,auth_epoch=auth_epoch+1,version=version+1 WHERE id=$1 AND password_hash=$3", a.User.ID, hash, old)
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return fault.Conflict
	}
	if err = revokeAll(ctx, tx, a.User.ID); err != nil {
		return err
	}
	if err = Audit(ctx, tx, a, "password.changed", "user", strconv.FormatInt(a.User.ID, 10), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AuditLog(ctx context.Context, a Actor, cursor string, limit int) (page.Result[AuditEvent], error) {
	out := page.Result[AuditEvent]{Items: []AuditEvent{}}
	if !a.IsAdmin() {
		return out, fault.Forbidden
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	last, err := page.Decode("audit", cursor)
	if err != nil {
		return out, err
	}
	// An account target is named by its current username; the id stays for accounts since deleted.
	query := `SELECT a.id,a.actor_label,a.action,a.target_kind,a.target_id,
		COALESCE(CASE WHEN a.target_kind='user' AND a.target_id ~ '^[1-9][0-9]{0,17}$' THEN (SELECT u.username FROM users u WHERE u.id=a.target_id::bigint) END,''),a.created_at FROM audit_events a`
	args := []any{}
	if last > 0 {
		query += " WHERE a.id<$1"
		args = append(args, last)
	}
	args = append(args, limit+1)
	query += " ORDER BY a.id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e AuditEvent
		if err = rows.Scan(&e.ID, &e.Actor, &e.Action, &e.TargetKind, &e.TargetID, &e.TargetLabel, &e.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode("audit", out.Items[len(out.Items)-1].ID)
		out.NextCursor = &next
	}
	return out, nil
}
