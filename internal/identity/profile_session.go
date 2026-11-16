package identity

import (
	"context"
	"errors"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// profileSessionLimit is how many open browser sessions one profile login
// keeps. It is separate from, and doesn't count toward, the account's own.
const profileSessionLimit = 10

// browserTx admits a browser session of either kind: the account's own, or a
// profile session acting for its one profile.
func (s *Store) browserTx(ctx context.Context, a Actor) (pgx.Tx, Actor, error) {
	return s.lockedTx(ctx, a, Actor.Browser)
}

// issueProfileSession opens a browser session for a profile login, keeps at
// most profileSessionLimit per login and records the sign-in on the login. It
// returns the user as the profile person sees it (PublicUser). The caller holds
// accountLock and verified hash for this login.
func issueProfileSession(ctx context.Context, tx pgx.Tx, accountID, loginID, epoch int64, hash string) (User, string, error) {
	// Account before credential, the order authenticate and revocation use.
	var currentEpoch int64
	var active bool
	if err := tx.QueryRow(ctx, "SELECT auth_epoch,active FROM users WHERE id=$1 FOR UPDATE", accountID).Scan(&currentEpoch, &active); err != nil {
		return User{}, "", fault.Unauthenticated
	}
	var currentHash, loginName, profileName string
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT l.password_hash,l.login_name,p.name,p.archived_at IS NOT NULL FROM profile_logins l
		JOIN tracking_profiles p ON p.id=l.profile_id WHERE l.id=$1 AND l.account_id=$2 FOR UPDATE OF l`, loginID, accountID).Scan(&currentHash, &loginName, &profileName, &archived); err != nil {
		return User{}, "", fault.Unauthenticated
	}
	if !active || archived || currentEpoch != epoch || currentHash != hash {
		return User{}, "", fault.Unauthenticated
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT pastime_account_has_service_access($1)`, accountID).Scan(&allowed); err != nil {
		return User{}, "", err
	}
	if !allowed {
		return User{}, "", ErrServiceAccessRequired
	}
	raw := randomToken("fn_s_")
	if _, err := tx.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,auth_epoch,expires_at,user_agent,last_used_at,profile_login_id)
		VALUES($1,$2,$3,clock_timestamp()+interval '7 days',$4,clock_timestamp(),$5)`, accountID, Digest(raw), epoch, clientOf(ctx), loginID); err != nil {
		return User{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE id IN
		(SELECT id FROM sessions WHERE profile_login_id=$1 AND revoked_at IS NULL ORDER BY created_at DESC,id OFFSET $2)`, loginID, profileSessionLimit); err != nil {
		return User{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE profile_logins SET last_login_at=clock_timestamp() WHERE id=$1`, loginID); err != nil {
		return User{}, "", err
	}
	u, err := ScanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id=$1`, accountID))
	if err != nil {
		return User{}, "", err
	}
	a := Actor{Kind: KindProfileSession, User: u, profileLoginID: loginID, loginName: loginName, profileName: profileName}
	return a.PublicUser(), raw, nil
}

// ForHousehold refuses anyone who may not act for the household as a whole,
// for things shared between accounts rather than profiles (collaborative
// lists). The account's own session passes, and so do credentials bound to the
// account's default profile: those are the primary user's own, since a profile
// session can only mint tokens for its own, non-default profile. A profile
// session, and a credential bound to any other profile, get fault.Forbidden.
func ForHousehold(ctx context.Context, q queryer, a Actor) error {
	if a.Household() {
		return nil
	}
	if a.Browser() {
		return fault.Forbidden
	}
	var isDefault bool
	err := q.QueryRow(ctx, `SELECT is_default FROM tracking_profiles WHERE id=$1 AND account_id=$2`, a.TrackingID(), a.User.ID).Scan(&isDefault)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !isDefault {
		return fault.Forbidden
	}
	return nil
}

// VisibleUser is the account as a may see it. The household's own session and
// its default-profile credentials see the account. A profile session sees its
// own login (PublicUser); any other credential bound to a non-default profile,
// such as a token a profile person minted, sees the account without its
// contact email, role or last sign-in.
func (s *Store) VisibleUser(ctx context.Context, a Actor) (User, error) {
	if a.Browser() {
		return a.PublicUser(), nil
	}
	err := ForHousehold(ctx, s.Pool, a)
	if err == nil {
		return a.User, nil
	}
	if !errors.Is(err, fault.Forbidden) {
		return User{}, err
	}
	u := a.User
	u.Email, u.Role, u.LastLoginAt = "", "member", nil
	return u, nil
}
