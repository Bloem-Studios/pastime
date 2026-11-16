package library

import (
	"context"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// ExportEntry is one library entry with its catalog identity and every play.
type ExportEntry struct {
	ID         int64
	Provider   string
	ProviderID string
	Kind       string
	Title      string
	Year       *int
	Status     string
	Progress   int
	Total      *int
	Unit       string
	Rating     *int
	Notes      string
	Cycle      int
	Added      time.Time
	Plays      []ExportPlay
}

type ExportPlay struct {
	ID       int64
	At       time.Time
	Cycle    int
	Season   *int
	Episode  *int
	ChildID  string // provider id of a podcast episode or comic issue
	TitleOwn bool   // the play is of the entry's own record
}

const exportBatch = 500

// Export streams every entry in id order from one repeatable-read snapshot. Entries are
// read 500 at a time and their plays with one range query per batch, so memory is
// bounded by a batch; unusually dense histories fail explicitly above 100,000 plays per batch.
func (s Store) Export(ctx context.Context, a identity.Actor, emit func(ExportEntry) error) error {
	if !a.Allows("library:read") {
		return fault.Forbidden
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout = '300s'"); err != nil {
		return err
	}
	var lastID int64
	for {
		rows, err := tx.Query(ctx, `SELECT e.id,COALESCE(c.provider,''),COALESCE(c.provider_id,''),COALESCE(c.kind,e.media_type),e.title,e.publication_year,
			e.status,e.progress,e.total,e.unit,e.rating,e.notes,e.cycle,e.created_at
			FROM library_entries e LEFT JOIN catalog_items c ON c.id=e.catalog_item_id WHERE e.profile_id=$1 AND e.id>$2 ORDER BY e.id LIMIT $3`, a.TrackingID(), lastID, exportBatch)
		if err != nil {
			return err
		}
		var batch []ExportEntry
		for rows.Next() {
			var e ExportEntry
			if err = rows.Scan(&e.ID, &e.Provider, &e.ProviderID, &e.Kind, &e.Title, &e.Year, &e.Status, &e.Progress, &e.Total, &e.Unit, &e.Rating, &e.Notes, &e.Cycle, &e.Added); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		index := make(map[int64]int, len(batch))
		for i, e := range batch {
			index[e.ID] = i
		}
		plays, err := tx.Query(ctx, `SELECT p.entry_id,p.id,p.played_at,p.cycle,c.season_number,c.episode_number,
			CASE WHEN c.kind IN ('podcast_episode','comic_issue') THEN c.provider_id ELSE '' END,
			p.catalog_item_id=e.catalog_item_id
			FROM plays p JOIN library_entries e ON e.profile_id=p.profile_id AND e.id=p.entry_id JOIN catalog_items c ON c.id=p.catalog_item_id
			WHERE p.profile_id=$1 AND p.entry_id>$2 AND p.entry_id<=$3 ORDER BY p.entry_id,p.played_at,p.id`, a.TrackingID(), lastID, batch[len(batch)-1].ID)
		if err != nil {
			return err
		}
		playCount := 0
		for plays.Next() {
			playCount++
			if playCount > 100000 {
				plays.Close()
				return fault.Invalid("This export batch exceeds 100,000 plays. Use the native streaming format.")
			}
			var entry int64
			var p ExportPlay
			if err = plays.Scan(&entry, &p.ID, &p.At, &p.Cycle, &p.Season, &p.Episode, &p.ChildID, &p.TitleOwn); err != nil {
				plays.Close()
				return err
			}
			if i, ok := index[entry]; ok {
				batch[i].Plays = append(batch[i].Plays, p)
			}
		}
		plays.Close()
		if err = plays.Err(); err != nil {
			return err
		}
		for _, e := range batch {
			if err = emit(e); err != nil {
				return err
			}
		}
		lastID = batch[len(batch)-1].ID
		if len(batch) < exportBatch {
			break
		}
	}
	return tx.Commit(ctx)
}
