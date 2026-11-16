package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
	// Cache keeps expensive per-person results such as statistics; nil disables it.
	Cache Cache
}

// Cache is disposable shared state; a miss or failure only costs recomputation.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
}

const columns = `e.id,e.title,e.media_type,e.publication_year,e.unit,e.total,e.status,e.progress,e.rating,e.cycle,e.completed_cycles,e.version,e.created_at,e.updated_at,
e.catalog_item_id,COALESCE(NULLIF(e.image_override,''),c.image_url,''),COALESCE(c.backdrop_url,''),COALESCE(c.original_title,''),e.started_at,e.finished_at,e.last_activity_at,
(SELECT c.details->'titles'->>(p.prefs->>'title_language') FROM user_preferences p WHERE p.profile_id=e.profile_id AND length(p.prefs->>'title_language')=2),e.scored_at,` + lastPlayedSQL

// lastPlayedSQL is an entry's latest real play. A bulk "mark as played" is not a viewing.
const lastPlayedSQL = `(SELECT max(lp.played_at) FROM plays lp WHERE lp.profile_id=e.profile_id AND lp.entry_id=e.id AND NOT lp.marked)`

// entryColumns adds the private and per-entry detail of one item to columns.
const entryColumns = columns + `,e.notes,e.edition,e.episode_order,
(SELECT json_build_object('catalog_id',n.id::text,'season_number',n.season_number,'episode_number',n.episode_number,'title',n.title,'release_date',n.release_date,'runtime_minutes',n.runtime_minutes)
FROM catalog_items n WHERE n.id=e.next_item_id)`

// from joins shared artwork by primary key; private columns come only from the entry.
const from = " FROM library_entries e LEFT JOIN catalog_items c ON c.id=e.catalog_item_id"

func fields(e *Summary) []any {
	return []any{&e.ID, &e.Title, &e.MediaType, &e.Year, &e.Unit, &e.Total, &e.Status, &e.Progress, &e.Rating, &e.Cycle, &e.CompletedCycles, &e.Version, &e.CreatedAt, &e.UpdatedAt,
		&e.CatalogID, &e.ImageURL, &e.BackdropURL, &e.OriginalTitle, &e.StartedAt, &e.FinishedAt, &e.LastActivityAt, &e.LocalizedTitle, &e.ScoredAt, &e.LastPlayedAt}
}
func scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	err := row.Scan(append(fields(&e.Summary), &e.Notes, &e.Edition, &e.EpisodeOrder, &e.NextItem)...)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fault.NotFound
	}
	return e, err
}

