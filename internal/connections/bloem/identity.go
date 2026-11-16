package bloem

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Subject modes of a declared sign-in issuer: how its provider forms subjects.
const (
	SubjectUserUUID     = "user_uuid"
	SubjectHashedUserID = "hashed_user_id"
)

var (
	// The integration's plugin reads these codes to hold one account back while the
	// others converge, so their codes are part of the provisioning contract.
	errIdentityMismatch = &fault.Error{Status: 403, Code: "bloem_identity_mismatch", Message: "The managed identity issuer does not match this integration."}
	errIdentityConflict = &fault.Error{Status: 409, Code: "bloem_identity_conflict", Message: "This managed identity belongs to another account or cannot change."}
	errIssuerInUse      = &fault.Error{Status: 409, Code: "identity_issuer_in_use", Message: "The identity issuer cannot change while managed identities or sign-in issuers depend on it."}
	errIssuerRequired   = &fault.Error{Status: 409, Code: "identity_issuer_required", Message: "Set the integration identity issuer before declaring a sign-in issuer."}
	errLoginDeclared    = &fault.Error{Status: 409, Code: "login_issuer_declared", Message: "This sign-in issuer is already declared with another subject mode. Remove it first."}
	errNotClaimed       = &fault.Error{Status: 409, Code: "bloem_account_not_claimed", Message: "This managed account was not claimed by signing in; nothing was changed."}
	errClaimPrivileged  = &fault.Error{Status: 409, Code: "claimed_account_privileged", Message: "The claimed account is an owner or administrator. Change its role to member before revoking the claim."}
)

var hashedSubject = regexp.MustCompile(`^[0-9a-f]{64}$`)

// subjectShape reports whether subject has the form mode promises. A provider that
// forms subjects differently from the identity issuer never yields equal subjects
// for one person, so a mismatched shape must never claim or link.
func subjectShape(mode, subject string) bool {
	switch mode {
	case SubjectUserUUID:
		return identity.ValidUUID(subject)
	case SubjectHashedUserID:
		return hashedSubject.MatchString(subject)
	}
	return false
}

// validIssuer accepts an HTTPS issuer URL without credentials, query or fragment.
func validIssuer(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		len(v) <= 500 && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
}

func identityConstraint(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) && e.Code == "23505" && e.ConstraintName == "bloem_accounts_identity" {
		return errIdentityConflict
	}
	return constraint(err)
}

func withAudit(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// adminTx opens a transaction holding the administrator's current authority.
func (s Store) adminTx(ctx context.Context, a identity.Actor) (pgx.Tx, identity.Actor, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, a, err
	}
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "")
	if err == nil && !fresh.IsAdmin() {
		err = fault.Forbidden
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, a, err
	}
	return tx, fresh, nil
}

// lockInstanceByID takes the instance row lock that provisioning's shared lock waits
// on, so no provision commits an identity under an issuer that is changing.
func lockInstanceByID(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var issuer string
	err := tx.QueryRow(ctx, `SELECT identity_issuer FROM bloem_instances WHERE id=$1 FOR UPDATE`, id).Scan(&issuer)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fault.NotFound
	}
	return issuer, err
}

// SetIdentityIssuer sets the issuer whose verified identities the instance may send.
// Setting the current value changes nothing. A non-empty issuer can change only
// while no managed account carries or is bound to an identity and no sign-in issuer
// is declared.
func (s Store) SetIdentityIssuer(ctx context.Context, a identity.Actor, instanceID, issuer string, audit map[string]any) (string, bool, error) {
	if !validIssuer(issuer) {
		return "", false, fault.Invalid("Enter a valid integration identity issuer.")
	}
	tx, fresh, err := s.adminTx(ctx, a)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	previous, err := lockInstanceByID(ctx, tx, instanceID)
	if err != nil {
		return "", false, err
	}
	if previous == issuer {
		return previous, false, nil
	}
	if previous != "" {
		var used bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_accounts WHERE instance_id=$1 AND (identity_subject<>'' OR bound_subject<>'')) OR EXISTS(SELECT 1 FROM bloem_login_issuers WHERE instance_id=$1)`, instanceID).Scan(&used); err != nil {
			return "", false, err
		}
		if used {
			return "", false, errIssuerInUse
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_instances SET identity_issuer=$2 WHERE id=$1`, instanceID, issuer); err != nil {
		return "", false, err
	}
	if err = identity.Audit(ctx, tx, fresh, "bloem.instance.identity_issuer", "bloem_instance", instanceID, withAudit(audit, map[string]any{"old_issuer": previous, "new_issuer": issuer})); err != nil {
		return "", false, err
	}
	return previous, true, tx.Commit(ctx)
}

