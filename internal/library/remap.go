package library

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// SetArtwork replaces the cover shown for one of the caller's titles, or restores the
// catalog artwork with an empty URL. Only HTTPS images are accepted.
func (s Store) SetArtwork(ctx context.Context, a identity.Actor, entryID int64, key string, version int, imageURL string) (Mutation, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL != "" && (!strings.HasPrefix(imageURL, "https://") || len(imageURL) > 1000 || strings.ContainsAny(imageURL, " \"'<>")) {
		return Mutation{}, fault.Invalid("Use an https:// image address of at most 1,000 characters.")
	}
	input := struct {
		Version int
		URL     string
	}{version, imageURL}
	return s.mutate(ctx, a, "library:write", key, "entry.artwork/"+strconv.FormatInt(entryID, 10), input, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		if _, err := current(ctx, tx, a, entryID, version); err != nil {
			return Mutation{}, err
		}
		out := Mutation{ID: entryID}
		err := tx.QueryRow(ctx, "UPDATE library_entries SET image_override=$3,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version", a.TrackingID(), entryID, imageURL).Scan(&out.Version)
		return out, err
	})
}

// RemapResult reports what could not follow the title to its new catalog record.
type RemapResult struct {
	Mutation
	PlaysMoved    int `json:"plays_moved"`
	PlaysDropped  int `json:"plays_dropped"`
	PlaysRetained int `json:"plays_retained"`
}

