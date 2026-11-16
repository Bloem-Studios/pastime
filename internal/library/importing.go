package library

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// ImportItem is one title from an external service, already matched to the catalog.
type ImportItem struct {
	ScoredAt                      *time.Time
	Lists                         []string
	ListID                        int64
	ListSourceKind, ListSourceRef string
	// RunID stamps created entries and plays so the run can be rolled back.
	RunID    string
	TitleID  int64
	Status   string // empty keeps the current status
	Progress *int
	Rating   *int // 0-10
	Notes    string
	Plays    []ImportPlay
	// Copies are owned copies from a collection export, idempotent by ExternalID.
	Copies []ImportCopy
}

// ImportCopy is an owned copy from a collection app. Custom values name collection
// fields, which are created as text fields when missing.
type ImportCopy struct {
	ChildID                                           string
	CatalogID                                         *int64
	Format, Edition, Currency, Store, Location, Notes string
	AcquiredOn                                        *string
	PriceCents                                        *int
	Custom                                            map[string]string
	ExternalID                                        string
}

// ImportPlay is a dated consumption fact. ExternalID makes it idempotent: a play with
// the same source and ExternalID is recorded once however often the import runs.
type ImportPlay struct {
	TargetID   int64
	At         time.Time
	ExternalID string
	Minutes    *int           // time spent; nil takes the title's runtime
	Details    map[string]any // board game players and location
}

type ImportOutcome struct {
	Created     bool
	Changed     bool
	PlaysAdded  int
	CopiesAdded int
}

// statusRank orders statuses so an import can move an entry forward but never back.
var statusRank = map[string]int{"planned": 0, "paused": 1, "dropped": 1, "in_progress": 2, "completed": 3}

// ApplyImport merges one item into the account's library on behalf of an import run.
// It rechecks that the account is active inside the transaction, so a disabled
// account stops receiving writes mid-import.
func (s Store) ApplyImport(ctx context.Context, userID int64, source string, item ImportItem) (ImportOutcome, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ImportOutcome{}, err
	}
	defer tx.Rollback(ctx)
	out, err := s.ApplyImportTx(ctx, tx, userID, source, item)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// ApplyImportTx allows a repair receipt and its library changes to commit together.
