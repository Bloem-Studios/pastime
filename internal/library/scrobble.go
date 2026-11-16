package library

import (
	"context"
	"errors"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// ScrobbleWindow suppresses repeated deliveries of one viewing: media servers can send a
// completion more than once, and a real rewatch within the window is rare.
const ScrobbleWindow = 3 * time.Hour

// Scrobble records an externally reported play of target (the title itself or one of its
// episodes), adding the title to the library when needed. It runs as the account that
// owns connectionID and rechecks both the account and the connection inside the
// transaction, so a disabled account or deleted connection cannot record anything.
func (s Store) Scrobble(ctx context.Context, userID int64, connectionID, source string, titleID, targetID int64, at time.Time) (bool, error) {
	return s.scrobble(ctx, connectionActor(ctx, userID, connectionID), connectionID, source, titleID, targetID, at, "")
}

func connectionActor(ctx context.Context, userID int64, connectionID string, expected ...time.Time) func(pgx.Tx) (identity.Actor, error) {
	return func(tx pgx.Tx) (identity.Actor, error) {
		var stamp *time.Time
		if len(expected) > 0 {
			stamp = &expected[0]
		}
		_, err := identity.ScanUser(tx.QueryRow(ctx, "SELECT "+identity.UserColumns+`
			FROM tracking_profiles p JOIN users u ON u.id=p.account_id JOIN connections c ON c.profile_id=p.id WHERE p.id=$1 AND p.archived_at IS NULL AND c.id=$2 AND u.active AND ($3::timestamptz IS NULL OR c.created_at=$3) FOR SHARE OF u,c`, userID, connectionID, stamp))
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.Actor{}, fault.Unauthenticated
		}
		if err != nil {
			return identity.Actor{}, err
		}
		return identity.ResolveWorkerProfile(ctx, tx, userID, "connection", []string{"library:read", "library:write"}, true)
	}
}