// Remap points a tracked title at a different catalog record: the right edition, the
// right show when an import matched the wrong one, or another provider's record. Plays,
// skips and owned-copy scopes follow by season and episode number; plays with no
// counterpart retain their original identity and remain visible in history.
func (s Store) Remap(ctx context.Context, a identity.Actor, entryID int64, key string, version int, catalogID int64) (RemapResult, error) {
	var out RemapResult
	input := struct {
		Version int
		Catalog int64
	}{version, catalogID}
	m, err := s.mutate(ctx, a, "library:write", key, "entry.remap/"+strconv.FormatInt(entryID, 10), input, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		e, err := current(ctx, tx, a, entryID, version)
		if err != nil {
			return Mutation{}, err
		}
		if e.CatalogID == nil {
			return Mutation{}, fault.Invalid("Only titles from the catalog can be remapped.")
		}
		if *e.CatalogID == catalogID {
			return Mutation{ID: entryID, Version: e.Version}, nil
		}
		var oldKind, newKind string
		var parent *int64
		err = tx.QueryRow(ctx, "SELECT kind,parent_id FROM catalog_items WHERE id=$1", catalogID).Scan(&newKind, &parent)
		if errors.Is(err, pgx.ErrNoRows) {
			return Mutation{}, fault.NotFound
		}
		if err != nil {
			return Mutation{}, err
		}
		if err = tx.QueryRow(ctx, "SELECT kind FROM catalog_items WHERE id=$1", *e.CatalogID).Scan(&oldKind); err != nil {
			return Mutation{}, err
		}
		// Plays and daily statistics are counted under the entry's media type, so a title
		// can only move to a record of the same kind.
		if parent != nil || oldKind != newKind {
			return Mutation{}, fault.Invalid("Choose a whole title of the same kind of media.")
		}
		var taken bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2)", a.TrackingID(), catalogID).Scan(&taken); err != nil {
			return Mutation{}, err
		}
		if taken {
			return Mutation{}, &fault.Error{Status: 409, Code: "already_tracked", Message: "That title is already in your library."}
		}
		// Map every old record to its counterpart: the title itself, then parts by
		// season and number.
		tag, err := tx.Exec(ctx, remapMapping+` UPDATE plays p SET catalog_item_id=m.new_id FROM m WHERE p.profile_id=$1 AND p.entry_id=$4 AND p.catalog_item_id=m.old_id`, a.TrackingID(), *e.CatalogID, catalogID, entryID)
		if err != nil {
			return Mutation{}, err
		}
		out.PlaysMoved = int(tag.RowsAffected())
		if _, err = tx.Exec(ctx, `UPDATE plays p SET unmapped=(c.root_id<>$3) FROM catalog_items c WHERE p.profile_id=$1 AND p.entry_id=$2 AND c.id=p.catalog_item_id`, a.TrackingID(), entryID, catalogID); err != nil {
			return Mutation{}, err
		}
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id=$1 AND entry_id=$2 AND unmapped", a.TrackingID(), entryID).Scan(&out.PlaysRetained); err != nil {
			return Mutation{}, err
		}
		if _, err = tx.Exec(ctx, remapMapping+` UPDATE skipped_items k SET catalog_item_id=m.new_id FROM m WHERE k.profile_id=$1 AND k.entry_id=$4 AND k.catalog_item_id=m.old_id
			AND NOT EXISTS(SELECT 1 FROM skipped_items x WHERE x.profile_id=k.profile_id AND x.entry_id=k.entry_id AND x.cycle=k.cycle AND x.catalog_item_id=m.new_id)`, a.TrackingID(), *e.CatalogID, catalogID, entryID); err != nil {
			return Mutation{}, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM skipped_items k USING catalog_items c WHERE k.profile_id=$1 AND k.entry_id=$2 AND c.id=k.catalog_item_id AND c.root_id<>$3`, a.TrackingID(), entryID, catalogID); err != nil {
			return Mutation{}, err
		}
		// Episode ratings follow their episodes; a rating without a counterpart is dropped,
		// as the preview reports.
		if _, err = tx.Exec(ctx, remapMapping+` UPDATE episode_ratings r SET catalog_item_id=m.new_id FROM m WHERE r.profile_id=$1 AND r.entry_id=$4 AND r.catalog_item_id=m.old_id AND m.old_id<>m.new_id`, a.TrackingID(), *e.CatalogID, catalogID, entryID); err != nil {
			return Mutation{}, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM episode_ratings r USING catalog_items c WHERE r.profile_id=$1 AND r.entry_id=$2 AND c.id=r.catalog_item_id AND c.root_id<>$3`, a.TrackingID(), entryID, catalogID); err != nil {
			return Mutation{}, err
		}
		if _, err = tx.Exec(ctx, remapMapping+` UPDATE owned_copies o SET catalog_item_id=m.new_id FROM m WHERE o.profile_id=$1 AND o.entry_id=$4 AND o.catalog_item_id=m.old_id`, a.TrackingID(), *e.CatalogID, catalogID, entryID); err != nil {
			return Mutation{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE owned_copies o SET catalog_item_id=NULL FROM catalog_items c WHERE o.profile_id=$1 AND o.entry_id=$2 AND c.id=o.catalog_item_id AND c.root_id<>$3`, a.TrackingID(), entryID, catalogID); err != nil {
			return Mutation{}, err
		}
		// Progress moves with the total in one statement so the completion checks hold.
		if _, err = tx.Exec(ctx, `UPDATE library_entries e SET catalog_item_id=c.id,title=left(c.title,200),publication_year=extract(year FROM c.release_date)::int,
			unit=c.unit,total=c.total,edition=NULL,episode_order=NULL,
			progress=CASE WHEN c.total IS NULL THEN e.progress WHEN e.status='completed' THEN c.total ELSE LEAST(e.progress,c.total) END,
			version=version+1,updated_at=clock_timestamp() FROM catalog_items c WHERE e.profile_id=$1 AND e.id=$2 AND c.id=$3`, a.TrackingID(), entryID, catalogID); err != nil {
			return Mutation{}, err
		}
		fresh, err := scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2 FOR UPDATE OF e", a.TrackingID(), entryID))
		if err != nil {
			return Mutation{}, err
		}
		m, err := s.reproject(ctx, tx, a, fresh, "updated")
		m.RemapMoved, m.RemapDropped, m.RemapRetained = out.PlaysMoved, out.PlaysDropped, out.PlaysRetained
		return m, err
	})
	out.Mutation = m
	out.PlaysMoved, out.PlaysDropped, out.PlaysRetained = m.RemapMoved, m.RemapDropped, m.RemapRetained
	return out, err
}

// SetEdition records the edition a person reads, or clears it with nil. A known page
// count becomes the title's total; progress is kept within it, and a finished title
// stays finished.
func (s Store) SetEdition(ctx context.Context, a identity.Actor, entryID int64, key string, version int, ed *Edition) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "entry.edition/"+strconv.FormatInt(entryID, 10), map[string]any{"version": version, "edition": ed}, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		old, err := current(ctx, tx, a, entryID, version)
		if err != nil {
			return Mutation{}, err
		}
		if old.CatalogID == nil || (old.MediaType != "book" && old.MediaType != "audiobook") {
			return Mutation{}, fault.Invalid("Editions apply to books and audiobooks from the catalog.")
		}
		out := Mutation{ID: entryID}
		if ed == nil {
			err = tx.QueryRow(ctx, `UPDATE library_entries e SET edition=NULL,
				image_override=CASE WHEN e.image_override=COALESCE(e.edition->>'image_url','') THEN '' ELSE e.image_override END,
				total=CASE WHEN e.unit='pages' THEN c.total ELSE e.total END,
				progress=CASE WHEN e.status='completed' AND e.unit='pages' AND c.total IS NOT NULL THEN c.total WHEN e.unit='pages' AND c.total IS NOT NULL THEN LEAST(e.progress,c.total) ELSE e.progress END,
				version=e.version+1,updated_at=clock_timestamp()
				FROM catalog_items c WHERE c.id=e.catalog_item_id AND e.profile_id=$1 AND e.id=$2 RETURNING e.version`, a.TrackingID(), entryID).Scan(&out.Version)
			return out, err
		}
		pages := ed.Pages
		if pages != nil && (*pages < 1 || *pages > 100000 || old.Unit != "pages") {
			pages = nil
		}
		err = tx.QueryRow(ctx, `UPDATE library_entries SET edition=$3,
			image_override=CASE WHEN $4<>'' THEN $4 ELSE image_override END,
			total=COALESCE($5,total),
			progress=CASE WHEN $5::int IS NULL THEN progress WHEN status='completed' THEN $5 ELSE LEAST(progress,$5) END,
			version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version`, a.TrackingID(), entryID, ed, ed.ImageURL, pages).Scan(&out.Version)
		return out, err
	})
}

// SetEpisodeOrder chooses an alternate episode order for a show, or the aired order with
// nil. The order must already be loaded for the entry's show.
func (s Store) SetEpisodeOrder(ctx context.Context, a identity.Actor, entryID int64, key string, version int, orderID *string) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "entry.episode_order/"+strconv.FormatInt(entryID, 10), map[string]any{"version": version, "order": orderID}, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		old, err := current(ctx, tx, a, entryID, version)
		if err != nil {
			return Mutation{}, err
		}
		if old.CatalogID == nil || old.MediaType != "series" {
			return Mutation{}, fault.Invalid("Episode orders apply to shows from the catalog.")
		}
		if orderID != nil {
			var ok bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM episode_orders WHERE id=$1 AND show_id=$2)", *orderID, *old.CatalogID).Scan(&ok); err != nil {
				return Mutation{}, err
			}
			if !ok {
				return Mutation{}, fault.Invalid("Load one of this show's episode orders first.")
			}
		}
		out := Mutation{ID: entryID}
		if err = tx.QueryRow(ctx, "UPDATE library_entries SET episode_order=$3,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version", a.TrackingID(), entryID, orderID).Scan(&out.Version); err != nil {
			return out, err
		}
		return out, refreshNext(ctx, tx, a.TrackingID(), entryID)
	})
}

// Only one-to-one candidates may move. Missing TV coordinates never join other
// missing coordinates; named tracks/episodes must be unique on both sides.
const remapMapping = `WITH candidates AS (
 SELECT o.id old_id,n.id new_id FROM catalog_items o JOIN catalog_items n ON n.root_id=$3 AND n.kind=o.kind
 WHERE o.root_id=$2 AND o.id<>$2 AND (
  (o.kind='episode' AND o.season_number IS NOT NULL AND o.episode_number IS NOT NULL AND n.season_number=o.season_number AND n.episode_number=o.episode_number)
  OR (o.kind='season' AND o.season_number IS NOT NULL AND n.season_number=o.season_number)
  OR (o.kind IN ('track','podcast_episode','comic_issue') AND lower(btrim(o.title))=lower(btrim(n.title)) AND btrim(o.title)<>'')
 )), unique_candidates AS (SELECT *,count(*) OVER(PARTITION BY old_id) old_count,count(*) OVER(PARTITION BY new_id) new_count FROM candidates),
 m AS (SELECT $2::bigint old_id,$3::bigint new_id UNION ALL SELECT old_id,new_id FROM unique_candidates WHERE old_count=1 AND new_count=1)`

// RemapPreview counts effects without changing the library. The version must be
// reused for the subsequent command; changes after preview cause a conflict.
type RemapPreview struct {
	Version       int `json:"version"`
	PlaysMoved    int `json:"plays_moved"`
	PlaysRetained int `json:"plays_retained"`
	// RatingsMoved and RatingsDropped count episode ratings; the title's own rating,
	// notes and status always stay with the entry.
	RatingsMoved   int `json:"ratings_moved"`
	RatingsDropped int `json:"ratings_dropped"`
	// Missing lists tracked episodes the new record has no counterpart for (at most 50).
	Missing []MissingEpisode `json:"missing"`
}

// MissingEpisode is a tracked episode without a counterpart on the new record.
type MissingEpisode struct {
	Season  *int   `json:"season"`
	Episode *int   `json:"episode"`
	Title   string `json:"title"`
}

// RemapSeasons lists the seasons an entry has episode history or ratings in, so the
// new record's episodes for them can be loaded before matching.
func (s Store) RemapSeasons(ctx context.Context, a identity.Actor, entryID int64) ([]int, error) {
	if !a.Allows("library:read") {
		return nil, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT c.season_number FROM catalog_items c WHERE c.season_number IS NOT NULL AND c.kind='episode' AND c.id IN (
		SELECT catalog_item_id FROM plays WHERE profile_id=$1 AND entry_id=$2
		UNION SELECT catalog_item_id FROM episode_ratings WHERE profile_id=$1 AND entry_id=$2
		UNION SELECT catalog_item_id FROM skipped_items WHERE profile_id=$1 AND entry_id=$2) ORDER BY 1 LIMIT 200`, a.TrackingID(), entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var n int
		if err = rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s Store) PreviewRemap(ctx context.Context, a identity.Actor, entryID int64, version int, catalogID int64) (RemapPreview, error) {
	var out RemapPreview
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return out, err
	}
	e, err := current(ctx, tx, a, entryID, version)
	if err != nil {
		return out, err
	}
	if e.CatalogID == nil {
		return out, fault.Invalid("Only titles from the catalog can be remapped.")
	}
	var valid, taken bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_items c JOIN catalog_items old ON old.id=$1 WHERE c.id=$2 AND c.parent_id IS NULL AND c.kind=old.kind),
 EXISTS(SELECT 1 FROM library_entries WHERE profile_id=$3 AND catalog_item_id=$2 AND id<>$4)`, *e.CatalogID, catalogID, a.TrackingID(), entryID).Scan(&valid, &taken)
	if err != nil {
		return out, err
	}
	if !valid {
		return out, fault.Invalid("Choose a whole title of the same kind of media.")
	}
	if taken {
		return out, &fault.Error{Status: 409, Code: "already_tracked", Message: "That title is already in your library."}
	}
	out.Version = e.Version
	err = tx.QueryRow(ctx, remapMapping+` SELECT count(*) FILTER(WHERE m.old_id IS NOT NULL),count(*) FILTER(WHERE m.old_id IS NULL AND c.root_id<>$3)
 FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id LEFT JOIN m ON m.old_id=p.catalog_item_id
 WHERE p.profile_id=$1 AND p.entry_id=$4`, a.TrackingID(), *e.CatalogID, catalogID, entryID).Scan(&out.PlaysMoved, &out.PlaysRetained)
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, remapMapping+` SELECT count(*) FILTER(WHERE m.old_id IS NOT NULL),count(*) FILTER(WHERE m.old_id IS NULL)
 FROM episode_ratings r LEFT JOIN m ON m.old_id=r.catalog_item_id WHERE r.profile_id=$1 AND r.entry_id=$4`, a.TrackingID(), *e.CatalogID, catalogID, entryID).Scan(&out.RatingsMoved, &out.RatingsDropped)
	if err != nil {
		return out, err
	}
	out.Missing = []MissingEpisode{}
	rows, err := tx.Query(ctx, remapMapping+` SELECT DISTINCT c.season_number,c.episode_number,c.title FROM catalog_items c LEFT JOIN m ON m.old_id=c.id
 WHERE m.old_id IS NULL AND c.kind='episode' AND c.root_id=$2 AND c.id IN (
   SELECT catalog_item_id FROM plays WHERE profile_id=$1 AND entry_id=$4 UNION SELECT catalog_item_id FROM episode_ratings WHERE profile_id=$1 AND entry_id=$4)
 ORDER BY 1,2 LIMIT 50`, a.TrackingID(), *e.CatalogID, catalogID, entryID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v MissingEpisode
		if err = rows.Scan(&v.Season, &v.Episode, &v.Title); err != nil {
			return out, err
		}
		out.Missing = append(out.Missing, v)
	}
	return out, rows.Err()
}
