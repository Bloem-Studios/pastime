package bloem

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

type CredentialResult struct {
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// credentialRotationGrace bounds how long a superseded profile credential keeps
// working while the host has not yet used its successor. The host commits a new
// credential only after issuance returns, so revoking at issue time would strand
// it whenever that commit fails.
const credentialRotationGrace = 24 * time.Hour

// SyncAuthority is source-bound machine authority, never a browser actor.
type SyncAuthority struct {
	InstanceID      string
	SourceAccountID string
	SourceProfileID string
	AccountID       int64
	ProfileID       int64
	CredentialID    string
}

func (s Store) IssueCredential(ctx context.Context, raw string, profileID int64, issuanceID string) (CredentialResult, error) {
	var out CredentialResult
	if profileID < 1 || !opaque(issuanceID) {
		return out, fault.Invalid("Invalid managed credential request.")
	}
	if !s.Box.Available() {
		return out, fault.Unavailable
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var instance string
	err = tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.Unauthenticated
	}
	if err != nil {
		return out, err
	}
	if _, err = lockSourceProfile(ctx, tx, instance, profileID, true); err != nil {
		return out, err
	}
	var ciphertext []byte
	var active bool
	err = tx.QueryRow(ctx, `SELECT credential_ciphertext,expires_at,revoked_at IS NULL AND expires_at>clock_timestamp() FROM bloem_sync_credentials WHERE instance_id=$1 AND profile_id=$2 AND issuance_id=$3 FOR UPDATE`, instance, profileID, issuanceID).Scan(&ciphertext, &out.ExpiresAt, &active)
	purpose := syncCredentialPurpose(instance, profileID, issuanceID)
	if err == nil {
		if !active {
			return out, fault.Unauthenticated
		}
		plain, e := s.Box.Open(ciphertext, purpose)
		if e != nil {
			return out, e
		}
		if len(plain) == 0 {
			// A Connect Silo key: only its digest is kept, so it cannot be revealed.
			return out, fault.Conflict
		}
		out.Credential = string(plain)
		if err = tx.Commit(ctx); err != nil {
			return CredentialResult{}, err
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if out, err = s.mintSyncCredentialTx(ctx, tx, instance, profileID, issuanceID, true); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CredentialResult{}, err
	}
	return out, nil
}

func syncCredentialPurpose(instance string, profileID int64, issuanceID string) string {
	return fmt.Sprintf("bloem-sync:%x", fingerprint([]string{instance, fmt.Sprint(profileID), issuanceID}))
}

// mintSyncCredentialTx stores a new sync credential for a locked binding and supersedes
// the binding's earlier ones. The caller commits and only then reveals the credential.
// A revealable credential is stored sealed, because IssueCredential must return the
// same key again for the same issuance; any other is stored as its digest and a sealed
// empty value, so a database copy with the server secret yields no working key.
func (s Store) mintSyncCredentialTx(ctx context.Context, tx pgx.Tx, instance string, profileID int64, issuanceID string, revealable bool) (CredentialResult, error) {
	var out CredentialResult
	rawSync, err := randomRaw("ptbs_")
	if err != nil {
		return out, err
	}
	var kept []byte
	if revealable {
		kept = []byte(rawSync)
	}
	ciphertext, err := s.Box.Seal(kept, syncCredentialPurpose(instance, profileID, issuanceID))
	if err != nil {
		return out, err
	}
	// Supersede rather than revoke: earlier credentials get a bounded deadline in
	// revoked_at and stop at once when the host first uses the successor (see
	// authenticateSyncTx). LEAST never extends an earlier deadline or revocation.
	if _, err = tx.Exec(ctx, `UPDATE bloem_sync_credentials SET revoked_at=LEAST(revoked_at,clock_timestamp()+make_interval(secs=>$3::double precision)) WHERE instance_id=$1 AND profile_id=$2`, instance, profileID, credentialRotationGrace.Seconds()); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO bloem_sync_credentials(instance_id,profile_id,issuance_id,token_digest,credential_ciphertext) VALUES($1,$2,$3,$4,$5) RETURNING expires_at`, instance, profileID, issuanceID, identity.Digest(rawSync), ciphertext).Scan(&out.ExpiresAt)
	if err != nil {
		return out, err
	}
	out.Credential = rawSync
	return out, nil
}

// Lifecycle locks precede credential locks on every path. Locking a credential
// while waiting for an account disable would invert revocation's lock order.
func lockSourceProfile(ctx context.Context, tx pgx.Tx, instance string, profileID int64, exclusive bool) (SyncAuthority, error) {
	var out SyncAuthority
	out.InstanceID = instance
	out.ProfileID = profileID
	err := tx.QueryRow(ctx, `SELECT source_account_id FROM bloem_profiles WHERE instance_id=$1 AND profile_id=$2`, instance, profileID).Scan(&out.SourceAccountID)
	if err != nil {
		return out, authorityError(err)
	}
	for _, check := range []struct {
		sql  string
		args []any
	}{
		{`SELECT 1 FROM bloem_instances WHERE id=$1 AND active FOR SHARE`, []any{instance}},
		{`SELECT 1 FROM bloem_accounts WHERE instance_id=$1 AND source_account_id=$2 AND active FOR SHARE`, []any{instance, out.SourceAccountID}},
	} {
		var exists int
		err = tx.QueryRow(ctx, check.sql, check.args...).Scan(&exists)
		if err != nil {
			return out, authorityError(err)
		}
	}
	lock := " FOR SHARE"
	if exclusive {
		lock = " FOR UPDATE"
	}
	err = tx.QueryRow(ctx, `SELECT account_id,source_profile_id FROM bloem_profiles WHERE instance_id=$1 AND profile_id=$2 AND active AND NOT excluded`+lock, instance, profileID).Scan(&out.AccountID, &out.SourceProfileID)
	if err != nil {
		return out, authorityError(err)
	}
	var exists int
	err = tx.QueryRow(ctx, `SELECT 1 FROM users WHERE id=$1 AND active AND pastime_account_has_service_access(id) FOR SHARE`, out.AccountID).Scan(&exists)
	if err != nil {
		return out, authorityError(err)
	}
	err = tx.QueryRow(ctx, `SELECT 1 FROM tracking_profiles WHERE id=$1 AND account_id=$2 AND archived_at IS NULL FOR SHARE`, profileID, out.AccountID).Scan(&exists)
	return out, authorityError(err)
}
func authorityError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Unauthenticated
	}
	return err
}

// ErrServiceAccess refuses a valid sync credential whose Pastime account has no tracking
// access under the service-access policy, with the same answer sign-in gives. It is
// distinct from 401 so the host reports a permission problem, not an invalid key.
var ErrServiceAccess = identity.ErrServiceAccessRequired

// syncCredentialLifetime is how long a sync credential stays valid after use: each use
// extends the current credential, so a connection that keeps syncing never expires.
// syncCredentialRenewal is the remaining lifetime below which a use extends it, which
// bounds the writes to one per credential a week.
const (
	syncCredentialLifetime = "90 days"
	syncCredentialRenewal  = "83 days"
)

// ErrUnknownSyncKey answers a sync key Pastime never issued, with the same public 401
// as any other refused key. Callers tell it apart (errors.Is) to limit guessing without
// counting the known but revoked or expired keys a Silo server may still hold.
var ErrUnknownSyncKey = &fault.Error{Status: 401, Code: "unauthenticated", Message: "Sign in to continue."}

func authenticateSyncTx(ctx context.Context, tx pgx.Tx, raw string) (SyncAuthority, error) {
	var instance string
	var profileID int64
	err := tx.QueryRow(ctx, `SELECT instance_id,profile_id FROM bloem_sync_credentials WHERE token_digest=$1`, identity.Digest(raw)).Scan(&instance, &profileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncAuthority{}, ErrUnknownSyncKey
	}
	if err != nil {
		return SyncAuthority{}, err
	}
	out, err := lockSourceProfile(ctx, tx, instance, profileID, false)
	if errors.Is(err, fault.Unauthenticated) {
		if denied, e := serviceAccessDenied(ctx, tx, instance, profileID, raw); e != nil || denied {
			return out, firstError(e, ErrServiceAccess)
		}
	}
	if err != nil {
		return out, err
	}
	// A future revoked_at is a superseded credential's deadline. Only the current
	// credential (revoked_at IS NULL) is reissuable; see IssueCredential.
	var current bool
	err = tx.QueryRow(ctx, `SELECT id::text,revoked_at IS NULL FROM bloem_sync_credentials WHERE instance_id=$1 AND profile_id=$2 AND token_digest=$3 AND (revoked_at IS NULL OR revoked_at>clock_timestamp()) AND expires_at>clock_timestamp() FOR SHARE`, instance, profileID, identity.Digest(raw)).Scan(&out.CredentialID, &current)
	if err != nil {
		return out, authorityError(err)
	}
	if current {
		// Use of the current credential proves the host committed it, so the
		// superseded ones stop now instead of at their deadline.
		_, err = tx.Exec(ctx, `UPDATE bloem_sync_credentials SET revoked_at=clock_timestamp() WHERE instance_id=$1 AND profile_id=$2 AND revoked_at>clock_timestamp()`, instance, profileID)
		if err != nil {
			return out, err
		}
		// Rolling expiry. The host keeps no expiry or refresh token for these
		// credentials, so use renews them here. SKIP LOCKED never waits on a concurrent
		// request holding the same row: it renews on a later request instead of
		// upgrading a shared lock into a deadlock.
		_, err = tx.Exec(ctx, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()+interval '`+syncCredentialLifetime+`' WHERE id=(SELECT id FROM bloem_sync_credentials WHERE id=$1::uuid AND revoked_at IS NULL AND expires_at>clock_timestamp() AND expires_at<clock_timestamp()+interval '`+syncCredentialRenewal+`' FOR UPDATE SKIP LOCKED)`, out.CredentialID)
	}
	return out, err
}