// Plan adds a title as planned for a connection's owner, such as a media request. A
// title already in the library is left as it is.
func (s Store) Plan(ctx context.Context, userID int64, connectionID string, titleID int64, expected ...time.Time) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	a, err := connectionActor(ctx, userID, connectionID, expected...)(tx)
	if err != nil {
		return false, err
	}
	if err = lockChanges(ctx, tx, a.TrackingID()); err != nil {
		return false, err
	}
	var entryID int64
	err = tx.QueryRow(ctx, `INSERT INTO library_entries(profile_id,title,media_type,publication_year,unit,total,status,catalog_item_id)
		SELECT $1,left(c.title,200),c.kind,extract(year FROM c.release_date)::int,c.unit,c.total,'planned',c.id
		FROM catalog_items c WHERE c.id=$2 AND c.parent_id IS NULL AND NOT EXISTS(SELECT 1 FROM library_tombstones WHERE profile_id=$1 AND catalog_item_id=c.id)
		ON CONFLICT DO NOTHING RETURNING id`, a.TrackingID(), titleID).Scan(&entryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err == nil {
		err = event(ctx, tx, a, entryID, "added")
	}
	if err == nil {
		_, err = tx.Exec(ctx, "UPDATE connections SET last_event_at=clock_timestamp(),events_recorded=events_recorded+1 WHERE id=$1", connectionID)
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// ScrobbleAs records a play for an authenticated API client, rechecking the caller's
// current authority and library:write scope inside the transaction.
func (s Store) ScrobbleAs(ctx context.Context, a identity.Actor, source string, titleID, targetID int64, at time.Time) (bool, error) {
	return s.scrobble(ctx, func(tx pgx.Tx) (identity.Actor, error) {
		return identity.AuthorizeTx(ctx, tx, a, "library:write")
	}, "", source, titleID, targetID, at, "")
}

func (s Store) scrobble(ctx context.Context, authorize func(pgx.Tx) (identity.Actor, error), connectionID, source string, titleID, targetID int64, at time.Time, eventID string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	a, err := authorize(tx)
	if err != nil {
		return false, err
	}
	applied, err := s.recordScrobbleTx(ctx, tx, a, connectionID, source, titleID, targetID, at, eventID)
	if err != nil {
		return false, err
	}
	return applied, tx.Commit(ctx)
}

// ScrobbleTx lets a native integration commit its replay receipt and play effect
// together. The caller must authorize the actor and keep its authority locks in
// this transaction; this method does not mint any login or integration authority.
func (s Store) ScrobbleTx(ctx context.Context, tx pgx.Tx, a identity.Actor, source string, titleID, targetID int64, at time.Time, eventID string) (bool, error) {
	if a.TrackingID() < 1 || !a.Allows("library:write") {
		return false, fault.Forbidden
	}
	if eventID == "" || len(eventID) > 200 || source == "" || len(source) > 200 || at.IsZero() {
		return false, fault.Invalid("Invalid managed playback event.")
	}
	current, err := identity.ResolveWorkerProfile(ctx, tx, a.TrackingID(), "connection", []string{"library:write"}, true)
	if err != nil {
		return false, err
	}
	if current.User.ID != a.User.ID {
		return false, fault.Forbidden
	}
	var valid bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM catalog_items WHERE id=$1 AND root_id=$2)", targetID, titleID).Scan(&valid); err != nil {
		return false, err
	}
	if !valid {
		return false, fault.NotFound
	}
	return s.recordScrobbleTx(ctx, tx, current, "", source, titleID, targetID, at, eventID)
}
func (s Store) recordScrobbleTx(ctx context.Context, tx pgx.Tx, a identity.Actor, connectionID, source string, titleID, targetID int64, at time.Time, eventID string) (bool, error) {
	a.Kind = "connection" // Completed external playback still respects deliberate library status.
	profileID := a.TrackingID()
	var err error
	if err := lockChanges(ctx, tx, a.TrackingID()); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('pastime.change_source',$1,true)", "scrobble:"+source); err != nil {
		return false, err
	}
	var deletedAt *time.Time
	if err = tx.QueryRow(ctx, "SELECT (SELECT deleted_at FROM library_tombstones WHERE profile_id=$1 AND catalog_item_id=$2)", profileID, titleID).Scan(&deletedAt); err != nil {
		return false, err
	}
	if deletedAt != nil {
		if !at.After(*deletedAt) {
			return false, nil
		}
		if _, err = tx.Exec(ctx, "DELETE FROM library_tombstones WHERE profile_id=$1 AND catalog_item_id=$2", profileID, titleID); err != nil {
			return false, err
		}
	}
	var entryID int64
	err = tx.QueryRow(ctx, "SELECT id FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2 FOR UPDATE", profileID, titleID).Scan(&entryID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO library_entries(profile_id,title,media_type,publication_year,unit,total,status,catalog_item_id)
		SELECT $1,left(c.title,200),c.kind,extract(year FROM c.release_date)::int,c.unit,c.total,'in_progress',c.id
		FROM catalog_items c WHERE c.id=$2 AND c.parent_id IS NULL AND c.kind IN ('movie','series','anime','podcast','music','audiobook','book','game','video') RETURNING id`, profileID, titleID).Scan(&entryID)
		if err == nil {
			err = event(ctx, tx, a, entryID, "added")
		}
	}
	if err != nil {
		return false, err
	}
	var duplicate bool
	if eventID != "" {
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM plays WHERE profile_id=$1 AND source=$2 AND external_id=$3)", profileID, source, eventID).Scan(&duplicate)
	} else {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plays WHERE profile_id=$1 AND entry_id=$2 AND catalog_item_id=$3 AND played_at BETWEEN $4::timestamptz-make_interval(secs=>$5) AND $4::timestamptz+make_interval(secs=>$5))`,
			profileID, entryID, targetID, at, ScrobbleWindow.Seconds()).Scan(&duplicate)
	}
	if err != nil {
		return false, err
	}
	if duplicate {
		return false, nil
	}
	e, err := scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2", profileID, entryID))
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,source,external_id) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''))", profileID, entryID, targetID, e.Cycle, at, source, eventID); err != nil {
		return false, err
	}
	if _, err = s.reproject(ctx, tx, a, e, "played"); err != nil {
		return false, err
	}
	if connectionID != "" {
		if _, err = tx.Exec(ctx, "UPDATE connections SET last_event_at=clock_timestamp(),events_recorded=events_recorded+1 WHERE id=$1", connectionID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ScrobbleEvent deduplicates a sender-identified viewing independently of its time.
func (s Store) ScrobbleEvent(ctx context.Context, userID int64, connectionID, source string, titleID, targetID int64, at time.Time, eventID string, expected ...time.Time) (bool, error) {
	return s.scrobble(ctx, connectionActor(ctx, userID, connectionID, expected...), connectionID, source, titleID, targetID, at, eventID)
}
