package bloem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

type CalendarEvent struct {
	InstanceID         string            `json:"source_instance_id,omitempty"`
	AuthorityExpiresAt *time.Time        `json:"source_authority_expires_at,omitempty"`
	EventID            string            `json:"event_id"`
	ContentID          string            `json:"content_id"`
	Kind               string            `json:"kind"`
	Title              string            `json:"title"`
	EpisodeTitle       string            `json:"episode_title,omitempty"`
	SeriesID           string            `json:"series_id,omitempty"`
	SeasonNumber       *int32            `json:"season_number,omitempty"`
	EpisodeNumber      *int32            `json:"episode_number,omitempty"`
	Date               string            `json:"date"`
	AirAt              *time.Time        `json:"air_at,omitempty"`
	AirTimezone        string            `json:"air_timezone,omitempty"`
	Watched            bool              `json:"watched"`
	Followed           bool              `json:"followed"`
	Favourite          bool              `json:"favourite"`
	Watchlist          bool              `json:"watchlist"`
	ExternalIDs        map[string]string `json:"external_ids,omitempty"`
	SeriesExternalIDs  map[string]string `json:"series_external_ids,omitempty"`
	ArtworkID          string            `json:"artwork_id,omitempty"`
	Playable           bool              `json:"playable"`
	ReleaseType        string            `json:"release_type,omitempty"`
	Badges             []string          `json:"badges,omitempty"`
}
type CalendarInput struct {
	Version          int             `json:"version"`
	InstanceID       string          `json:"instance_id"`
	AccountID        string          `json:"account_id"`
	ProfileID        string          `json:"profile_id"`
	SnapshotID       string          `json:"snapshot_id"`
	Cursor           string          `json:"cursor"`
	NextCursor       string          `json:"next_cursor"`
	Complete         bool            `json:"complete"`
	Start            string          `json:"start"`
	End              string          `json:"end"`
	VisibilityDigest string          `json:"visibility_digest"`
	Revision         uint64          `json:"revision"`
	ExpiresAt        time.Time       `json:"expires_at"`
	Events           []CalendarEvent `json:"events"`
}
type CalendarReceipt struct {
	SnapshotID string `json:"snapshot_id"`
	NextCursor string `json:"next_cursor"`
	Complete   bool   `json:"complete"`
}