// serviceAccessDenied reports whether a valid sync credential failed authentication
// only because its account lacks tracking access: every other source, binding,
// account and profile condition holds. Anything else stays unauthenticated.
func serviceAccessDenied(ctx context.Context, tx pgx.Tx, instance string, profileID int64, raw string) (bool, error) {
	var denied bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_sync_credentials c
		JOIN bloem_instances i ON i.id=c.instance_id AND i.active
		JOIN bloem_profiles p ON p.instance_id=c.instance_id AND p.profile_id=c.profile_id AND p.active AND NOT p.excluded
		JOIN bloem_accounts a ON a.instance_id=p.instance_id AND a.source_account_id=p.source_account_id AND a.active
		JOIN users u ON u.id=p.account_id AND u.active
		JOIN tracking_profiles t ON t.id=p.profile_id AND t.account_id=p.account_id AND t.archived_at IS NULL
		WHERE c.instance_id=$1 AND c.profile_id=$2 AND c.token_digest=$3 AND (c.revoked_at IS NULL OR c.revoked_at>clock_timestamp()) AND c.expires_at>clock_timestamp()
		AND NOT pastime_account_has_service_access(u.id))`, instance, profileID, identity.Digest(raw)).Scan(&denied)
	return denied, err
}

func firstError(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
func (s Store) AuthenticateSync(ctx context.Context, raw string) (SyncAuthority, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return SyncAuthority{}, err
	}
	defer tx.Rollback(ctx)
	out, err := authenticateSyncTx(ctx, tx, raw)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// SyncKeyCounts reports current sync credentials (not superseded or revoked) of active,
// included bindings in active instances.
type SyncKeyCounts struct {
	// ExpiringSoon counts keys that expire within the window; FirstExpiry is the earliest.
	ExpiringSoon int
	FirstExpiry  *time.Time
	// Expired counts keys that already expired while still current.
	Expired int
}

// SyncKeyExpiry counts the keys that need attention. A key Silo keeps using renews
// itself, so these belong to connections that stopped syncing.
func (s Store) SyncKeyExpiry(ctx context.Context, within time.Duration) (SyncKeyCounts, error) {
	var out SyncKeyCounts
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE c.expires_at>clock_timestamp()),
		min(c.expires_at) FILTER (WHERE c.expires_at>clock_timestamp()),
		count(*) FILTER (WHERE c.expires_at<=clock_timestamp())
		FROM bloem_sync_credentials c
		JOIN bloem_instances i ON i.id=c.instance_id AND i.active
		JOIN bloem_profiles p ON p.instance_id=c.instance_id AND p.profile_id=c.profile_id AND p.active AND NOT p.excluded
		WHERE c.revoked_at IS NULL AND c.expires_at<clock_timestamp()+make_interval(secs=>$1::double precision)`, within.Seconds()).Scan(&out.ExpiringSoon, &out.FirstExpiry, &out.Expired)
	return out, err
}
