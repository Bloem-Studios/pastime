package bloem

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// The Connect Silo page lets a signed-in person create the sync credential for one of
// their own tracking profiles and paste it into Silo (Settings, Watch Providers). It replaces
// machine provisioning for profiles that plugin 0.1.14 never enrolled.

var (
	ErrSiloNotRegistered = &fault.Error{Status: 409, Code: "silo_not_registered", Message: "No Silo server is set up for this Pastime yet. Ask the Pastime administrator."}
	ErrProfileExcluded   = &fault.Error{Status: 409, Code: "profile_excluded", Message: "This profile is excluded from Silo sync."}
	// ErrSiloTurnedOff refuses a binding or source account that Silo provisioning turned
	// off (a disabled Silo account or a removed Silo profile): a person's own key must
	// not undo that decision, and an active source account can count toward tracking
	// access.
	ErrSiloTurnedOff = &fault.Error{Status: 409, Code: "silo_account_inactive", Message: "Silo turned off sync for this profile. Ask the Pastime administrator."}
)

// selfServiceSource reports a source account the Connect Silo page created. Silo's own
// account IDs are decimal, so they never carry this prefix.
func selfServiceSource(sourceAccountID string) bool {
	return strings.HasPrefix(sourceAccountID, "pastime-account-")
}

// ConnectView is the Connect Silo page: the person's active tracking profiles and
// whether each has a current Silo key.
type ConnectView struct {
	Available     bool             `json:"available"`
	ServiceAccess bool             `json:"service_access"`
	SiloURL       string           `json:"silo_url"`
	Profiles      []ConnectProfile `json:"profiles"`
}
type ConnectProfile struct {
	ProfileID int64      `json:"profile_id,string"`
	Name      string     `json:"name"`
	Connected bool       `json:"connected"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// SelfServiceCredential is a newly created Silo key, revealed once.
type SelfServiceCredential struct {
	Credential  string    `json:"credential"`
	ExpiresAt   time.Time `json:"expires_at"`
	ProfileName string    `json:"profile_name"`
}

// currentKeys selects the usable sync credentials of a tracking profile: an active
// instance, an active and included binding of an active source account, and a current
// unexpired credential.
const currentKeys = `FROM bloem_profiles p
	JOIN bloem_instances i ON i.id=p.instance_id AND i.active
	JOIN bloem_accounts a ON a.instance_id=p.instance_id AND a.source_account_id=p.source_account_id AND a.active
	JOIN bloem_sync_credentials c ON c.instance_id=p.instance_id AND c.profile_id=p.profile_id AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp()
	WHERE p.profile_id=t.id AND p.active AND NOT p.excluded`

func (s Store) ConnectView(ctx context.Context, a identity.Actor) (ConnectView, error) {
	out := ConnectView{Profiles: []ConnectProfile{}}
	account := a.User.ID
	// The household sees every profile; anyone else only their own.
	bound := int64(0)
	if !a.Household() {
		if bound = a.TrackingID(); bound == 0 {
			return out, fault.Forbidden
		}
	}
	var instances int
	var url *string
	err := s.Pool.QueryRow(ctx, `SELECT count(*),min(url),pastime_account_has_service_access($1) FROM bloem_instances WHERE active`, account).Scan(&instances, &url, &out.ServiceAccess)
	if err != nil {
		return out, err
	}
	out.Available = instances > 0 && s.Box.Available()
	if instances == 1 && url != nil {
		out.SiloURL = *url
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id,t.name,(SELECT max(c.expires_at) `+currentKeys+`)
		FROM tracking_profiles t WHERE t.account_id=$1 AND t.archived_at IS NULL AND ($2::bigint=0 OR t.id=$2) ORDER BY t.is_default DESC,t.id`, account, bound)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ConnectProfile
		if err = rows.Scan(&p.ProfileID, &p.Name, &p.ExpiresAt); err != nil {
			return out, err
		}
		p.Connected = p.ExpiresAt != nil
		out.Profiles = append(out.Profiles, p)
	}
	return out, rows.Err()
}

