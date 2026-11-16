package library

import (
	"context"
	"strconv"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// recordWatchRemovalSQL notes that a profile removed a movie or show from its
// library, for state:read to pass to Bloem as a watched removal (watch_removals).
// Only a profile connected to Bloem gets a note, so connecting later never
// replays an old deletion against history Bloem already had.
const recordWatchRemovalSQL = `INSERT INTO watch_removals(profile_id,catalog_item_id,removed_at)
 SELECT e.profile_id,e.catalog_item_id,clock_timestamp() FROM library_entries e JOIN catalog_items c ON c.id=e.catalog_item_id
 WHERE e.profile_id=$1 AND e.id=$2 AND c.kind IN ('movie','series')
 AND EXISTS(SELECT 1 FROM bloem_sync_credentials k JOIN bloem_profiles b ON b.instance_id=k.instance_id AND b.profile_id=k.profile_id
  WHERE k.profile_id=e.profile_id AND b.active AND NOT b.excluded AND (k.revoked_at IS NULL OR k.revoked_at>clock_timestamp()) AND k.expires_at>clock_timestamp())
 ON CONFLICT (profile_id,catalog_item_id) DO UPDATE SET removed_at=EXCLUDED.removed_at`

// event touches an entry and records an activity event. Inside an import run's
// transaction (pastime.import_run, set by ApplyImportTx) the event names that run.
func event(ctx context.Context, tx pgx.Tx, a identity.Actor, id int64, kind string) error {
	_, err := tx.Exec(ctx, `WITH touched AS (
		UPDATE library_entries SET last_activity_at=clock_timestamp(),
		started_at=CASE WHEN started_at IS NULL AND (progress>0 OR status IN ('in_progress','completed')) THEN clock_timestamp() ELSE started_at END,
		finished_at=CASE WHEN status='completed' THEN COALESCE(finished_at,clock_timestamp()) ELSE NULL END
		WHERE profile_id=$1 AND id=$2 RETURNING profile_id,id,unit,status,progress,cycle)
	INSERT INTO activity_events(profile_id,entry_id,kind,unit,status,progress,cycle,import_run)
	SELECT profile_id,id,$3,unit,status,progress,cycle,NULLIF(current_setting('pastime.import_run',true),'')::uuid FROM touched`, a.TrackingID(), id, kind)
	return err
}
func current(ctx context.Context, tx pgx.Tx, a identity.Actor, id int64, version int) (Entry, error) {
	if version < 1 {
		return Entry{}, fault.Invalid("Include the current record version.")
	}
	e, err := scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2 FOR UPDATE OF e", a.TrackingID(), id))
	if err != nil {
		return e, err
	}
	if e.Version != version {
		return e, fault.Conflict
	}
	return e, nil
}
func completionDelta(before, after string) int {
	if before == after {
		return 0
	}
	if after == "completed" {
		return 1
	}
	if before == "completed" {
		return -1
	}
	return 0
}
func (s Store) Create(ctx context.Context, a identity.Actor, key string, in EntryInput) (Mutation, error) {
	if err := in.validate(true); err != nil {
		return Mutation{}, err
	}
	return s.mutate(ctx, a, "library:write", key, "entry.create", in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		var out Mutation
		completed := 0
		if in.Status == "completed" {
			completed = 1
		}
		err := tx.QueryRow(ctx, `INSERT INTO library_entries(profile_id,title,media_type,publication_year,unit,total,status,progress,rating,notes,completed_cycles)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id,version`, a.TrackingID(), in.Title, in.MediaType, in.Year, in.Unit, in.Total, in.Status, in.Progress, in.Rating, in.Notes, completed).Scan(&out.ID, &out.Version)
		if err == nil {
			err = event(ctx, tx, a, out.ID, "added")
		}
		return out, err
	})
}
func (s Store) Replace(ctx context.Context, a identity.Actor, id int64, key string, in EntryInput) (Mutation, error) {
	if err := in.validate(false); err != nil {
		return Mutation{}, err
	}
	return s.mutate(ctx, a, "library:write", key, "entry.replace/"+strconv.FormatInt(id, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		old, err := current(ctx, tx, a, id, in.Version)
		if err != nil {
			return Mutation{}, err
		}
		// Plays record the media type they were counted under, so a catalog-backed entry
		// keeps the type its catalog record gives it.
		if old.CatalogID != nil && in.MediaType != old.MediaType {
			return Mutation{}, fault.Invalid("The media type of a title from the catalog cannot be changed.")
		}
		out := Mutation{ID: id}
		err = tx.QueryRow(ctx, `UPDATE library_entries SET title=$3,media_type=$4,publication_year=$5,unit=$6,total=$7,status=$8,progress=$9,rating=$10,notes=$11,
		completed_cycles=completed_cycles+$12,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version`, a.TrackingID(), id, in.Title, in.MediaType, in.Year, in.Unit, in.Total, in.Status, in.Progress, in.Rating, in.Notes, completionDelta(old.Status, in.Status)).Scan(&out.Version)
		if err == nil {
			err = event(ctx, tx, a, id, "updated")
		}
		if err == nil && in.Status == "completed" && old.Status != "completed" {
			err = completionPlay(ctx, tx, a, id)
		}
		return out, err
	})
}
func (s Store) Progress(ctx context.Context, a identity.Actor, id int64, key string, in ProgressInput) (Mutation, error) {
	if in.Version < 1 || in.Progress == nil || *in.Progress < 0 || *in.Progress > 100000000 || !validStatus(in.Status) {
		return Mutation{}, fault.Invalid("Include a current version, nonnegative progress and a supported status.")
	}
	return s.mutate(ctx, a, "library:write", key, "entry.progress/"+strconv.FormatInt(id, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		old, err := current(ctx, tx, a, id, in.Version)
		if err != nil {
			return Mutation{}, err
		}
		progress := *in.Progress
		if old.Total != nil && progress > *old.Total {
			return Mutation{}, fault.Invalid("Progress cannot exceed the total.")
		}
		if in.Status == "completed" && old.Total != nil {
			progress = *old.Total
		}
		out := Mutation{ID: id, Version: old.Version}
		if old.Progress == progress && old.Status == in.Status {
			return out, nil
		}
		err = tx.QueryRow(ctx, `UPDATE library_entries SET progress=$3,status=$4,completed_cycles=completed_cycles+$5,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version`, a.TrackingID(), id, progress, in.Status, completionDelta(old.Status, in.Status)).Scan(&out.Version)
		if err == nil {
			err = event(ctx, tx, a, id, "progress")
		}
		if err == nil && in.Status == "completed" && old.Status != "completed" && old.CatalogID != nil {
			err = completionPlay(ctx, tx, a, id)
		}
		return out, err
	})
}
func (s Store) Restart(ctx context.Context, a identity.Actor, id int64, key string, version int) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "entry.restart/"+strconv.FormatInt(id, 10), version, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		old, err := current(ctx, tx, a, id, version)
		if err != nil {
			return Mutation{}, err
		}
		if old.Progress == 0 && old.Status != "completed" {
			return Mutation{}, fault.Invalid("This item is already at the beginning.")
		}
		if old.Cycle >= 100000000 {
			return Mutation{}, fault.Invalid("The cycle limit has been reached.")
		}
		out := Mutation{ID: id}
		err = tx.QueryRow(ctx, `UPDATE library_entries SET cycle=cycle+1,progress=0,status='in_progress',started_at=NULL,finished_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version`, a.TrackingID(), id).Scan(&out.Version)
		if err == nil {
			err = event(ctx, tx, a, id, "restarted")
		}
		if err == nil {
			err = refreshNext(ctx, tx, a.TrackingID(), id)
		}
		return out, err
	})
}
func (s Store) Delete(ctx context.Context, a identity.Actor, id int64, key string, version int) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "entry.delete/"+strconv.FormatInt(id, 10), version, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		old, err := current(ctx, tx, a, id, version)
		if err != nil {
			return Mutation{}, err
		}
		// A connected profile's Bloem watch history follows: note the removal for
		// state:read (see watch_removals) before the entry and its plays go.
		if _, err = tx.Exec(ctx, recordWatchRemovalSQL, a.TrackingID(), id); err != nil {
			return Mutation{}, err
		}
		_, err = tx.Exec(ctx, "DELETE FROM library_entries WHERE profile_id=$1 AND id=$2", a.TrackingID(), id)
		return Mutation{ID: id, Version: old.Version, Deleted: true}, err
	})
}
