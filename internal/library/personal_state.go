package library

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

type PersonalState struct {
	Kind     string `json:"kind"`
	Present  bool   `json:"present"`
	Rating   *int   `json:"rating,omitempty"`
	Position *int32 `json:"position,omitempty"`
}

// ApplyPersonalStateTx changes only one state family. The caller owns fresh
// authority locks and commits its receipt with the effect in this transaction.
func (s Store) ApplyPersonalStateTx(ctx context.Context, tx pgx.Tx, a identity.Actor, catalogID int64, in PersonalState, at time.Time, source string) (bool, error) {
	if a.TrackingID() < 1 || !a.Allows("library:write") {
		return false, fault.Forbidden
	}
	if in.Kind != "favorite" && in.Kind != "watchlist" && in.Kind != "rating" || at.IsZero() || source == "" || len(source) > 100 || in.Position != nil && (in.Kind != "watchlist" || *in.Position < 0) || in.Kind == "rating" && (in.Present && (in.Rating == nil || *in.Rating < 0 || *in.Rating > 10) || !in.Present && in.Rating != nil) || in.Kind != "rating" && in.Rating != nil {
		return false, fault.Invalid("Invalid personal state.")
	}
	if err := lockChanges(ctx, tx, a.TrackingID()); err != nil {
		return false, err
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_items WHERE id=$1 AND parent_id IS NULL)`, catalogID).Scan(&valid); err != nil {
		return false, err
	}
	if !valid {
		return false, fault.NotFound
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('pastime.change_source',$1,true)`, source); err != nil {
		return false, err
	}
	// Joining a watchlist or rating a title can create its tracking record. An
	// existing record retains every unrelated field, including its current status.
	if in.Present && (in.Kind == "watchlist" || in.Kind == "rating") {
		status := "planned"
		if in.Kind == "rating" {
			status = "rated"
		}
		_, err := tx.Exec(ctx, `INSERT INTO library_entries(profile_id,title,media_type,publication_year,unit,total,status,catalog_item_id)
 SELECT $1,left(title,200),kind,extract(year FROM release_date)::int,unit,total,$3,id FROM catalog_items WHERE id=$2 ON CONFLICT DO NOTHING`, a.TrackingID(), catalogID, status)
		if err != nil {
			return false, err
		}
	}
	if in.Kind == "rating" {
		tag, err := tx.Exec(ctx, `UPDATE library_entries SET rating=$3,scored_at=$4,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND catalog_item_id=$2 AND rating IS DISTINCT FROM $3::integer`, a.TrackingID(), catalogID, in.Rating, at)
		return tag.RowsAffected() > 0, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO profile_catalog_memberships(profile_id,catalog_item_id,kind,present,position,changed_at,source)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(profile_id,catalog_item_id,kind) DO UPDATE SET present=EXCLUDED.present,position=EXCLUDED.position,changed_at=EXCLUDED.changed_at,source=EXCLUDED.source
 WHERE profile_catalog_memberships.present IS DISTINCT FROM EXCLUDED.present OR profile_catalog_memberships.position IS DISTINCT FROM EXCLUDED.position`, a.TrackingID(), catalogID, in.Kind, in.Present, in.Position, at, source)
	return tag.RowsAffected() > 0, err
}