// DeclareLoginIssuer records that subjects from loginIssuer, formed as mode, name the
// same people as the instance's identity issuer. Declaring the current pair again
// changes nothing; a different mode must be removed first.
func (s Store) DeclareLoginIssuer(ctx context.Context, a identity.Actor, instanceID, loginIssuer, mode string, audit map[string]any) (bool, error) {
	if !validIssuer(loginIssuer) || len(loginIssuer) < 9 || mode != SubjectUserUUID && mode != SubjectHashedUserID {
		return false, fault.Invalid("Enter a valid sign-in issuer and subject mode.")
	}
	tx, fresh, err := s.adminTx(ctx, a)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	issuer, err := lockInstanceByID(ctx, tx, instanceID)
	if err != nil {
		return false, err
	}
	if issuer == "" {
		return false, errIssuerRequired
	}
	if issuer == loginIssuer {
		// The identity issuer is equivalent to itself without a declaration.
		return false, fault.Invalid("Enter a valid sign-in issuer and subject mode.")
	}
	var current string
	err = tx.QueryRow(ctx, `SELECT subject_mode FROM bloem_login_issuers WHERE instance_id=$1 AND login_issuer=$2`, instanceID, loginIssuer).Scan(&current)
	switch {
	case err == nil && current == mode:
		return false, nil
	case err == nil:
		return false, errLoginDeclared
	case !errors.Is(err, pgx.ErrNoRows):
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_login_issuers(instance_id,login_issuer,subject_mode) VALUES($1,$2,$3)`, instanceID, loginIssuer, mode); err != nil {
		return false, constraint(err)
	}
	if err = identity.Audit(ctx, tx, fresh, "bloem.instance.login_issuer.declare", "bloem_instance", instanceID, withAudit(audit, map[string]any{"login_issuer": loginIssuer, "subject_mode": mode})); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// RemoveLoginIssuer stops new claims and links through loginIssuer. Existing claims
// and sign-ins stay.
func (s Store) RemoveLoginIssuer(ctx context.Context, a identity.Actor, instanceID, loginIssuer string, audit map[string]any) error {
	tx, fresh, err := s.adminTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockInstanceByID(ctx, tx, instanceID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM bloem_login_issuers WHERE instance_id=$1 AND login_issuer=$2`, instanceID, loginIssuer)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fault.NotFound
	}
	if err = identity.Audit(ctx, tx, fresh, "bloem.instance.login_issuer.remove", "bloem_instance", instanceID, withAudit(audit, map[string]any{"login_issuer": loginIssuer})); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// LoginIssuer summarizes one declared sign-in issuer without subjects.
type LoginIssuer struct {
	Issuer              string
	SubjectMode         string
	CreatedAt           time.Time
	SignIns             int
	SignInsMatchingMode int
}

// IdentitySummary is an operator's count-only view of an instance's identities.
type IdentitySummary struct {
	InstanceID     string
	Active         bool
	Issuer         string
	IssuerSignIns  int
	LoginIssuers   []LoginIssuer
	Accounts       int
	Inactive       int
	WithIdentity   int
	Claimable      int
	Claimed        int
	LinkedAtSetup  int
	AlreadySignsIn int
	// ClearedAfterLink counts claimed or linked accounts whose identity the Bloem
	// host has since cleared. The person keeps the account; an operator should check
	// whether the link was a mistake and, if so, revoke the claim.
	ClearedAfterLink int
}

