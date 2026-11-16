package identity

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// ProfileLogin is a profile's own sign-in, as the household's primary user
// sees it. The password is never returned.
type ProfileLogin struct {
	ProfileID   int64      `json:"profile_id,string"`
	LoginName   string     `json:"login_name"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

// ProfileLoginInput turns a login on or changes it. An empty Password keeps
// the current one; any other value resets it.
type ProfileLoginInput struct {
	LoginName string `json:"login_name"`
	Password  string `json:"password"`
}

const profileLoginColumns = "profile_id,login_name,created_at,last_login_at"

func scanProfileLogin(row pgx.Row) (ProfileLogin, error) {
	var l ProfileLogin
	err := row.Scan(&l.ProfileID, &l.LoginName, &l.CreatedAt, &l.LastLoginAt)
	return l, err
}

// ProfileLogins lists the logins of the household's active profiles.
func (s *Store) ProfileLogins(ctx context.Context, a Actor) ([]ProfileLogin, error) {
	if !a.Household() {
		return nil, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.profile_id,l.login_name,l.created_at,l.last_login_at FROM profile_logins l
		JOIN tracking_profiles p ON p.id=l.profile_id WHERE l.account_id=$1 AND p.archived_at IS NULL ORDER BY p.created_at,p.id`, a.User.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProfileLogin{}
	for rows.Next() {
		l, err := scanProfileLogin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetProfileLogin turns on, renames or resets the login of one of the
// household's non-default profiles. A new password signs the profile person
// out of every browser; a rename alone doesn't.
func (s *Store) SetProfileLogin(ctx context.Context, a Actor, profileID int64, in ProfileLoginInput) (ProfileLogin, error) {
	if !a.Household() {
		return ProfileLogin{}, fault.Forbidden
	}
	name, err := ValidateLoginName(in.LoginName)
	if err != nil {
		return ProfileLogin{}, err
	}
	hash := ""
	if in.Password != "" {
		if hash, err = s.Passwords.Hash(ctx, in.Password); err != nil {
			return ProfileLogin{}, err
		}
	}
	tx, fresh, err := s.identityTx(ctx, a)
	if err != nil {
		return ProfileLogin{}, err
	}
	defer tx.Rollback(ctx)
	if err = signInProfile(ctx, tx, fresh.User.ID, profileID); err != nil {
		return ProfileLogin{}, err
	}
	target := strconv.FormatInt(profileID, 10)
	var loginID int64
	var current string
	err = tx.QueryRow(ctx, `SELECT id,login_name FROM profile_logins WHERE profile_id=$1 AND account_id=$2 FOR UPDATE`, profileID, fresh.User.ID).Scan(&loginID, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		if hash == "" {
			return ProfileLogin{}, fault.Invalid("Choose a password for this sign-in.")
		}
		out, err := scanProfileLogin(tx.QueryRow(ctx, `INSERT INTO profile_logins(account_id,profile_id,login_name,password_hash) VALUES($1,$2,$3,$4) RETURNING `+profileLoginColumns, fresh.User.ID, profileID, name, hash))
		if err != nil {
			return ProfileLogin{}, dbError(err)
		}
		if err = Audit(ctx, tx, fresh, "profile_login.created", "profile", target, map[string]any{"login_name": name}); err != nil {
			return ProfileLogin{}, err
		}
		return out, tx.Commit(ctx)
	}
	if err != nil {
		return ProfileLogin{}, err
	}
	if name != current {
		if _, err = tx.Exec(ctx, `UPDATE profile_logins SET login_name=$2,updated_at=clock_timestamp() WHERE id=$1`, loginID, name); err != nil {
			return ProfileLogin{}, dbError(err)
		}
		if err = Audit(ctx, tx, fresh, "profile_login.updated", "profile", target, map[string]any{"login_name": name, "previous_login_name": current}); err != nil {
			return ProfileLogin{}, err
		}
	}
	if hash != "" {
		if err = replaceProfileLoginPassword(ctx, tx, loginID, hash); err != nil {
			return ProfileLogin{}, err
		}
		if err = Audit(ctx, tx, fresh, "profile_login.password_reset", "profile", target, map[string]any{"login_name": name}); err != nil {
			return ProfileLogin{}, err
		}
	}
	out, err := scanProfileLogin(tx.QueryRow(ctx, `SELECT `+profileLoginColumns+` FROM profile_logins WHERE id=$1`, loginID))
	if err != nil {
		return ProfileLogin{}, err
	}
	return out, tx.Commit(ctx)
}

// signInProfile checks that profileID is an active, non-default profile of
// account, which is what may have its own sign-in.
func signInProfile(ctx context.Context, tx pgx.Tx, account, profileID int64) error {
	var isDefault, archived bool
	err := tx.QueryRow(ctx, `SELECT is_default,archived_at IS NOT NULL FROM tracking_profiles WHERE id=$1 AND account_id=$2 FOR SHARE`, profileID, account).Scan(&isDefault, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	if err != nil {
		return err
	}
	if isDefault || archived {
		return fault.Invalid("This profile can't have its own sign-in.")
	}
	return nil
}

// replaceProfileLoginPassword stores a new hash and signs the login out of
// every browser.
func replaceProfileLoginPassword(ctx context.Context, tx pgx.Tx, loginID int64, hash string) error {
	if _, err := tx.Exec(ctx, `UPDATE profile_logins SET password_hash=$2,updated_at=clock_timestamp() WHERE id=$1`, loginID, hash); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE profile_login_id=$1 AND revoked_at IS NULL`, loginID)
	return err
}

// RemoveProfileLogin turns a profile's login off. Its sessions go with it.
func (s *Store) RemoveProfileLogin(ctx context.Context, a Actor, profileID int64) error {
	if !a.Household() {
		return fault.Forbidden
	}
	tx, fresh, err := s.identityTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var name string
	if err = tx.QueryRow(ctx, `DELETE FROM profile_logins WHERE profile_id=$1 AND account_id=$2 RETURNING login_name`, profileID, fresh.User.ID).Scan(&name); err != nil {
		return dbError(err)
	}
	if err = Audit(ctx, tx, fresh, "profile_login.removed", "profile", strconv.FormatInt(profileID, 10), map[string]any{"login_name": name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ChangeProfileLoginPassword lets a profile person change their own password
// with the current one. Every session of the login, including this one, ends.
func (s *Store) ChangeProfileLoginPassword(ctx context.Context, a Actor, current, password string) error {
	loginID := a.ProfileLoginID()
	if loginID == 0 {
		return fault.Forbidden
	}
	var old string
	if err := s.Pool.QueryRow(ctx, `SELECT password_hash FROM profile_logins WHERE id=$1`, loginID).Scan(&old); err != nil {
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
	tx, fresh, err := s.browserTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var name string
	if err = tx.QueryRow(ctx, `SELECT login_name FROM profile_logins WHERE id=$1 AND password_hash=$2 FOR UPDATE`, loginID, old).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.Conflict
		}
		return err
	}
	if err = replaceProfileLoginPassword(ctx, tx, loginID, hash); err != nil {
		return err
	}
	if err = Audit(ctx, tx, fresh, "profile_login.password_changed", "profile", strconv.FormatInt(fresh.TrackingID(), 10), map[string]any{"login_name": name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
