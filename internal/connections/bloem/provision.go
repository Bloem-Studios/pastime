package bloem

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// Provision never treats email/name as evidence of account ownership. A new account
// links only to an existing member's verified sign-in, under this instance's granted
// issuer or a declared equivalent (see linkCandidate); otherwise it stays pending
// until its person claims it by signing in. A newer revision may attach or clear the
// identity; changing it to another identity needs an unclaimed, integration-owned
// account. An account handed to a person by a claim or a link stays bound to the
// identity it was handed over under: a clear is accepted, but no other identity is,
// until an operator revokes the claim. An identity belongs to at most one account per
// instance.
func (s Store) Provision(ctx context.Context, raw string, in ProvisionInput) (ProvisionResult, error) {
	var out ProvisionResult
	if err := validate(in); err != nil {
		return out, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var instance, issuer string
	err = tx.QueryRow(ctx, `SELECT id,identity_issuer FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance, &issuer)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.Unauthenticated
	}
	if err != nil {
		return out, err
	}
	if instance != in.InstanceID {
		return out, fault.Forbidden
	}
	if in.IdentityIssuer != "" && (issuer == "" || in.IdentityIssuer != issuer) {
		return out, errIdentityMismatch
	}
	// Stable entity lock serializes first provision and subsequent revision checks.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.bloem.account.'||$1||':'||$2,0))`, instance, in.AccountID); err != nil {
		return out, err
	}
	accountPrint := fingerprint(struct {
		Default, Name, Email, Issuer, Subject string
		Active                                bool
	}{in.DefaultProfileID, in.AccountName, in.Email, in.IdentityIssuer, in.IdentitySubject, in.AccountActive})
	profilePrint := fingerprint(struct {
		Name   string
		Active bool
	}{in.Name, in.Active})
	var revision int64
	var oldPrint []byte
	var owns bool
	var oldIssuer, oldSubject, boundIssuer, boundSubject, defaultSource string
	err = tx.QueryRow(ctx, `SELECT account_id,source_revision,source_fingerprint,link_status,owns_account,identity_issuer,identity_subject,bound_issuer,bound_subject,default_source_profile_id FROM bloem_accounts WHERE instance_id=$1 AND source_account_id=$2 FOR UPDATE`, instance, in.AccountID).Scan(&out.AccountID, &revision, &oldPrint, &out.LinkStatus, &owns, &oldIssuer, &oldSubject, &boundIssuer, &boundSubject, &defaultSource)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out.LinkStatus = "pending"
		owns = true
		if in.IdentityIssuer != "" {
			linked, ok, err := linkCandidate(ctx, tx, instance, issuer, in.IdentitySubject)
			if err != nil {
				return out, err
			}
			if ok {
				out.AccountID, out.LinkStatus, owns = linked, "linked", false
			}
		}
		if owns {
			// Opaque source-derived username is a stable label, never a login proof.
			username := fmt.Sprintf("bloem_%x", fingerprint([]string{instance, in.AccountID})[:16])
			err = tx.QueryRow(ctx, `INSERT INTO users(username,display_name,email,password_hash,role) VALUES($1,$2,$3,'','member') RETURNING id`, username, in.AccountName, in.Email).Scan(&out.AccountID)
			if err != nil {
				return out, constraint(err)
			}
		}
		if !owns {
			// A link hands the member's own account over under this identity.
			boundIssuer, boundSubject = in.IdentityIssuer, in.IdentitySubject
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_accounts(instance_id,source_account_id,account_id,default_source_profile_id,source_revision,source_fingerprint,active,link_status,identity_issuer,identity_subject,owns_account,bound_issuer,bound_subject) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, instance, in.AccountID, out.AccountID, in.DefaultProfileID, int64(in.AccountRevision), accountPrint, in.AccountActive, out.LinkStatus, in.IdentityIssuer, in.IdentitySubject, owns, boundIssuer, boundSubject)
		if err != nil {
			return out, identityConstraint(err)
		}
		var defaultID int64
		err = tx.QueryRow(ctx, `SELECT id FROM tracking_profiles WHERE account_id=$1 AND is_default FOR UPDATE`, out.AccountID).Scan(&defaultID)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_profiles(instance_id,source_account_id,source_profile_id,account_id,profile_id,owns_profile) VALUES($1,$2,$3,$4,$5,$6)`, instance, in.AccountID, in.DefaultProfileID, out.AccountID, defaultID, owns)
		if err != nil {
			return out, constraint(err)
		}
	case err != nil:
		return out, err
	default:
		if in.AccountRevision < uint64(revision) || in.AccountRevision == uint64(revision) && !bytes.Equal(accountPrint, oldPrint) {
			return out, fault.Conflict
		}
		if defaultSource != in.DefaultProfileID {
			// Reassigning the account's main context needs an explicit reconciliation,
			// rather than repointing a populated default library on a partial update.
			return out, fault.Conflict
		}
		// An equal revision has an equal fingerprint, so the identity changes only here.
		if in.AccountRevision > uint64(revision) {
			attach := in.IdentitySubject != ""
			retarget := attach && oldSubject != "" && (oldIssuer != in.IdentityIssuer || oldSubject != in.IdentitySubject)
			rebind := attach && boundSubject != "" && (boundIssuer != in.IdentityIssuer || boundSubject != in.IdentitySubject)
			if rebind || retarget && (!owns || out.LinkStatus != "pending") {
				// A linked or claimed account keeps its person: it may lose its identity,
				// but only the identity it was handed over under may come back. Checking
				// the binding rather than the current identity stops a clear followed by
				// another link from moving someone else's tracking into the account.
				return out, errIdentityConflict
			}
			_, err = tx.Exec(ctx, `UPDATE bloem_accounts SET source_revision=$3,source_fingerprint=$4,active=$5,identity_issuer=$6,identity_subject=$7 WHERE instance_id=$1 AND source_account_id=$2`, instance, in.AccountID, int64(in.AccountRevision), accountPrint, in.AccountActive, in.IdentityIssuer, in.IdentitySubject)
			if err != nil {
				return out, identityConstraint(err)
			}
			if !in.AccountActive {
				// Keys created on the Connect Silo page are their owner's to revoke.
				_, err = tx.Exec(ctx, `UPDATE bloem_sync_credentials c SET revoked_at=LEAST(c.revoked_at,clock_timestamp()) FROM bloem_profiles p WHERE c.instance_id=p.instance_id AND c.profile_id=p.profile_id AND p.instance_id=$1 AND p.source_account_id=$2 AND p.origin='provisioned'`, instance, in.AccountID)
				if err != nil {
					return out, err
				}
			}
		}
	}
	var profileRevision int64
	var oldProfilePrint []byte
	var excluded, ownsProfile bool
	err = tx.QueryRow(ctx, `SELECT profile_id,source_revision,source_fingerprint,excluded,owns_profile FROM bloem_profiles WHERE instance_id=$1 AND source_account_id=$2 AND source_profile_id=$3 FOR UPDATE`, instance, in.AccountID, in.ProfileID).Scan(&out.ProfileID, &profileRevision, &oldProfilePrint, &excluded, &ownsProfile)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO tracking_profiles(account_id,name) VALUES($1,$2) RETURNING id`, out.AccountID, in.Name).Scan(&out.ProfileID)
		if err != nil {
			return out, err
		}
		ownsProfile = true
		_, err = tx.Exec(ctx, `INSERT INTO bloem_profiles(instance_id,source_account_id,source_profile_id,account_id,profile_id,owns_profile) VALUES($1,$2,$3,$4,$5,true)`, instance, in.AccountID, in.ProfileID, out.AccountID, out.ProfileID)
		if err != nil {
			return out, constraint(err)
		}
	} else if err != nil {
		return out, err
	}
	if in.Revision < uint64(profileRevision) || in.Revision == uint64(profileRevision) && !bytes.Equal(profilePrint, oldProfilePrint) {
		return out, fault.Conflict
	}
	if in.Revision > uint64(profileRevision) {
		_, err = tx.Exec(ctx, `UPDATE bloem_profiles SET source_revision=$4,source_fingerprint=$5,active=$6 WHERE instance_id=$1 AND source_account_id=$2 AND source_profile_id=$3`, instance, in.AccountID, in.ProfileID, int64(in.Revision), profilePrint, in.Active)
		if err != nil {
			return out, err
		}
		// The default and linked profiles retain independent browser/login eligibility.
		if !in.Active {
			_, err = tx.Exec(ctx, `UPDATE bloem_sync_credentials SET revoked_at=LEAST(revoked_at,clock_timestamp()) WHERE instance_id=$1 AND profile_id=$2`, instance, out.ProfileID)
			if err != nil {
				return out, err
			}
		}
		// Archival of integration-owned additional profiles preserves their history.
		_, err = tx.Exec(ctx, `UPDATE tracking_profiles SET name=$2,archived_at=CASE WHEN is_default OR NOT $4::boolean THEN archived_at WHEN $3::boolean THEN NULL ELSE coalesce(archived_at,clock_timestamp()) END WHERE id=$1`, out.ProfileID, in.Name, in.Active, ownsProfile)
		if err != nil {
			return out, err
		}
	}
	// Provisioning is complete, but connection readiness requires durable credential
	// issuance/enrollment and is never inferred from a mapping alone.
	out.ConnectionReady = false
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