func calendarText(v string, max int) bool {
	return utf8.ValidString(v) && len(v) > 0 && len(v) <= max && !strings.ContainsFunc(v, unicode.IsControl)
}
func calendarDates(start, end string) (time.Time, time.Time, error) {
	a, e1 := time.Parse("2006-01-02", start)
	b, e2 := time.Parse("2006-01-02", end)
	if e1 != nil || e2 != nil || b.Before(a) || b.Sub(a) > 30*24*time.Hour {
		return a, b, fault.Invalid("Choose a calendar window of at most 31 days.")
	}
	return a, b, nil
}
func calendarOffset(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 10000 || strconv.Itoa(n) != v {
		return 0, fault.Invalid("Invalid calendar cursor.")
	}
	return n, nil
}
func eventDay(e CalendarEvent) string {
	if e.AirAt != nil {
		return e.AirAt.UTC().Format("2006-01-02")
	}
	return e.Date
}
func (in CalendarInput) validate() error {
	if in.Version != 1 || !opaque(in.InstanceID) || !opaque(in.AccountID) || !opaque(in.ProfileID) || !opaque(in.SnapshotID) || !calendarText(in.VisibilityDigest, 256) || in.Revision < 1 || in.Revision > math.MaxInt64 || len(in.Events) > 100 {
		return fault.Invalid("Invalid source calendar page.")
	}
	if _, _, err := calendarDates(in.Start, in.End); err != nil {
		return err
	}
	offset, err := calendarOffset(in.Cursor)
	if err != nil {
		return err
	}
	if in.Complete {
		if in.NextCursor != "" {
			return fault.Invalid("Final calendar page has a cursor.")
		}
	} else {
		next, err := calendarOffset(in.NextCursor)
		if err != nil || len(in.Events) == 0 || next != offset+len(in.Events) {
			return fault.Invalid("Invalid next calendar page.")
		}
	}
	if offset+len(in.Events) > 10000 {
		return fault.Invalid("Calendar window exceeds its entry limit.")
	}
	seen := map[string]bool{}
	for _, e := range in.Events {
		if e.InstanceID != "" || e.AuthorityExpiresAt != nil || !calendarText(e.EventID, 256) || !opaque(e.ContentID) || !calendarText(e.Kind, 32) || !calendarText(e.Title, 4096) || len(e.EpisodeTitle) > 4096 || len(e.ArtworkID) > 2048 || strings.Contains(e.ArtworkID, "://") || seen[e.EventID] {
			return fault.Invalid("Invalid source calendar event.")
		}
		if _, err = time.Parse("2006-01-02", e.Date); err != nil {
			return fault.Invalid("Invalid calendar date.")
		}
		if eventDay(e) < in.Start || eventDay(e) > in.End {
			return fault.Invalid("Calendar event is outside its window.")
		}
		if e.AirTimezone != "" {
			if _, err = time.LoadLocation(e.AirTimezone); err != nil {
				return fault.Invalid("Invalid airing timezone.")
			}
		}
		seen[e.EventID] = true
	}
	return nil
}
func calendarBinding(ctx context.Context, tx pgx.Tx, raw, instance, account, profile string) (SyncAuthority, error) {
	var registered string
	err := tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&registered)
	if err != nil {
		return SyncAuthority{}, authorityError(err)
	}
	if registered != instance {
		return SyncAuthority{}, fault.Forbidden
	}
	var target int64
	err = tx.QueryRow(ctx, `SELECT profile_id FROM bloem_profiles WHERE instance_id=$1 AND source_account_id=$2 AND source_profile_id=$3`, instance, account, profile).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncAuthority{}, fault.Forbidden
	}
	if err != nil {
		return SyncAuthority{}, err
	}
	return lockSourceProfile(ctx, tx, instance, target, false)
}
func validCalendarLease(ctx context.Context, tx pgx.Tx, expires time.Time) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT $1::timestamptz>clock_timestamp() AND $1::timestamptz<=clock_timestamp()+interval '150 seconds'`, expires).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return fault.Invalid("Calendar source authority lease is invalid or expired.")
	}
	return nil
}

// AcceptCalendarPage stages immutable receipts; no event is visible until an
// explicitly completed snapshot is atomically published for its source window.
func (s Store) AcceptCalendarPage(ctx context.Context, raw string, in CalendarInput) (CalendarReceipt, error) {
	out := CalendarReceipt{SnapshotID: in.SnapshotID, NextCursor: in.NextCursor, Complete: in.Complete}
	if err := in.validate(); err != nil {
		return out, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	binding, err := calendarBinding(ctx, tx, raw, in.InstanceID, in.AccountID, in.ProfileID)
	if err != nil {
		return out, err
	}
	if err = validCalendarLease(ctx, tx, in.ExpiresAt); err != nil {
		return out, err
	}
	// Serialize first insert and receipt replay without a global instance lock.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.bloem.calendar.snapshot.'||$1,0))`, in.SnapshotID); err != nil {
		return out, err
	}
	var instance, account, profile, start, end, digest, next string
	var destination int64
	var revision uint64
	var complete bool
	err = tx.QueryRow(ctx, `SELECT instance_id,profile_id,source_account_id,source_profile_id,to_char(start_date,'YYYY-MM-DD'),to_char(end_date,'YYYY-MM-DD'),visibility_digest,source_revision,next_cursor,complete FROM bloem_calendar_snapshots WHERE id=$1 FOR UPDATE`, in.SnapshotID).Scan(&instance, &destination, &account, &profile, &start, &end, &digest, &revision, &next, &complete)
	if errors.Is(err, pgx.ErrNoRows) {
		if in.Cursor != "" {
			return out, fault.Conflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_calendar_snapshots(id,instance_id,profile_id,source_account_id,source_profile_id,start_date,end_date,visibility_digest,source_revision,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, in.SnapshotID, in.InstanceID, binding.ProfileID, in.AccountID, in.ProfileID, in.Start, in.End, in.VisibilityDigest, in.Revision, in.ExpiresAt)
		if err != nil {
			return out, err
		}
	} else if err != nil {
		return out, err
	} else if instance != in.InstanceID || destination != binding.ProfileID || account != in.AccountID || profile != in.ProfileID || start != in.Start || end != in.End || digest != in.VisibilityDigest || revision != in.Revision {
		return out, fault.Conflict
	}
	// Lease renewal is separate from the immutable page content/digest.
	stable := in
	stable.ExpiresAt = time.Time{}
	print := fingerprint(stable)
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest FROM bloem_calendar_pages WHERE snapshot_id=$1 AND cursor=$2`, in.SnapshotID, in.Cursor).Scan(&previous)
	if err == nil {
		if !bytes.Equal(previous, print) {
			return out, fault.Conflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if complete || next != in.Cursor {
		return out, fault.Conflict
	}
	for _, e := range in.Events {
		payload, err := json.Marshal(e)
		if err != nil {
			return out, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_calendar_events(snapshot_id,event_id,event_day,payload) VALUES($1,$2,$3,$4)`, in.SnapshotID, e.EventID, eventDay(e), payload); err != nil {
			return out, constraint(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_calendar_pages(snapshot_id,cursor,payload_digest,next_cursor,complete) VALUES($1,$2,$3,$4,$5)`, in.SnapshotID, in.Cursor, print, in.NextCursor, in.Complete); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_calendar_snapshots SET next_cursor=$2,complete=$3,expires_at=GREATEST(expires_at,$4) WHERE id=$1`, in.SnapshotID, in.NextCursor, in.Complete, in.ExpiresAt); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s Store) PublishCalendar(ctx context.Context, raw, snapshot string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var instance, account, profile, start, end string
	var revision uint64
	var complete, valid bool
	err = tx.QueryRow(ctx, `SELECT instance_id,source_account_id,source_profile_id,to_char(start_date,'YYYY-MM-DD'),to_char(end_date,'YYYY-MM-DD'),source_revision,complete,expires_at>clock_timestamp() FROM bloem_calendar_snapshots WHERE id=$1`, snapshot).Scan(&instance, &account, &profile, &start, &end, &revision, &complete, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	if err != nil {
		return err
	}
	binding, err := calendarBinding(ctx, tx, raw, instance, account, profile)
	if err != nil {
		return err
	}
	if !complete || !valid {
		return fault.Conflict
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.bloem.calendar.window.'||$1||':'||$2::bigint::text||':'||$3||':'||$4,0))`, instance, binding.ProfileID, start, end); err != nil {
		return err
	}
	// Snapshot authority is checked again after any window publisher wait.
	if err = tx.QueryRow(ctx, `SELECT complete,expires_at>clock_timestamp() FROM bloem_calendar_snapshots WHERE id=$1 FOR UPDATE`, snapshot).Scan(&complete, &valid); err != nil {
		return err
	}
	if !complete || !valid {
		return fault.Conflict
	}
	var current uint64
	var currentSnapshot string
	err = tx.QueryRow(ctx, `SELECT s.source_revision,s.id FROM bloem_calendar_windows w JOIN bloem_calendar_snapshots s ON s.id=w.snapshot_id WHERE w.instance_id=$1 AND w.profile_id=$2 AND w.start_date=$3 AND w.end_date=$4`, instance, binding.ProfileID, start, end).Scan(&current, &currentSnapshot)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if current > revision || current == revision && currentSnapshot != snapshot {
		return fault.Conflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_calendar_windows(instance_id,profile_id,start_date,end_date,snapshot_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(instance_id,profile_id,start_date,end_date) DO UPDATE SET snapshot_id=EXCLUDED.snapshot_id`, instance, binding.ProfileID, start, end, snapshot)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CalendarEvents reads only currently active, published, unexpired source rows.
// Authorization is fresh even for direct service calls outside HTTP middleware.
// Release A no longer shows Silo calendar rows, so only tests call it; it goes with
// the plugin 0.1.14 calendar routes in release B.
func (s Store) CalendarEvents(ctx context.Context, a identity.Actor, start, end, filter string) ([]CalendarEvent, error) {
	from, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, fault.Invalid("Invalid calendar start.")
	}
	to, err := time.Parse("2006-01-02", end)
	if err != nil || to.Before(from) || to.Sub(from) > 400*24*time.Hour {
		return nil, fault.Invalid("Invalid calendar end.")
	}
	switch filter {
	case "all", "following", "favourites", "watchlist":
	default:
		return nil, fault.Invalid("Invalid calendar filter.")
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
	rows, err := tx.Query(ctx, `SELECT payload,instance_id,expires_at FROM (
 SELECT DISTINCT ON (e.event_id,s.instance_id) e.event_id,s.instance_id,e.event_day,e.payload,s.source_revision,s.expires_at
 FROM bloem_calendar_windows w JOIN bloem_calendar_snapshots s ON s.id=w.snapshot_id
 JOIN bloem_calendar_events e ON e.snapshot_id=s.id
 JOIN bloem_instances i ON i.id=s.instance_id AND i.active
 JOIN bloem_profiles p ON p.instance_id=s.instance_id AND p.profile_id=s.profile_id AND p.active AND NOT p.excluded
 JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active
 WHERE w.profile_id=$1 AND s.expires_at>clock_timestamp() AND e.event_day BETWEEN $2 AND $3
 ORDER BY e.event_id,s.instance_id,s.source_revision DESC
 ) visible WHERE CASE $4 WHEN 'following' THEN COALESCE((payload->>'followed')::boolean,false) WHEN 'favourites' THEN COALESCE((payload->>'favourite')::boolean,false) WHEN 'watchlist' THEN COALESCE((payload->>'watchlist')::boolean,false) ELSE true END ORDER BY event_day,event_id,instance_id LIMIT 10001`, fresh.TrackingID(), from, to, filter)
	if err != nil {
		return nil, err
	}
	out := []CalendarEvent{}
	for rows.Next() {
		var raw []byte
		var instance string
		var expires time.Time
		if err = rows.Scan(&raw, &instance, &expires); err != nil {
			rows.Close()
			return nil, err
		}
		var event CalendarEvent
		if err = json.Unmarshal(raw, &event); err != nil {
			rows.Close()
			return nil, err
		}
		if filter == "following" && !event.Followed || filter == "favourites" && !event.Favourite || filter == "watchlist" && !event.Watchlist {
			continue
		}
		event.InstanceID = instance
		event.AuthorityExpiresAt = &expires
		out = append(out, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out) > 10000 {
		return nil, fault.Invalid("Choose a smaller calendar range.")
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// CalendarLease carries a fresh source proof for one published visibility digest.
type CalendarLease struct {
	InstanceID       string    `json:"instance_id"`
	AccountID        string    `json:"account_id"`
	ProfileID        string    `json:"profile_id"`
	VisibilityDigest string    `json:"visibility_digest"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func (s Store) calendarLeaseTx(ctx context.Context, raw string, in CalendarLease) (pgx.Tx, SyncAuthority, error) {
	if !opaque(in.InstanceID) || !opaque(in.AccountID) || !opaque(in.ProfileID) || !calendarText(in.VisibilityDigest, 256) {
		return nil, SyncAuthority{}, fault.Invalid("Invalid calendar lease binding.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, SyncAuthority{}, err
	}
	binding, err := calendarBinding(ctx, tx, raw, in.InstanceID, in.AccountID, in.ProfileID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, SyncAuthority{}, err
	}
	return tx, binding, nil
}
func (s Store) RenewCalendarLease(ctx context.Context, raw string, in CalendarLease) error {
	tx, binding, err := s.calendarLeaseTx(ctx, raw, in)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = validCalendarLease(ctx, tx, in.ExpiresAt); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_calendar_snapshots s SET expires_at=$4 FROM bloem_calendar_windows w WHERE w.snapshot_id=s.id AND s.instance_id=$1 AND s.profile_id=$2 AND s.visibility_digest=$3`, in.InstanceID, binding.ProfileID, in.VisibilityDigest, in.ExpiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fault.NotFound
	}
	return tx.Commit(ctx)
}
func (s Store) InvalidateCalendar(ctx context.Context, raw string, in CalendarLease) error {
	tx, binding, err := s.calendarLeaseTx(ctx, raw, in)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE bloem_calendar_snapshots SET expires_at=LEAST(expires_at,clock_timestamp()) WHERE instance_id=$1 AND profile_id=$2 AND visibility_digest=$3`, in.InstanceID, binding.ProfileID, in.VisibilityDigest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SweepCalendar bounds abandoned staging and receipt growth. Published windows
// stay available for safe lease renewal; a failed export cannot remove them.
func (s Store) SweepCalendar(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `WITH old AS (SELECT s.id FROM bloem_calendar_snapshots s WHERE s.expires_at<clock_timestamp()-interval '24 hours' AND NOT EXISTS(SELECT 1 FROM bloem_calendar_windows w WHERE w.snapshot_id=s.id) ORDER BY s.expires_at LIMIT 500 FOR UPDATE OF s SKIP LOCKED) DELETE FROM bloem_calendar_snapshots s USING old WHERE s.id=old.id`)
	return tag.RowsAffected(), err
}