// IssueSelfServiceCredential creates the Silo key for one of the person's own active
// tracking profiles. An existing binding of the profile is reused, so a legacy profile
// keeps its source identifiers and the consumption IDs Silo derives from them. Without
// one, it binds the profile under the person's existing source account, or a new
// self-service account. Earlier keys of the profile stop when Silo first uses the new
// one, or after the rotation grace.
func (s Store) IssueSelfServiceCredential(ctx context.Context, a identity.Actor, profileID int64, instanceID string) (SelfServiceCredential, error) {
	var out SelfServiceCredential
	if !a.Browser() {
		return out, fault.Forbidden
	}
	if !s.Box.Available() {
		return out, fault.Unavailable
	}
	if instanceID != "" && !opaque(instanceID) {
		return out, fault.Invalid("Choose a registered Silo server.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, ""); err != nil {
		return out, err
	}
	if !a.Household() && profileID != a.TrackingID() {
		return out, fault.NotFound
	}
	account := a.User.ID
	err = tx.QueryRow(ctx, `SELECT name FROM tracking_profiles WHERE id=$1 AND account_id=$2 AND archived_at IS NULL`, profileID, account).Scan(&out.ProfileName)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	if err != nil {
		return out, err
	}
	var access bool
	if err = tx.QueryRow(ctx, `SELECT pastime_account_has_service_access($1)`, account).Scan(&access); err != nil {
		return out, err
	}
	if !access {
		return out, ErrServiceAccess
	}
	// Lifecycle rows lock in the same order as sync authentication: instance, source
	// account, binding, then credentials.
	instance, err := connectInstance(ctx, tx, instanceID)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.bloem.self.'||$1||':'||$2::bigint::text,0))`, instance, account); err != nil {
		return out, err
	}
	binding := "reused"
	var source string
	err = tx.QueryRow(ctx, `SELECT source_account_id FROM bloem_profiles WHERE instance_id=$1 AND profile_id=$2`, instance, profileID).Scan(&source)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		binding = "created"
		if source, err = selfServiceAccount(ctx, tx, instance, account); err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_profiles(instance_id,source_account_id,source_profile_id,account_id,profile_id,owns_profile,origin) VALUES($1,$2,$3,$4,$5,false,'self_service')`, instance, source, "pastime-profile-"+strconv.FormatInt(profileID, 10), account, profileID)
		if err != nil {
			return out, constraint(err)
		}
	case err != nil:
		return out, err
	default:
		// A binding or source account that only the Connect Silo page created is active
		// again when the person connects explicitly. One that Silo provisioning turned
		// off stays off, and exclusion stays.
		var accountActive bool
		if err = tx.QueryRow(ctx, `SELECT active FROM bloem_accounts WHERE instance_id=$1 AND source_account_id=$2 FOR UPDATE`, instance, source).Scan(&accountActive); err != nil {
			return out, err
		}
		var excluded, active bool
		var origin string
		if err = tx.QueryRow(ctx, `SELECT excluded,active,origin FROM bloem_profiles WHERE instance_id=$1 AND profile_id=$2 FOR UPDATE`, instance, profileID).Scan(&excluded, &active, &origin); err != nil {
			return out, err
		}
		if excluded {
			return out, ErrProfileExcluded
		}
		if !accountActive && !selfServiceSource(source) || !active && origin != "self_service" {
			return out, ErrSiloTurnedOff
		}
		if !accountActive {
			if _, err = tx.Exec(ctx, `UPDATE bloem_accounts SET active=true WHERE instance_id=$1 AND source_account_id=$2`, instance, source); err != nil {
				return out, err
			}
		}
		if !active {
			if _, err = tx.Exec(ctx, `UPDATE bloem_profiles SET active=true WHERE instance_id=$1 AND profile_id=$2`, instance, profileID); err != nil {
				return out, err
			}
		}
	}
	var held int
	if err = tx.QueryRow(ctx, `SELECT 1 FROM tracking_profiles WHERE id=$1 AND account_id=$2 AND archived_at IS NULL FOR SHARE`, profileID, account).Scan(&held); err != nil {
		return out, authorityError(err)
	}
	var issuance string
	if err = tx.QueryRow(ctx, `SELECT 'self:'||gen_random_uuid()::text`).Scan(&issuance); err != nil {
		return out, err
	}
	// Nothing reveals a Connect Silo key again, so only its digest is kept.
	issued, err := s.mintSyncCredentialTx(ctx, tx, instance, profileID, issuance, false)
	if err != nil {
		return out, err
	}
	if err = identity.Audit(ctx, tx, a, "silo.credential.create", "tracking_profile", strconv.FormatInt(profileID, 10), map[string]any{"instance_id": instance, "binding": binding}); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SelfServiceCredential{}, err
	}
	out.Credential, out.ExpiresAt = issued.Credential, issued.ExpiresAt
	return out, nil
}

// connectInstance locks the Silo instance to connect to: the named one, or the only
// active one.
func connectInstance(ctx context.Context, tx pgx.Tx, named string) (string, error) {
	if named != "" {
		var id string
		err := tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE id=$1 AND active FOR SHARE`, named).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrSiloNotRegistered
		}
		return id, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM bloem_instances WHERE active ORDER BY id LIMIT 2 FOR SHARE`)
	if err != nil {
		return "", err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return "", err
	}
	switch len(ids) {
	case 0:
		return "", ErrSiloNotRegistered
	case 1:
		return ids[0], nil
	}
	return "", fault.Invalid("Choose the Silo server to connect this profile to.")
}

// selfServiceAccount returns the person's source account in the instance: the one
// provisioning created or linked, or a self-service one, reactivated only when the
// Connect Silo page created it; or a new self-service one. Its pastime- prefixed source
// ID cannot collide with Silo's decimal account IDs.
func selfServiceAccount(ctx context.Context, tx pgx.Tx, instance string, account int64) (string, error) {
	var source string
	var active bool
	err := tx.QueryRow(ctx, `SELECT source_account_id,active FROM bloem_accounts WHERE instance_id=$1 AND account_id=$2 FOR UPDATE`, instance, account).Scan(&source, &active)
	if err == nil {
		if !active {
			if !selfServiceSource(source) {
				return "", ErrSiloTurnedOff
			}
			_, err = tx.Exec(ctx, `UPDATE bloem_accounts SET active=true WHERE instance_id=$1 AND source_account_id=$2`, instance, source)
		}
		return source, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var defaultProfile int64
	if err = tx.QueryRow(ctx, `SELECT id FROM tracking_profiles WHERE account_id=$1 AND is_default`, account).Scan(&defaultProfile); err != nil {
		return "", err
	}
	source = "pastime-account-" + strconv.FormatInt(account, 10)
	accountPrint := fingerprint(struct {
		Origin  string
		Account int64
	}{"self_service", account})
	_, err = tx.Exec(ctx, `INSERT INTO bloem_accounts(instance_id,source_account_id,account_id,default_source_profile_id,source_revision,source_fingerprint,active,link_status,owns_account) VALUES($1,$2,$3,$4,1,$5,true,'linked',false)`,
		instance, source, account, fmt.Sprintf("pastime-profile-%d", defaultProfile), accountPrint)
	return source, constraint(err)
}

// RevokeSelfServiceCredentials stops every Silo key of one of the person's own tracking
// profiles at once. Silo then reports that the key is no longer accepted.
func (s Store) RevokeSelfServiceCredentials(ctx context.Context, a identity.Actor, profileID int64) error {
	if !a.Browser() {
		return fault.Forbidden
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, ""); err != nil {
		return err
	}
	if !a.Household() && profileID != a.TrackingID() {
		return fault.NotFound
	}
	var owned int
	err = tx.QueryRow(ctx, `SELECT 1 FROM tracking_profiles WHERE id=$1 AND account_id=$2`, profileID, a.User.ID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_sync_credentials SET revoked_at=LEAST(coalesce(revoked_at,clock_timestamp()),clock_timestamp()) WHERE profile_id=$1 AND (revoked_at IS NULL OR revoked_at>clock_timestamp())`, profileID)
	if err != nil {
		return err
	}
	if err = identity.Audit(ctx, tx, a, "silo.credential.revoke", "tracking_profile", strconv.FormatInt(profileID, 10), map[string]any{"revoked": tag.RowsAffected()}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
