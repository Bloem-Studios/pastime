package imports

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
)

// Unmatched is one title an import could not match to the catalog.
type Unmatched struct {
	ID        int64     `json:"id,string"`
	RunID     string    `json:"run_id"`
	Source    string    `json:"source"`
	Label     string    `json:"label"`
	Kind      string    `json:"kind"`
	Year      *int      `json:"year"`
	Plays     int       `json:"plays"`
	Status    string    `json:"status"`
	CatalogID *int64    `json:"catalog_id,string"`
	CreatedAt time.Time `json:"created_at"`
}

// Unmatched lists the caller's unmatched import titles, pending first by default.
func (s Store) Unmatched(ctx context.Context, a identity.Actor, runID, status, cursor string, limit int) (page.Result[Unmatched], error) {
	out := page.Result[Unmatched]{Items: []Unmatched{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "resolved" && status != "ignored" {
		return out, fault.Invalid("Unknown status.")
	}
	if runID != "" && !identity.ValidUUID(runID) {
		return out, fault.NotFound
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := "unmatched|" + strconv.FormatInt(a.TrackingID(), 10) + "|" + runID + "|" + status
	last, err := page.Decode(scope, cursor)
	if err != nil {
		return out, err
	}
	query := `SELECT u.id,u.run_id::text,r.source,u.label,u.kind,u.record,u.status,u.catalog_item_id,u.created_at
		FROM import_unmatched u JOIN import_runs r ON r.id=u.run_id WHERE u.profile_id=$1 AND u.status=$2`
	args := []any{a.TrackingID(), status}
	if runID != "" {
		args = append(args, runID)
		query += " AND u.run_id=$" + strconv.Itoa(len(args)) + "::uuid"
	}
	if last > 0 {
		args = append(args, last)
		query += " AND u.id<$" + strconv.Itoa(len(args))
	}
	args = append(args, limit+1)
	query += " ORDER BY u.id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var u Unmatched
		var raw []byte
		if err = rows.Scan(&u.ID, &u.RunID, &u.Source, &u.Label, &u.Kind, &raw, &u.Status, &u.CatalogID, &u.CreatedAt); err != nil {
			return out, err
		}
		var rec Record
		if json.Unmarshal(raw, &rec) == nil {
			u.Plays = len(rec.Plays)
			if rec.Video != nil {
				u.Year = rec.Video.Year
			}
		}
		out.Items = append(out.Items, u)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode(scope, out.Items[limit-1].ID)
		out.NextCursor = &next
	}
	return out, nil
}

// Resolve applies an unmatched title to the catalog record the person chose, exactly as
// the import would have: merge-forward, with its plays keyed by their source identity.
func (s Store) Resolve(ctx context.Context, a identity.Actor, id, catalogID int64) (Unmatched, error) {
	var out Unmatched
	if !a.Allows("library:write") {
		return out, fault.Forbidden
	}
	var raw []byte
	var runID, source, status string
	err := s.Pool.QueryRow(ctx, `SELECT u.record,u.run_id::text,r.source,u.status FROM import_unmatched u JOIN import_runs r ON r.id=u.run_id WHERE u.profile_id=$1 AND u.id=$2`, a.TrackingID(), id).Scan(&raw, &runID, &source, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	if err != nil {
		return out, err
	}
	if status != "pending" {
		return out, &fault.Error{Status: 409, Code: "already_handled", Message: "This title was already matched or ignored."}
	}
	var rec Record
	if err = json.Unmarshal(raw, &rec); err != nil {
		return out, err
	}
	item, err := s.Catalog.Get(ctx, catalogID)
	if err != nil {
		return out, err
	}
	if item.ParentID != nil {
		return out, fault.Invalid("Choose a whole title, not a season or episode.")
	}
	in, err := s.importItem(ctx, runID, item, rec, map[int64]map[[2]int]int64{})
	if err != nil {
		return out, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return out, err
	}
	var runStatus string
	if err = tx.QueryRow(ctx, "SELECT status FROM import_runs WHERE profile_id=$1 AND id=$2 FOR UPDATE", a.TrackingID(), runID).Scan(&runStatus); err != nil {
		return out, err
	}
	if runStatus == "rolled_back" || runStatus == "queued" || runStatus == "running" {
		return out, fault.Conflict
	}
	// Lock the receipt before applying. Concurrent resolve/ignore requests cannot
	// both win, and a failed receipt write rolls back every library effect.
	if err = tx.QueryRow(ctx, "SELECT status FROM import_unmatched WHERE profile_id=$1 AND id=$2 FOR UPDATE", a.TrackingID(), id).Scan(&status); err != nil {
		return out, err
	}
	if status != "pending" {
		return out, &fault.Error{Status: 409, Code: "already_handled", Message: "This title was already matched or ignored."}
	}
	if _, err = s.Library.ApplyImportTx(ctx, tx, a.TrackingID(), source, in); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, "UPDATE import_unmatched SET status='resolved',catalog_item_id=$3,resolved_at=clock_timestamp() WHERE profile_id=$1 AND id=$2", a.TrackingID(), id, catalogID); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, "UPDATE import_runs SET last_applied_at=clock_timestamp() WHERE id=$1::uuid", runID); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return s.oneUnmatched(ctx, a, id)
}

// Ignore marks an unmatched title as not worth matching.
func (s Store) Ignore(ctx context.Context, a identity.Actor, id int64) (Unmatched, error) {
	if !a.Allows("library:write") {
		return Unmatched{}, fault.Forbidden
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Unmatched{}, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return Unmatched{}, err
	}
	tag, err := tx.Exec(ctx, "UPDATE import_unmatched SET status='ignored',resolved_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 AND status='pending'", a.TrackingID(), id)
	if err != nil {
		return Unmatched{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.oneUnmatched(ctx, a, id); err != nil {
			return Unmatched{}, err
		}
		return Unmatched{}, &fault.Error{Status: 409, Code: "already_handled", Message: "This title was already matched or ignored."}
	}
	if err = tx.Commit(ctx); err != nil {
		return Unmatched{}, err
	}
	return s.oneUnmatched(ctx, a, id)
}

func (s Store) oneUnmatched(ctx context.Context, a identity.Actor, id int64) (Unmatched, error) {
	var u Unmatched
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT u.id,u.run_id::text,r.source,u.label,u.kind,u.record,u.status,u.catalog_item_id,u.created_at
		FROM import_unmatched u JOIN import_runs r ON r.id=u.run_id WHERE u.profile_id=$1 AND u.id=$2`, a.TrackingID(), id).
		Scan(&u.ID, &u.RunID, &u.Source, &u.Label, &u.Kind, &raw, &u.Status, &u.CatalogID, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, fault.NotFound
	}
	var rec Record
	if json.Unmarshal(raw, &rec) == nil {
		u.Plays = len(rec.Plays)
		if rec.Video != nil {
			u.Year = rec.Video.Year
		}
	}
	return u, err
}

// Rollback undoes a finished run. See library.RollbackImport for what is kept.
func (s Store) Rollback(ctx context.Context, a identity.Actor, id string) (Run, int, int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Run{}, 0, 0, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return Run{}, 0, 0, err
	}
	r, err := scanRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM import_runs WHERE profile_id=$1 AND id=$2 FOR UPDATE", a.TrackingID(), id))
	if err != nil {
		return r, 0, 0, err
	}
	if r.Status == "queued" || r.Status == "running" || r.Status == "rolled_back" {
		return r, 0, 0, fault.Conflict
	}
	var finished time.Time
	if err = tx.QueryRow(ctx, "SELECT COALESCE(GREATEST(finished_at,last_applied_at),clock_timestamp()) FROM import_runs WHERE id=$1", id).Scan(&finished); err != nil {
		return r, 0, 0, err
	}
	entries, plays, err := s.Library.RollbackImportTx(ctx, tx, a.TrackingID(), id, finished)
	if err != nil {
		return r, 0, 0, err
	}
	if _, err = tx.Exec(ctx, "UPDATE import_runs SET status='rolled_back' WHERE id=$1", id); err != nil {
		return r, 0, 0, err
	}
	if _, err = tx.Exec(ctx, "UPDATE import_unmatched SET status='ignored',resolved_at=clock_timestamp() WHERE profile_id=$1 AND run_id=$2 AND status='pending'", a.TrackingID(), id); err != nil {
		return r, 0, 0, err
	}
	r.Status = "rolled_back"
	return r, entries, plays, tx.Commit(ctx)
}
