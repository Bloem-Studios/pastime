package library

import (
	"context"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

// Resume is a stored playback position with the identity a sync client needs: the
// playable item and, for an episode, its show.
type Resume struct {
	CatalogID       int64
	Kind            string
	Provider        string
	ProviderID      string
	Title           string
	ExternalIDs     map[string]any
	ShowProvider    string
	ShowProviderID  string
	ShowTitle       string
	ShowExternalIDs map[string]any
	SeasonNumber    *int
	EpisodeNumber   *int
	PositionSeconds int
	DurationSeconds *int
	UpdatedAt       time.Time
}

// SaveProgress records a resume position, rechecking the caller's write authority.
func (s Store) SaveProgress(ctx context.Context, a identity.Actor, catalogID int64, position int, duration *int, source string) error {
	if position < 0 || position > 10000000 || (duration != nil && (*duration < 1 || *duration > 10000000)) {
		return fault.Invalid("Position and duration must be within ten million seconds.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO playback_progress(profile_id,catalog_item_id,position_seconds,duration_seconds,source) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(profile_id,catalog_item_id) DO UPDATE SET position_seconds=EXCLUDED.position_seconds,
		duration_seconds=COALESCE(EXCLUDED.duration_seconds,playback_progress.duration_seconds),source=EXCLUDED.source,updated_at=clock_timestamp()`,
		a.TrackingID(), catalogID, position, duration, source); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClearProgress removes a resume position once the item has been finished.
func (s Store) ClearProgress(ctx context.Context, a identity.Actor, catalogID int64) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM playback_progress WHERE profile_id=$1 AND catalog_item_id=$2", a.TrackingID(), catalogID)
	return err
}

// Resumes lists resume positions, most recently updated first.
func (s Store) Resumes(ctx context.Context, a identity.Actor, since *time.Time, kinds []string, limit, offset int) ([]Resume, int, error) {
	if !a.Allows("library:read") {
		return nil, 0, fault.Forbidden
	}
	where := " WHERE p.profile_id=$1 AND c.kind=ANY($2)"
	args := []any{a.TrackingID(), kinds}
	if since != nil {
		args = append(args, *since)
		where += " AND p.updated_at>$" + strconv.Itoa(len(args))
	}
	const joins = ` FROM playback_progress p JOIN catalog_items c ON c.id=p.catalog_item_id
		LEFT JOIN catalog_items season ON season.id=c.parent_id AND c.kind='episode' LEFT JOIN catalog_items show ON show.id=season.parent_id`
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*)"+joins+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.Pool.Query(ctx, `SELECT c.id,c.kind,c.provider,c.provider_id,c.title,c.external_ids,COALESCE(show.provider,''),COALESCE(show.provider_id,''),COALESCE(show.title,''),COALESCE(show.external_ids,'{}'),
		c.season_number,c.episode_number,p.position_seconds,COALESCE(p.duration_seconds,c.runtime_minutes*60),p.updated_at`+joins+where+
		` ORDER BY p.updated_at DESC,p.catalog_item_id LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Resume{}
	for rows.Next() {
		var r Resume
		if err = rows.Scan(&r.CatalogID, &r.Kind, &r.Provider, &r.ProviderID, &r.Title, &r.ExternalIDs, &r.ShowProvider, &r.ShowProviderID, &r.ShowTitle, &r.ShowExternalIDs,
			&r.SeasonNumber, &r.EpisodeNumber, &r.PositionSeconds, &r.DurationSeconds, &r.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// PlayRecord is one play with the identifiers a sync client matches on.
type PlayRecord struct {
	PlayID          int64
	PlayedAt        time.Time
	Kind            string
	Provider        string
	ProviderID      string
	Title           string
	ExternalIDs     map[string]any
	ShowProvider    string
	ShowProviderID  string
	ShowTitle       string
	ShowExternalIDs map[string]any
	SeasonNumber    *int
	EpisodeNumber   *int
}

// PlayRecords pages plays newest first by offset, for clients whose protocol requires
// offsets. Date bounds are inclusive UTC calendar days.
func (s Store) PlayRecords(ctx context.Context, a identity.Actor, start, end *time.Time, limit, offset int) ([]PlayRecord, int, error) {
	if !a.Allows("library:read") {
		return nil, 0, fault.Forbidden
	}
	where := " WHERE p.profile_id=$1"
	args := []any{a.TrackingID()}
	if start != nil {
		args = append(args, *start)
		where += " AND p.played_at>=$" + strconv.Itoa(len(args))
	}
	if end != nil {
		args = append(args, end.AddDate(0, 0, 1))
		where += " AND p.played_at<$" + strconv.Itoa(len(args))
	}
	const joins = ` FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id
		LEFT JOIN catalog_items season ON season.id=c.parent_id AND c.kind='episode' LEFT JOIN catalog_items show ON show.id=season.parent_id`
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM plays p"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.Pool.Query(ctx, `SELECT p.id,p.played_at,c.kind,c.provider,c.provider_id,c.title,c.external_ids,COALESCE(show.provider,''),COALESCE(show.provider_id,''),COALESCE(show.title,''),COALESCE(show.external_ids,'{}'),
		c.season_number,c.episode_number`+joins+where+` ORDER BY p.played_at DESC,p.id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []PlayRecord{}
	for rows.Next() {
		var r PlayRecord
		if err = rows.Scan(&r.PlayID, &r.PlayedAt, &r.Kind, &r.Provider, &r.ProviderID, &r.Title, &r.ExternalIDs, &r.ShowProvider, &r.ShowProviderID, &r.ShowTitle, &r.ShowExternalIDs, &r.SeasonNumber, &r.EpisodeNumber); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
