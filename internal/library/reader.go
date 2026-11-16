package library

import (
	"context"
	"errors"
	"math"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// ReaderProgress moves a title forward from a reading device's position, given as a
// fraction of the whole. It never moves progress back or reopens a finished title; the
// end of the book completes it.
func (s Store) ReaderProgress(ctx context.Context, userID, entryID int64, fraction float64) (bool, error) {
	if fraction < 0 || fraction > 1 || math.IsNaN(fraction) {
		return false, nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	changed, err := s.ReaderProgressTx(ctx, tx, userID, entryID, fraction)
	if err != nil {
		return false, err
	}
	return changed, tx.Commit(ctx)
}

// ReaderProgressTx lets the position and tracking projection commit atomically.
func (s Store) ReaderProgressTx(ctx context.Context, tx pgx.Tx, userID, entryID int64, fraction float64) (bool, error) {
	if fraction < 0 || fraction > 1 || math.IsNaN(fraction) {
		return false, fault.Invalid("Invalid reading progress.")
	}
	a, err := identity.ResolveWorkerProfile(ctx, tx, userID, "kosync", []string{"library:write"}, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fault.Unauthenticated
	}
	if err != nil {
		return false, err
	}

	if err = lockChanges(ctx, tx, userID); err != nil {
		return false, err
	}
	var total *int
	var progress int
	var status string
	err = tx.QueryRow(ctx, "SELECT total,progress,status FROM library_entries WHERE profile_id=$1 AND id=$2 AND media_type IN ('book','audiobook','comic','manga') FOR UPDATE", userID, entryID).Scan(&total, &progress, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if total == nil || *total <= 0 || status == "completed" {
		return false, nil
	}
	next, nextStatus := int(math.Floor(fraction*float64(*total))), "in_progress"
	if fraction >= 0.995 {
		next, nextStatus = *total, "completed"
	}
	if next <= progress && (nextStatus != "completed" || status == "completed") {
		return false, nil
	}
	next = max(next, progress)
	if _, err = tx.Exec(ctx, "SELECT set_config('pastime.user_mutation','true',true)"); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE library_entries SET progress=$3,status=$4,completed_cycles=completed_cycles+$5,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2`,
		userID, entryID, next, nextStatus, completionDelta(status, nextStatus)); err != nil {
		return false, err
	}
	if err = event(ctx, tx, a, entryID, "progress"); err != nil {
		return false, err
	}
	if nextStatus == "completed" {
		if err = completionPlay(ctx, tx, a, entryID); err != nil {
			return false, err
		}
	}
	return true, nil
}