// IdentitySummary counts managed accounts and sign-in identities for an instance.
// It returns no subjects, names or email addresses.
func (s Store) IdentitySummary(ctx context.Context, a identity.Actor, instanceID string) (IdentitySummary, error) {
	out := IdentitySummary{InstanceID: instanceID, LoginIssuers: []LoginIssuer{}}
	tx, _, err := s.adminTx(ctx, a)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT active,identity_issuer FROM bloem_instances WHERE id=$1`, instanceID).Scan(&out.Active, &out.Issuer)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	if err != nil {
		return out, err
	}
	if out.Issuer != "" {
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM oidc_identities WHERE issuer=$1`, out.Issuer).Scan(&out.IssuerSignIns); err != nil {
			return out, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT login_issuer,subject_mode,created_at FROM bloem_login_issuers WHERE instance_id=$1 ORDER BY login_issuer`, instanceID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var l LoginIssuer
		if err = rows.Scan(&l.Issuer, &l.SubjectMode, &l.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.LoginIssuers = append(out.LoginIssuers, l)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	for i := range out.LoginIssuers {
		l := &out.LoginIssuers[i]
		subjects, err := tx.Query(ctx, `SELECT subject FROM oidc_identities WHERE issuer=$1`, l.Issuer)
		if err != nil {
			return out, err
		}
		for subjects.Next() {
			var subject string
			if err = subjects.Scan(&subject); err != nil {
				subjects.Close()
				return out, err
			}
			l.SignIns++
			if subjectShape(l.SubjectMode, subject) {
				l.SignInsMatchingMode++
			}
		}
		subjects.Close()
		if err = subjects.Err(); err != nil {
			return out, err
		}
	}
	// AlreadySignsIn counts claimable accounts whose identity already signs in to some
	// Pastime account: that sign-in wins, so they will not be claimed.
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE NOT a.active),count(*) FILTER (WHERE a.identity_subject<>''),
		count(*) FILTER (WHERE a.identity_subject<>'' AND a.owns_account AND a.link_status='pending'),
		count(*) FILTER (WHERE a.claimed_at IS NOT NULL),count(*) FILTER (WHERE NOT a.owns_account),
		count(*) FILTER (WHERE a.identity_subject<>'' AND a.owns_account AND a.link_status='pending' AND EXISTS(
			SELECT 1 FROM oidc_identities o WHERE o.subject=a.identity_subject AND (o.issuer=a.identity_issuer OR o.issuer IN (SELECT login_issuer FROM bloem_login_issuers l WHERE l.instance_id=a.instance_id)))),
		count(*) FILTER (WHERE a.bound_subject<>'' AND a.identity_subject='')
		FROM bloem_accounts a WHERE a.instance_id=$1`, instanceID).
		Scan(&out.Accounts, &out.Inactive, &out.WithIdentity, &out.Claimable, &out.Claimed, &out.LinkedAtSetup, &out.AlreadySignsIn, &out.ClearedAfterLink)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

type claimCandidate struct {
	instance, source, issuer, mode, status string
	user                                   int64
	active, owns                           bool
}

func refuse(reason string, k claimCandidate) error {
	return &identity.ClaimConflict{Reason: reason, InstanceID: k.instance, SourceAccountID: k.source, UserID: k.user}
}