func (s Store) Get(ctx context.Context, a identity.Actor, id int64) (Entry, error) {
	if !a.Allows("library:read") {
		return Entry{}, fault.Forbidden
	}
	return scanEntry(s.Pool.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2", a.TrackingID(), id))
}
func (s Store) Stats(ctx context.Context, a identity.Actor) (Stats, error) {
	var out Stats
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	err := s.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='in_progress'),count(*) FILTER(WHERE status='planned'),count(*) FILTER(WHERE status='completed'),COALESCE(sum(completed_cycles),0) FROM library_entries WHERE profile_id=$1`, a.TrackingID()).Scan(&out.Total, &out.InProgress, &out.Planned, &out.Completed, &out.CompletedCycles)
	return out, err
}
func (s Store) Activity(ctx context.Context, a identity.Actor, entryID int64, cursor string, limit int) (page.Result[Activity], error) {
	out := page.Result[Activity]{Items: []Activity{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := "activity|" + strconv.FormatInt(a.TrackingID(), 10) + "|" + strconv.FormatInt(entryID, 10)
	last, err := page.Decode(scope, cursor)
	if err != nil {
		return out, err
	}
	query := `SELECT a.id,a.entry_id,e.title,e.media_type,a.kind,a.status,a.progress,a.unit,a.cycle,a.created_at,
		COALESCE(NULLIF(e.image_override,''),c.image_url,''),(SELECT c.details->'titles'->>(p.prefs->>'title_language') FROM user_preferences p WHERE p.profile_id=e.profile_id AND length(p.prefs->>'title_language')=2),
		a.import_run::text,r.source
		FROM activity_events a JOIN library_entries e ON e.id=a.entry_id AND e.profile_id=a.profile_id LEFT JOIN catalog_items c ON c.id=e.catalog_item_id
		LEFT JOIN import_runs r ON r.id=a.import_run AND r.profile_id=a.profile_id WHERE a.profile_id=$1`
	args := []any{a.TrackingID()}
	if entryID > 0 {
		args = append(args, entryID)
		query += " AND a.entry_id=$" + strconv.Itoa(len(args))
	}
	if last > 0 {
		args = append(args, last)
		query += " AND a.id<$" + strconv.Itoa(len(args))
	}
	args = append(args, limit+1)
	query += " ORDER BY a.id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Activity
		if err = rows.Scan(&v.ID, &v.EntryID, &v.Title, &v.MediaType, &v.Kind, &v.Status, &v.Progress, &v.Unit, &v.Cycle, &v.CreatedAt, &v.ImageURL, &v.LocalizedTitle, &v.ImportID, &v.ImportSource); err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode(scope, out.Items[len(out.Items)-1].ID)
		out.NextCursor = &next
	}
	return out, nil
}

// mutate records only an acknowledgement and intent hash, never a snapshot of private notes.
// Authority is rechecked before consulting receipts; disabled/revoked callers cannot replay.
func (s Store) mutate(ctx context.Context, a identity.Actor, scope, key, operation string, input any, apply func(pgx.Tx, identity.Actor) (Mutation, error)) (Mutation, error) {
	if !identity.ValidUUID(key) {
		return Mutation{}, fault.Invalid("Provide a UUID Idempotency-Key header.")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Mutation{}, err
	}
	h := sha256.New()
	h.Write([]byte(operation))
	h.Write([]byte{0})
	h.Write(body)
	intent := h.Sum(nil)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Mutation{}, err
	}
	defer tx.Rollback(ctx)
	a, err = identity.AuthorizeTx(ctx, tx, a, scope)
	if err != nil {
		return Mutation{}, err
	}
	if err = lockChanges(ctx, tx, a.TrackingID()); err != nil {
		return Mutation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mutation_receipts(profile_id,idempotency_key,intent_hash) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, a.TrackingID(), key, intent); err != nil {
		return Mutation{}, err
	}
	var storedHash, receipt []byte
	if err = tx.QueryRow(ctx, "SELECT intent_hash,response FROM mutation_receipts WHERE profile_id=$1 AND idempotency_key=$2 FOR UPDATE", a.TrackingID(), key).Scan(&storedHash, &receipt); err != nil {
		return Mutation{}, err
	}
	if !bytes.Equal(storedHash, intent) {
		return Mutation{}, fault.Conflict
	}
	if len(receipt) > 0 {
		var saved mutationReceipt
		if err = json.Unmarshal(receipt, &saved); err != nil {
			return Mutation{}, err
		}
		out := saved.Mutation
		out.RemapMoved = saved.Moved
		out.RemapDropped = saved.Dropped
		out.RemapRetained = saved.Retained
		out.Replayed = true
		return out, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('pastime.user_mutation','true',true),set_config('pastime.change_source',$1,true)", "user:"+operation); err != nil {
		return Mutation{}, err
	}
	out, err := apply(tx, a)
	if err != nil {
		return out, err
	}
	receipt, err = json.Marshal(mutationReceipt{Mutation: out, Moved: out.RemapMoved, Dropped: out.RemapDropped, Retained: out.RemapRetained})
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, "UPDATE mutation_receipts SET response=$3 WHERE profile_id=$1 AND idempotency_key=$2", a.TrackingID(), key, receipt); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// Receipt-only counters are not added to unrelated public mutation responses.
type mutationReceipt struct {
	Retained int `json:"remap_retained"`
	Mutation
	Moved   int `json:"remap_moved,omitempty"`
	Dropped int `json:"remap_dropped,omitempty"`
}

// Match the feed trigger's lock before taking entry locks in multi-row workflows.
func lockChanges(ctx context.Context, tx pgx.Tx, userID int64) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('pastime.change.' || $1::bigint::text,0))", userID)
	return err
}
