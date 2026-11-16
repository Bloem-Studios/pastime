package catalog

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/jobs"
)

// RefreshJob is the durable job kind that refetches one top-level record.
const RefreshJob = "catalog_refresh"

// EnqueueStale schedules refreshes for tracked records whose metadata can still change:
// serialized works, anything unreleased or recent, and anything never refreshed in a week.
// The dedupe key is per record per day, so overlapping schedulers enqueue once.
func (s *Service) EnqueueStale(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.id FROM catalog_items c
	WHERE c.parent_id IS NULL AND EXISTS(SELECT 1 FROM library_entries e WHERE e.catalog_item_id=c.id AND e.status IN ('planned','in_progress','paused')) AND (
		(c.refreshed_at < $1::timestamptz - interval '1 day' AND (c.kind IN ('series','anime','manga','comic','podcast') OR c.release_date IS NULL OR c.release_date > $1::date - 60))
		OR c.refreshed_at < $1::timestamptz - interval '7 days')
	AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.dedupe_key='catalog_refresh:'||c.id::text||':'||to_char($1::timestamptz AT TIME ZONE 'UTC','YYYY-MM-DD'))
	ORDER BY c.refreshed_at,c.id LIMIT 200`, now)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	day := now.UTC().Format("2006-01-02")
	for _, id := range ids {
		payload, _ := json.Marshal(map[string]string{"catalog_id": strconv.FormatInt(id, 10)})
		if _, err = jobs.Enqueue(ctx, s.Pool, RefreshJob, RefreshJob+":"+strconv.FormatInt(id, 10)+":"+day, "idempotent", payload, 3); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// RefreshHandler refetches a record and its two newest regular seasons, so episodes
// that are airing now reach the calendar and up-next rows without a visit.
func (s *Service) RefreshHandler(ctx context.Context, payload json.RawMessage) error {
	var in struct {
		CatalogID string `json:"catalog_id"`
	}
	if err := json.Unmarshal(payload, &in); err != nil {
		return jobs.ErrInvalidPayload
	}
	id, err := strconv.ParseInt(in.CatalogID, 10, 64)
	if err != nil || id < 1 {
		return jobs.ErrInvalidPayload
	}
	if err = s.Refresh(ctx, id); err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+itemColumns+" FROM catalog_items WHERE parent_id=$1 AND kind='season' AND season_number>0 ORDER BY season_number DESC LIMIT 2", id)
	if err != nil {
		return err
	}
	var seasons []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return err
		}
		seasons = append(seasons, it)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, season := range seasons {
		if err = s.loadSeason(ctx, season); err != nil {
			return err
		}
	}
	return rows.Err()
}