// The caller owns the transaction; account activity is rechecked and locked here.
func (s Store) ApplyImportTx(ctx context.Context, tx pgx.Tx, userID int64, source string, item ImportItem) (ImportOutcome, error) {
	var out ImportOutcome
	if item.Status != "" && !validStatus(item.Status) {
		return out, fault.Invalid("unsupported status")
	}
	if item.Rating != nil && (*item.Rating < 0 || *item.Rating > 10) {
		item.Rating = nil
	}
	if len([]rune(item.Notes)) > 10000 {
		item.Notes = string([]rune(item.Notes)[:10000])
	}
	worker, err := identity.ResolveWorkerProfile(ctx, tx, userID, "import", []string{"library:read", "library:write"}, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.Unauthenticated
	}
	if err != nil {
		return out, err
	}
	if item.ListID > 0 {
		var kind, ref *string
		if err = tx.QueryRow(ctx, "SELECT source_kind,source_ref FROM personal_lists WHERE profile_id=$1 AND id=$2 FOR SHARE", userID, item.ListID).Scan(&kind, &ref); err != nil {
			return out, err
		}
		if kind == nil || ref == nil || *kind != item.ListSourceKind || *ref != item.ListSourceRef {
			return out, fault.Conflict
		}
	}
	if item.RunID != "" {
		var linked bool
		var runStatus string
		if err = tx.QueryRow(ctx, "SELECT COALESCE((options->>'link_id')::bigint,0)>0,status FROM import_runs WHERE id=$1 AND profile_id=$2 FOR SHARE", item.RunID, userID).Scan(&linked, &runStatus); err != nil {
			return out, err
		}
		if runStatus == "rolled_back" || runStatus == "cancelled" {
			return out, fault.Conflict
		}
		if linked {
			var generation int64
			err = tx.QueryRow(ctx, `SELECT l.generation FROM import_batches b JOIN service_links l ON l.id=b.link_id AND l.profile_id=b.profile_id AND l.generation=b.generation WHERE b.run_id=$1 AND b.profile_id=$2 FOR SHARE OF l`, item.RunID, userID).Scan(&generation)
			if errors.Is(err, pgx.ErrNoRows) {
				return out, fault.Conflict
			}
			if err != nil {
				return out, err
			}
		}
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('pastime.change_source',$1,true),set_config('pastime.import_run',$2,true)", "import:"+source, item.RunID); err != nil {
		return out, err
	}
	a := worker
	if err = lockChanges(ctx, tx, userID); err != nil {
		return out, err
	}
	var deleted bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM library_tombstones WHERE profile_id=$1 AND catalog_item_id=$2)", userID, item.TitleID).Scan(&deleted); err != nil {
		return out, err
	}
	if deleted {
		return out, nil
	}
	if item.Status == "completed" {
		var kind, production string
		if err = tx.QueryRow(ctx, "SELECT kind,release_status FROM catalog_items WHERE id=$1", item.TitleID).Scan(&kind, &production); err != nil {
			return out, err
		}
		if (kind == "series" || kind == "anime") && !terminalProduction(production) {
			item.Status = "in_progress"
		}
	}
	e, err := scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.catalog_item_id=$2 FOR UPDATE OF e", userID, item.TitleID))
	if errors.Is(err, fault.NotFound) {
		status := item.Status
		if status == "" {
			status = "planned"
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO library_entries(profile_id,title,media_type,publication_year,unit,total,status,progress,rating,notes,completed_cycles,catalog_item_id,import_run,scored_at)
			SELECT $1,left(c.title,200),c.kind,extract(year FROM c.release_date)::int,c.unit,c.total,$3,
			CASE WHEN $3='completed' AND c.total IS NOT NULL THEN c.total ELSE LEAST(COALESCE($4,0),COALESCE(c.total,100000000)) END,$5,$6,
			CASE WHEN $3='completed' THEN 1 ELSE 0 END,c.id,$7::uuid,$8
			FROM catalog_items c WHERE c.id=$2 AND c.parent_id IS NULL RETURNING id`, userID, item.TitleID, status, item.Progress, item.Rating, item.Notes, runRef(item.RunID), item.ScoredAt).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, fault.NotFound
		}
		if err != nil {
			return out, err
		}
		if err = event(ctx, tx, a, id, "added"); err != nil {
			return out, err
		}
		out.Created, out.Changed = true, true
		if e, err = scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2", userID, id)); err != nil {
			return out, err
		}
	} else if err != nil {
		return out, err
	} else {
		status, progress, rating, notes := e.Status, e.Progress, e.Rating, e.Notes
		if status != "paused" && status != "dropped" && status != "rated" && item.Status != "" && statusRank[item.Status] > statusRank[status] {
			status = item.Status
		}
		if item.Progress != nil && *item.Progress > progress {
			progress = *item.Progress
		}
		if e.Total != nil && progress > *e.Total {
			progress = *e.Total
		}
		if status == "completed" && e.Total != nil {
			progress = *e.Total
		}
		if rating == nil && e.ScoredAt == nil && item.Rating != nil {
			rating = item.Rating
		}
		if strings.TrimSpace(notes) == "" && item.Notes != "" {
			notes = item.Notes
		}
		if status != e.Status || progress != e.Progress || !sameInt(rating, e.Rating) || notes != e.Notes {
			if _, err = tx.Exec(ctx, `UPDATE library_entries SET status=$3,progress=$4,rating=$5,notes=$6,scored_at=CASE WHEN rating IS NULL AND scored_at IS NULL AND $5::integer IS NOT NULL THEN $8 ELSE scored_at END,completed_cycles=completed_cycles+$7,version=version+1,updated_at=clock_timestamp()
				WHERE profile_id=$1 AND id=$2`, userID, e.ID, status, progress, rating, notes, completionDelta(e.Status, status), item.ScoredAt); err != nil {
				return out, err
			}
			if err = event(ctx, tx, a, e.ID, "updated"); err != nil {
				return out, err
			}
			out.Changed = true
			if e, err = scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2", userID, e.ID)); err != nil {
				return out, err
			}
		}
	}
	if len(item.Lists) > 100 {
		return out, fault.Invalid("Too many imported lists.")
	}
	for _, name := range item.Lists {
		name = strings.TrimSpace(name)
		if name == "" || len([]rune(name)) > 80 {
			return out, fault.Invalid("Invalid imported list name.")
		}
		var listID int64
		err = tx.QueryRow(ctx, "SELECT id FROM personal_lists WHERE profile_id=$1 AND name=$2 AND kind='manual' AND visibility='private' AND source_kind IS NULL ORDER BY id LIMIT 1", userID, name).Scan(&listID)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, "INSERT INTO personal_lists(profile_id,name) SELECT $1,$2 WHERE (SELECT count(*) FROM personal_lists WHERE profile_id=$1)<1000 RETURNING id", userID, name).Scan(&listID)
		}
		if err != nil {
			return out, err
		}
		membership, err := tx.Exec(ctx, "INSERT INTO list_entries(profile_id,list_id,entry_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", userID, listID, e.ID)
		if err != nil {
			return out, err
		}
		if membership.RowsAffected() > 0 {
			out.Changed = true
			if _, err = tx.Exec(ctx, "UPDATE personal_lists SET version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2", userID, listID); err != nil {
				return out, err
			}
		}
	}
	if len(item.Plays) > 0 {
		// Plays must belong to this title's own tree.
		ids := make([]int64, len(item.Plays))
		for i, p := range item.Plays {
			ids[i] = p.TargetID
		}
		var valid int
		if err = tx.QueryRow(ctx, `WITH RECURSIVE tree AS (SELECT id FROM catalog_items WHERE id=$1
			UNION ALL SELECT c.id FROM catalog_items c JOIN tree t ON c.parent_id=t.id)
			SELECT count(DISTINCT id) FROM tree WHERE id=ANY($2)`, item.TitleID, ids).Scan(&valid); err != nil {
			return out, err
		}
		distinct := map[int64]bool{}
		for _, id := range ids {
			distinct[id] = true
		}
		if valid != len(distinct) {
			return out, fault.Invalid("a play targets a record outside the imported title")
		}
		batch := &pgx.Batch{}
		for _, p := range item.Plays {
			// The same viewing reported by two services is recorded once: a play of the same
			// item within the scrobble window, or, for podcast episodes, any play in this
			// cycle, since services often report only that an episode was finished.
			var details []byte
			if len(p.Details) > 0 {
				b, err := json.Marshal(p.Details)
				if err != nil {
					return out, err
				}
				details = b
			}
			batch.Queue(`INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,source,external_id,import_run,minutes,details)
				SELECT $1,$2,$3,$4,$5,$6,$7,$8::uuid,$10,$11 WHERE NOT EXISTS(SELECT 1 FROM plays x JOIN catalog_items c ON c.id=x.catalog_item_id
					WHERE x.profile_id=$1 AND x.entry_id=$2 AND x.catalog_item_id=$3 AND x.cycle=$4 AND x.source<>$6
					AND (c.kind='podcast_episode' OR x.played_at BETWEEN $5::timestamptz-make_interval(secs=>$9) AND $5::timestamptz+make_interval(secs=>$9)))
				ON CONFLICT (profile_id,source,external_id) WHERE external_id IS NOT NULL DO NOTHING`, userID, e.ID, p.TargetID, e.Cycle, p.At, source, p.ExternalID, runRef(item.RunID), ScrobbleWindow.Seconds(), p.Minutes, details)
		}
		results := tx.SendBatch(ctx, batch)
		for range item.Plays {
			tag, err := results.Exec()
			if err != nil {
				results.Close()
				return out, err
			}
			out.PlaysAdded += int(tag.RowsAffected())
		}
		if err = results.Close(); err != nil {
			return out, err
		}
		if out.PlaysAdded > 0 {
			if _, err = s.reproject(ctx, tx, a, e, "played"); err != nil {
				return out, err
			}
			out.Changed = true
		}
	}
	for _, c := range item.Copies {
		added, err := importCopy(ctx, tx, userID, e.ID, source, c)
		if err != nil {
			return out, err
		}
		if added {
			out.CopiesAdded++
			out.Changed = true
		}
	}
	if out.Created && out.PlaysAdded == 0 {
		if err = refreshNext(ctx, tx, userID, e.ID); err != nil {
			return out, err
		}
	}
	return out, nil
}

func sameInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func runRef(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// RollbackImport removes what one import run added: its plays, and the entries it
// created unless the person has since done something with them. Status or rating
// upgrades the run made to existing entries are kept, because later edits may depend
// on them. Returns the entries removed and the plays removed.
func (s Store) RollbackImport(ctx context.Context, userID int64, runID string, finishedAt time.Time) (int, int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	entries, plays, err := s.RollbackImportTx(ctx, tx, userID, runID, finishedAt)
	if err != nil {
		return 0, 0, err
	}
	return entries, plays, tx.Commit(ctx)
}

func (s Store) RollbackImportTx(ctx context.Context, tx pgx.Tx, userID int64, runID string, finishedAt time.Time) (int, int, error) {
	worker, err := identity.ResolveWorkerProfile(ctx, tx, userID, "import", []string{"library:read", "library:write"}, true)
	if err != nil {
		return 0, 0, fault.Unauthenticated
	}
	a := worker
	if err = lockChanges(ctx, tx, userID); err != nil {
		return 0, 0, err
	}
	var touched []int64
	rows, err := tx.Query(ctx, "DELETE FROM plays WHERE profile_id=$1 AND import_run=$2::uuid RETURNING entry_id", userID, runID)
	if err != nil {
		return 0, 0, err
	}
	plays := 0
	seen := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		plays++
		if !seen[id] {
			seen[id] = true
			touched = append(touched, id)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, 0, err
	}
	// An entry the run created goes, unless the person acted on it after the run.
	tag, err := tx.Exec(ctx, `DELETE FROM library_entries e WHERE e.profile_id=$1 AND e.import_run=$2::uuid
		AND NOT EXISTS(SELECT 1 FROM activity_events a WHERE a.profile_id=e.profile_id AND a.entry_id=e.id AND a.created_at > $3)
		AND NOT EXISTS(SELECT 1 FROM plays p WHERE p.profile_id=e.profile_id AND p.entry_id=e.id)`, userID, runID, finishedAt)
	if err != nil {
		return 0, 0, err
	}
	for _, id := range touched {
		e, err := scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2 FOR UPDATE OF e", userID, id))
		if errors.Is(err, fault.NotFound) {
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		if e.CatalogID != nil {
			if _, err = s.reproject(ctx, tx, a, e, "unplayed"); err != nil {
				return 0, 0, err
			}
		}
	}
	return int(tag.RowsAffected()), plays, nil
}

var copyFormats = map[string]bool{"digital": true, "uhd_bluray": true, "bluray": true, "dvd": true, "vhs": true, "hardcover": true, "paperback": true, "ebook": true,
	"audiobook": true, "vinyl": true, "cd": true, "cassette": true, "game_physical": true, "game_digital": true, "boxed": true, "other": true}

func importCopy(ctx context.Context, tx pgx.Tx, userID, entryID int64, source string, c ImportCopy) (bool, error) {
	if c.ExternalID == "" || len(c.ExternalID) > 200 {
		return false, nil
	}
	if !copyFormats[c.Format] {
		c.Format = "other"
	}
	cut := func(v string, n int) string {
		if r := []rune(strings.TrimSpace(v)); len(r) > n {
			return string(r[:n])
		}
		return strings.TrimSpace(v)
	}
	if c.Currency != "" && (len(c.Currency) != 3 || strings.ToUpper(c.Currency) != c.Currency) {
		c.Currency = ""
	}
	if c.AcquiredOn != nil {
		if _, err := time.Parse("2006-01-02", *c.AcquiredOn); err != nil {
			c.AcquiredOn = nil
		}
	}
	if c.PriceCents != nil && (*c.PriceCents < 0 || *c.PriceCents > 100000000) {
		c.PriceCents = nil
	}
	custom := map[string]string{}
	for name, value := range c.Custom {
		name, value = cut(name, 60), cut(value, 500)
		if name == "" || value == "" || len(custom) >= 20 {
			continue
		}
		// Fields are created on first use, up to the per-account limit.
		if _, err := tx.Exec(ctx, `INSERT INTO collection_fields(profile_id,name,kind,position)
			SELECT $1,$2,'text',COALESCE((SELECT max(position)+1 FROM collection_fields WHERE profile_id=$1),0)
			WHERE (SELECT count(*) FROM collection_fields WHERE profile_id=$1)<100 AND NOT EXISTS(SELECT 1 FROM collection_fields WHERE profile_id=$1 AND lower(name)=lower($2))`, userID, name); err != nil {
			return false, err
		}
		var field string
		err := tx.QueryRow(ctx, "SELECT name FROM collection_fields WHERE profile_id=$1 AND lower(name)=lower($2)", userID, name).Scan(&field)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		custom[field] = value
	}
	var count int
	// Ownership indexes (Mylar3, Kapowarr) hold an issue per copy, beyond the usual cap.
	if source != "mylar3" && source != "kapowarr" {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM owned_copies WHERE profile_id=$1 AND entry_id=$2", userID, entryID).Scan(&count); err != nil || count >= 100 {
			return false, err
		}
	}
	tag, err := tx.Exec(ctx, `INSERT INTO owned_copies(profile_id,entry_id,format,edition,acquired_on,price_cents,currency,store,location,notes,custom,source,external_id,catalog_item_id)
		VALUES($1,$2,$3,$4,$5::date,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (profile_id,source,external_id) WHERE external_id IS NOT NULL DO NOTHING`,
		userID, entryID, c.Format, cut(c.Edition, 200), c.AcquiredOn, c.PriceCents, c.Currency, cut(c.Store, 200), cut(c.Location, 200), cut(c.Notes, 2000), custom, source, c.ExternalID, c.CatalogID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
