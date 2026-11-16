package bloem

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"time"
)

type AvailabilityView struct {
	CatalogID int64          `json:"catalog_id,string"`
	Status    string         `json:"status"`
	Handoff   *HandoffTarget `json:"handoff,omitempty"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
}
type AvailabilityRequest struct {
	RequestID  string `json:"request_id"`
	InstanceID string `json:"instance_id"`
	AccountID  string `json:"account_id"`
	ProfileID  string `json:"profile_id"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	ExternalID string `json:"external_id"`
}
type AvailabilityResult struct {
	RequestID string `json:"request_id"`
	Available bool   `json:"available"`
	ContentID string `json:"content_id"`
}
type AvailabilityInput struct {
	Version    int                  `json:"version"`
	InstanceID string               `json:"instance_id"`
	AccountID  string               `json:"account_id"`
	ProfileID  string               `json:"profile_id"`
	CheckedAt  time.Time            `json:"checked_at"`
	ExpiresAt  time.Time            `json:"expires_at"`
	Results    []AvailabilityResult `json:"results"`
}

func (s Store) Availability(ctx context.Context, a identity.Actor, ids []int64) ([]AvailabilityView, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, fault.Invalid("Choose between one and one hundred titles.")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id < 1 || seen[id] {
			return nil, fault.Invalid("Invalid title selection.")
		}
		seen[id] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:read")
	if err != nil {
		return nil, err
	}
	// Only mappings belonging to the current profile can warm or read this cache.
	rows, err := tx.Query(ctx, `SELECT i.id,i.handoff_enabled,i.handoff_label,COALESCE(i.handoff_base_url,i.url),c.id FROM bloem_profiles p
 JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active
 JOIN bloem_instances i ON i.id=p.instance_id AND i.active
 JOIN catalog_items c ON c.id=ANY($2::bigint[]) AND c.kind IN('movie','series') AND c.provider IN('tmdb','imdb','tvdb')
 WHERE p.profile_id=$1 AND p.active AND NOT p.excluded ORDER BY i.id,c.id LIMIT 101`, fresh.TrackingID(), ids)
	if err != nil {
		return nil, err
	}
	type row struct {
		instance string
		settings HandoffSettings
		catalog  int64
	}
	matches := []row{}
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.instance, &r.settings.Enabled, &r.settings.Label, &r.settings.BaseURL, &r.catalog); err != nil {
			rows.Close()
			return nil, err
		}
		matches = append(matches, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(matches) > 100 {
		return nil, fault.Invalid("Too many connected title lookups.")
	}
	out := []AvailabilityView{}
	for _, r := range matches {
		requestID, e := randomRaw("availability_")
		if e != nil {
			return nil, e
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_availability(instance_id,profile_id,catalog_id,request_id) VALUES($1,$2,$3,$4) ON CONFLICT(instance_id,profile_id,catalog_id) DO UPDATE SET request_id=EXCLUDED.request_id,requested_at=clock_timestamp(),pending=true WHERE (NOT bloem_availability.pending AND bloem_availability.expires_at<=clock_timestamp()+interval '60 seconds') OR (bloem_availability.pending AND bloem_availability.requested_at<clock_timestamp()-interval '5 minutes')`, r.instance, fresh.TrackingID(), r.catalog, requestID)
		if err != nil {
			return nil, err
		}
		var available, current bool
		var content string
		var expires *time.Time
		err = tx.QueryRow(ctx, `SELECT available,COALESCE(expires_at>clock_timestamp(),false),content_id,expires_at FROM bloem_availability WHERE instance_id=$1 AND profile_id=$2 AND catalog_id=$3`, r.instance, fresh.TrackingID(), r.catalog).Scan(&available, &current, &content, &expires)
		if err != nil {
			return nil, err
		}
		v := AvailabilityView{CatalogID: r.catalog, Status: "checking"}
		if current {
			v.ExpiresAt = expires
			v.Status = "unavailable"
			if available {
				v.Status = "available"
				v.Handoff = handoffTarget(r.settings, content)
			}
		}
		out = append(out, v)
	}
	return out, tx.Commit(ctx)
}
func (s Store) PendingAvailability(ctx context.Context, raw string) ([]AvailabilityRequest, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var instance string
	if err = tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance); err != nil {
		return nil, authorityError(err)
	}
	rows, err := tx.Query(ctx, `SELECT a.request_id,a.instance_id,p.source_account_id,p.source_profile_id,c.kind,c.provider,c.provider_id FROM bloem_availability a
 JOIN bloem_profiles p ON p.instance_id=a.instance_id AND p.profile_id=a.profile_id AND p.active AND NOT p.excluded
 JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active
 JOIN users u ON u.id=p.account_id AND u.active AND pastime_account_has_service_access(u.id)
 JOIN catalog_items c ON c.id=a.catalog_id WHERE a.instance_id=$1 AND a.pending AND a.requested_at>clock_timestamp()-interval '5 minutes' ORDER BY a.requested_at,a.request_id LIMIT 100`, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AvailabilityRequest{}
	for rows.Next() {
		var r AvailabilityRequest
		if err = rows.Scan(&r.RequestID, &r.InstanceID, &r.AccountID, &r.ProfileID, &r.Kind, &r.Namespace, &r.ExternalID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s Store) AcceptAvailability(ctx context.Context, raw string, in AvailabilityInput) error {
	if in.Version != 1 || !opaque(in.InstanceID) || !opaque(in.AccountID) || !opaque(in.ProfileID) || in.CheckedAt.IsZero() || !in.ExpiresAt.After(in.CheckedAt) || in.ExpiresAt.Sub(in.CheckedAt) > 120*time.Second || len(in.Results) == 0 || len(in.Results) > 100 {
		return fault.Invalid("Invalid availability response.")
	}
	seen := map[string]bool{}
	for _, r := range in.Results {
		if !opaque(r.RequestID) || seen[r.RequestID] || (r.Available && !opaque(r.ContentID)) || (!r.Available && r.ContentID != "") {
			return fault.Invalid("Invalid availability result.")
		}
		seen[r.RequestID] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	binding, err := calendarBinding(ctx, tx, raw, in.InstanceID, in.AccountID, in.ProfileID)
	if err != nil {
		return err
	}
	if _, err = lockSourceProfile(ctx, tx, in.InstanceID, binding.ProfileID, false); err != nil {
		return err
	}
	if err = validCalendarLease(ctx, tx, in.ExpiresAt); err != nil {
		return err
	}
	var timely bool
	if err = tx.QueryRow(ctx, `SELECT $1::timestamptz<=clock_timestamp()+interval '5 seconds' AND $1::timestamptz>clock_timestamp()-interval '2 minutes'`, in.CheckedAt).Scan(&timely); err != nil {
		return err
	}
	if !timely {
		return fault.Invalid("Availability response is stale.")
	}
	for _, r := range in.Results {
		tag, e := tx.Exec(ctx, `UPDATE bloem_availability SET pending=false,checked_at=$4,expires_at=$5,available=$6,content_id=$7 WHERE instance_id=$1 AND profile_id=$2 AND request_id=$3 AND requested_at<=$4::timestamptz+interval '5 seconds' AND (checked_at IS NULL OR checked_at<$4 OR (checked_at=$4 AND expires_at=$5 AND available=$6 AND content_id=$7))`, in.InstanceID, binding.ProfileID, r.RequestID, in.CheckedAt, in.ExpiresAt, r.Available, r.ContentID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return fault.Conflict
		}
	}
	return tx.Commit(ctx)
}