// ClaimForLogin is identity.ManagedClaim. A verified sign-in claims the managed
// account whose identity equals it in an active instance: the sign-in issuer is the
// instance's identity issuer, or a declared equivalent whose subject mode the subject
// matches. The claim succeeds only for an active, unclaimed account the integration
// created for a member without a password or another sign-in; any other single match
// is refused with its first failing reason, and so are several matches. Email is
// never compared. Rows lock in the account, then user order.
func (s Store) ClaimForLogin(ctx context.Context, tx pgx.Tx, c identity.OIDCClaims) (int64, bool, error) {
	if c.Issuer == "" || c.Subject == "" {
		return 0, false, nil
	}
	rows, err := tx.Query(ctx, `SELECT a.instance_id,a.source_account_id,a.account_id,a.active,a.owns_account,a.link_status,i.identity_issuer,
			CASE WHEN i.identity_issuer=$1 THEN '' ELSE l.subject_mode END
		FROM bloem_accounts a
		JOIN bloem_instances i ON i.id=a.instance_id
		LEFT JOIN bloem_login_issuers l ON l.instance_id=a.instance_id AND l.login_issuer=$1
		WHERE a.identity_subject=$2 AND a.identity_issuer=i.identity_issuer AND i.identity_issuer<>'' AND i.active
			AND (i.identity_issuer=$1 OR l.login_issuer IS NOT NULL)
		ORDER BY a.instance_id,a.source_account_id
		FOR UPDATE OF a FOR SHARE OF i`, c.Issuer, c.Subject)
	if err != nil {
		return 0, false, err
	}
	var found []claimCandidate
	for rows.Next() {
		var k claimCandidate
		if err = rows.Scan(&k.instance, &k.source, &k.user, &k.active, &k.owns, &k.status, &k.issuer, &k.mode); err != nil {
			rows.Close()
			return 0, false, err
		}
		if k.mode == "" || subjectShape(k.mode, c.Subject) {
			found = append(found, k)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, false, err
	}
	if len(found) == 0 {
		return 0, false, nil
	}
	if len(found) > 1 {
		return 0, false, &identity.ClaimConflict{Reason: "multiple_matches"}
	}
	k := found[0]
	if k.mode != "" {
		// The locked instance row keeps declarations stable from here on; recheck the
		// one the query read in case a removal committed while it waited.
		var mode string
		err = tx.QueryRow(ctx, `SELECT subject_mode FROM bloem_login_issuers WHERE instance_id=$1 AND login_issuer=$2`, k.instance, c.Issuer).Scan(&mode)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && mode != k.mode {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
	}
	if !k.active {
		return 0, false, refuse("source_inactive", k)
	}
	if !k.owns || k.status != "pending" {
		return 0, false, refuse("not_pending", k)
	}
	var active, hasPassword, hasIdentity bool
	var role, username string
	err = tx.QueryRow(ctx, `SELECT active,role,password_hash<>'',username FROM users WHERE id=$1 FOR UPDATE`, k.user).Scan(&active, &role, &hasPassword, &username)
	if err != nil {
		return 0, false, err
	}
	switch {
	case !active:
		return 0, false, refuse("disabled", k)
	case role != "member":
		return 0, false, refuse("privileged", k)
	case hasPassword:
		return 0, false, refuse("has_password", k)
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM oidc_identities WHERE user_id=$1)`, k.user).Scan(&hasIdentity); err != nil {
		return 0, false, err
	}
	if hasIdentity {
		return 0, false, refuse("has_identity", k)
	}
	// The claim binds the account to the identity it was claimed under.
	if _, err = tx.Exec(ctx, `UPDATE bloem_accounts SET link_status='linked',claimed_at=clock_timestamp(),bound_issuer=identity_issuer,bound_subject=identity_subject WHERE instance_id=$1 AND source_account_id=$2`, k.instance, k.source); err != nil {
		return 0, false, err
	}
	actor := identity.Actor{Kind: "oidc", User: identity.User{ID: k.user, Username: username}}
	if err = identity.Audit(ctx, tx, actor, "bloem.account.claim", "user", strconv.FormatInt(k.user, 10), map[string]any{"instance_id": k.instance, "source_account_id": k.source, "login_issuer": c.Issuer, "instance_issuer": k.issuer}); err != nil {
		return 0, false, err
	}
	return k.user, true, nil
}

// RevokedClaim describes a revoked claim with counts only.
type RevokedClaim struct {
	UserID   int64
	Username string
	identity.AccessRemoved
}

// RevokeClaim undoes a sign-in claim, for an account claimed by the wrong person or
// one whose Bloem link was corrected. The Pastime account keeps its tracked history
// and profiles but loses every way in and every place its activity was sent (see
// identity.WithdrawAccess), and returns to an unclaimed, pending account without an
// identity. The next account revision attaches whatever identity the Bloem host then
// holds, and that person can claim it, so correct the link in Bloem first. Only an
// administrator revokes, only a claim (a link to an existing member is not undone
// here), and never for an owner or administrator account. The audit event records
// identifiers and counts, never issuers or subjects. Locks follow the sign-in order:
// the account lock, then the managed account row, then the user row.
func (s Store) RevokeClaim(ctx context.Context, a identity.Actor, instanceID, sourceAccountID string, audit map[string]any) (RevokedClaim, error) {
	var out RevokedClaim
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = identity.LockAccounts(ctx, tx); err != nil {
		return out, err
	}
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "")
	if err != nil {
		return out, err
	}
	if !fresh.IsAdmin() {
		return out, fault.Forbidden
	}
	var claimed bool
	err = tx.QueryRow(ctx, `SELECT account_id,claimed_at IS NOT NULL FROM bloem_accounts WHERE instance_id=$1 AND source_account_id=$2 FOR UPDATE`, instanceID, sourceAccountID).Scan(&out.UserID, &claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	if err != nil {
		return out, err
	}
	if !claimed {
		return out, errNotClaimed
	}
	var role string
	if err = tx.QueryRow(ctx, `SELECT role,username FROM users WHERE id=$1 FOR UPDATE`, out.UserID).Scan(&role, &out.Username); err != nil {
		return out, err
	}
	if role != "member" {
		return out, errClaimPrivileged
	}
	if out.AccessRemoved, err = identity.WithdrawAccess(ctx, tx, out.UserID); err != nil {
		return out, err
	}
	// The source fingerprint stays, so replaying the delivered revision changes
	// nothing and the removed identity returns only with a newer revision.
	if _, err = tx.Exec(ctx, `UPDATE bloem_accounts SET link_status='pending',claimed_at=NULL,identity_issuer='',identity_subject='',bound_issuer='',bound_subject='' WHERE instance_id=$1 AND source_account_id=$2`, instanceID, sourceAccountID); err != nil {
		return out, err
	}
	r := out.AccessRemoved
	if err = identity.Audit(ctx, tx, fresh, "bloem.account.unclaim", "user", strconv.FormatInt(out.UserID, 10), withAudit(audit, map[string]any{
		"instance_id": instanceID, "source_account_id": sourceAccountID, "sign_ins_removed": r.SignIns, "password_cleared": r.Password,
		"authenticator_removed": r.Authenticator, "public_profile_disabled": r.PublicProfile, "integrations_removed": r.Integrations,
	})); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// linkCandidate finds the one existing member a new managed account may link to: a
// sign-in under the instance's identity issuer, or under a declared equivalent with a
// subject of its declared shape, belonging to an active member not yet mapped in the
// instance. Owners and administrators are never linked, and neither is anyone when
// two members match.
func linkCandidate(ctx context.Context, tx pgx.Tx, instance, issuer, subject string) (int64, bool, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT o.user_id,CASE WHEN o.issuer=$2 THEN '' ELSE l.subject_mode END
		FROM oidc_identities o
		JOIN users u ON u.id=o.user_id AND u.active AND u.role='member'
		LEFT JOIN bloem_login_issuers l ON l.instance_id=$1 AND l.login_issuer=o.issuer
		WHERE o.subject=$3 AND (o.issuer=$2 OR l.login_issuer IS NOT NULL)
			AND NOT EXISTS(SELECT 1 FROM bloem_accounts b WHERE b.instance_id=$1 AND b.account_id=o.user_id)`, instance, issuer, subject)
	if err != nil {
		return 0, false, err
	}
	users := map[int64]bool{}
	for rows.Next() {
		var user int64
		var mode string
		if err = rows.Scan(&user, &mode); err != nil {
			rows.Close()
			return 0, false, err
		}
		if mode == "" || subjectShape(mode, subject) {
			users[user] = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(users) != 1 {
		return 0, false, err
	}
	var user int64
	for id := range users {
		user = id
	}
	// Recheck under the lock that keeps the member's role and state until commit.
	var exists int
	err = tx.QueryRow(ctx, `SELECT 1 FROM users WHERE id=$1 AND active AND role='member' FOR SHARE`, user).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return user, err == nil, err
}
