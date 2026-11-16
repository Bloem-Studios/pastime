package library

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type SavedViewInput struct {
	Name     string `json:"name"`
	Rules    Rules  `json:"rules"`
	Sort     string `json:"sort"`
	Layout   string `json:"layout"`
	Position int    `json:"position"`
	Version  int    `json:"version"`
}
type SavedView struct {
	ID int64 `json:"id,string"`
	SavedViewInput
}

func (s Store) SavedViews(ctx context.Context, a identity.Actor) ([]SavedView, error) {
	out := []SavedView{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,name,rules,sort,layout,position,version FROM saved_views WHERE profile_id=$1 ORDER BY position,id LIMIT 100`, a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v SavedView
		if err = rows.Scan(&v.ID, &v.Name, &v.Rules, &v.Sort, &v.Layout, &v.Position, &v.Version); err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s Store) SaveView(ctx context.Context, a identity.Actor, id int64, in SavedViewInput) (SavedView, error) {
	in.Name = strings.TrimSpace(in.Name)
	if len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 80 || in.Position < 0 || in.Position > 100000 || (in.Layout != "grid" && in.Layout != "list") {
		return SavedView{}, fault.Invalid("Invalid saved view.")
	}
	if _, ok := sorts[in.Sort]; (!ok && in.Sort != "played") || in.Sort == "position" {
		return SavedView{}, fault.Invalid("Invalid saved view.")
	}
	if err := in.Rules.Validate(); err != nil {
		return SavedView{}, err
	}
	return identity.Write(ctx, s.Pool, a, "library:write", func(tx pgx.Tx, a identity.Actor) (SavedView, error) {
		var v SavedView
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.views.'||$1::bigint::text,0))`, a.TrackingID()); err != nil {
			return v, err
		}
		for _, tag := range in.Rules.TagIDs {
			var own bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tags WHERE profile_id=$1 AND id=$2::bigint)`, a.TrackingID(), tag).Scan(&own); err != nil {
				return v, err
			}
			if !own {
				return v, fault.NotFound
			}
		}
		raw, err := json.Marshal(in.Rules)
		if err != nil {
			return v, err
		}
		if id == 0 {
			var count int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM saved_views WHERE profile_id=$1`, a.TrackingID()).Scan(&count); err != nil {
				return v, err
			}
			if count >= 100 {
				return v, fault.Invalid("You can save up to 100 views.")
			}
			err = tx.QueryRow(ctx, `INSERT INTO saved_views(profile_id,name,rules,sort,layout,position) VALUES($1,$2,$3,$4,$5,$6) RETURNING id,name,rules,sort,layout,position,version`, a.TrackingID(), in.Name, raw, in.Sort, in.Layout, in.Position).Scan(&v.ID, &v.Name, &v.Rules, &v.Sort, &v.Layout, &v.Position, &v.Version)
		} else {
			err = tx.QueryRow(ctx, `UPDATE saved_views SET name=$3,rules=$4,sort=$5,layout=$6,position=$7,version=version+1 WHERE profile_id=$1 AND id=$2 AND version=$8 RETURNING id,name,rules,sort,layout,position,version`, a.TrackingID(), id, in.Name, raw, in.Sort, in.Layout, in.Position, in.Version).Scan(&v.ID, &v.Name, &v.Rules, &v.Sort, &v.Layout, &v.Position, &v.Version)
		}
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			err = fault.Conflict
		}
		if errors.Is(err, pgx.ErrNoRows) {
			err = fault.Conflict
		}
		return v, err
	})
}
func (s Store) DeleteView(ctx context.Context, a identity.Actor, id int64, version int) error {
	return identity.WriteOnly(ctx, s.Pool, a, "library:write", func(tx pgx.Tx, a identity.Actor) error {
		tag, err := tx.Exec(ctx, `DELETE FROM saved_views WHERE profile_id=$1 AND id=$2 AND version=$3`, a.TrackingID(), id, version)
		if err == nil && tag.RowsAffected() != 1 {
			return fault.Conflict
		}
		return err
	})
}

// ReorderViews requires the complete current set so a stale client cannot hide a
// newly-created view. The owner lock serializes creates and order changes.
func (s Store) ReorderViews(ctx context.Context, a identity.Actor, ids []string) error {
	if len(ids) > 100 {
		return fault.Invalid("Invalid saved view.")
	}
	return identity.WriteOnly(ctx, s.Pool, a, "library:write", func(tx pgx.Tx, a identity.Actor) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.views.'||$1::bigint::text,0))`, a.TrackingID()); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_views WHERE profile_id=$1`, a.TrackingID()).Scan(&count); err != nil {
			return err
		}
		if count != len(ids) {
			return fault.Conflict
		}
		seen := map[int64]bool{}
		for i, id := range ids {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil || n < 1 || seen[n] {
				return fault.Invalid("Invalid saved view.")
			}
			seen[n] = true
			tag, err := tx.Exec(ctx, `UPDATE saved_views SET position=$3,version=version+1 WHERE profile_id=$1 AND id=$2`, a.TrackingID(), n, i)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return fault.Conflict
			}
		}
		return nil
	})
}
