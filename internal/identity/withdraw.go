package identity

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// LockAccounts takes the lock that serializes sign-in identity changes, the one
// OIDCSignIn, aliasing and invitations hold. Code outside this package that removes
// sign-ins takes it first, before any row lock.
func LockAccounts(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accountLock)
	return err
}

// AccessRemoved counts what WithdrawAccess removed. It holds no identity details.
type AccessRemoved struct {
	SignIns       int
	Password      bool
	Authenticator bool
	PublicProfile bool
	Integrations  int
}

// WithdrawAccess removes every way into an account and every place its activity is
// sent: all single sign-on identities, the password and the authenticator that could
// reset it, sessions and tokens (through a new sign-out-everywhere epoch), pending
// invitations, calendar feeds, the public profile, server webhooks, linked services,
// pending service authorizations, notification targets and reading-sync keys. Tracking
// data and profiles stay. The caller holds LockAccounts and the user row lock, and
// decides whether the account may be withdrawn.
func WithdrawAccess(ctx context.Context, tx pgx.Tx, userID int64) (AccessRemoved, error) {
	var out AccessRemoved
	tag, err := tx.Exec(ctx, "DELETE FROM oidc_identities WHERE user_id=$1", userID)
	if err != nil {
		return out, err
	}
	out.SignIns = int(tag.RowsAffected())
	if err = tx.QueryRow(ctx, "SELECT password_hash<>'',public_profile FROM users WHERE id=$1", userID).Scan(&out.Password, &out.PublicProfile); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET password_hash='',public_profile=false,auth_epoch=auth_epoch+1,version=version+1 WHERE id=$1", userID); err != nil {
		return out, err
	}
	if tag, err = tx.Exec(ctx, "DELETE FROM user_totp WHERE user_id=$1", userID); err != nil {
		return out, err
	}
	out.Authenticator = tag.RowsAffected() > 0
	for _, table := range []string{"connections", "service_links", "notification_targets", "kosync_accounts"} {
		if tag, err = tx.Exec(ctx, "DELETE FROM "+table+" WHERE user_id=$1", userID); err != nil {
			return out, err
		}
		out.Integrations += int(tag.RowsAffected())
	}
	if _, err = tx.Exec(ctx, "DELETE FROM service_authorizations WHERE user_id=$1", userID); err != nil {
		return out, err
	}
	return out, revokeAll(ctx, tx, userID)
}
