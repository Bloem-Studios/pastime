package library

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/jobs"
)

const WarmJob = "statistics_warm"

// EnqueueWarm visits at most 25 active accounts per scheduling pass. The cursor
// and durable jobs commit together, so restarts cannot skip a page.
func (s Store) EnqueueWarm(ctx context.Context, now time.Time) error {
	if s.Cache == nil {
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var last int64
	if err = tx.QueryRow(ctx, "SELECT last_id FROM maintenance_cursors WHERE name='statistics_warm' FOR UPDATE").Scan(&last); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT p.id FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE u.active AND p.archived_at IS NULL AND p.id>$1 ORDER BY p.id LIMIT 25", last)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		payload, _ := json.Marshal(map[string]any{"profile_id": id, "day": now.UTC().Format("2006-01-02")})
		if _, err = jobs.Enqueue(ctx, tx, WarmJob, fmt.Sprintf("statistics_warm:%d:%s", id, now.UTC().Truncate(15*time.Minute).Format(time.RFC3339)), "idempotent", payload, 3); err != nil {
			return err
		}
		last = id
	}
	if len(ids) < 25 {
		last = 0
	}
	if _, err = tx.Exec(ctx, "UPDATE maintenance_cursors SET last_id=$1 WHERE name='statistics_warm'", last); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s Store) WarmHandler(ctx context.Context, raw json.RawMessage) error {
	if s.Cache == nil {
		return nil
	}
	var p struct {
		ProfileID int64  `json:"profile_id"`
		Day       string `json:"day"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ProfileID < 1 {
		return jobs.ErrInvalidPayload
	}
	day, err := time.Parse("2006-01-02", p.Day)
	if err != nil {
		return jobs.ErrInvalidPayload
	}
	// Warming has a short deadline and never owns a foreground worker lane.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	a, err := identity.ResolveWorkerProfile(ctx, tx, p.ProfileID, "worker", []string{"library:read"}, true)
	if err == fault.Unauthenticated {
		return nil
	}
	if err != nil {
		return err
	}

	for _, window := range [][2]time.Time{{day.AddDate(0, 0, -365), day.AddDate(0, 0, 1)}, {day.AddDate(0, 0, -730), day.AddDate(0, 0, -365)}} {
		if _, err = s.Insights(ctx, a, window[0], window[1]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
