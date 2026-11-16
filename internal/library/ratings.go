package library

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

type StatusChange struct {
	ID             int64     `json:"id,string"`
	PreviousStatus *string   `json:"previous_status"`
	Status         string    `json:"status"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s Store) StatusHistory(ctx context.Context, a identity.Actor, id int64, cursor string, limit int) (page.Result[StatusChange], error) {
	out := page.Result[StatusChange]{Items: []StatusChange{}}
	if _, err := s.Get(ctx, a, id); err != nil {
		return out, err
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := "status|" + strconv.FormatInt(a.TrackingID(), 10) + "|" + strconv.FormatInt(id, 10)
	last, err := page.Decode(scope, cursor)
	if err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,previous_status,status,source,created_at FROM entry_status_history WHERE profile_id=$1 AND entry_id=$2 AND ($3::bigint=0 OR id<$3) ORDER BY id DESC LIMIT $4`, a.TrackingID(), id, last, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v StatusChange
		if err = rows.Scan(&v.ID, &v.PreviousStatus, &v.Status, &v.Source, &v.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode(scope, out.Items[limit-1].ID)
		out.NextCursor = &next
	}
	return out, rows.Err()
}

type EpisodeRating struct {
	CatalogID int64      `json:"catalog_id,string"`
	Title     string     `json:"title"`
	Season    *int       `json:"season"`
	Episode   *int       `json:"episode"`
	Rating    *int       `json:"rating"`
	ScoredAt  *time.Time `json:"scored_at"`
	Version   int        `json:"version"`
}
type RatingSummary struct {
	Rating          *float64 `json:"rating"`
	RatedEpisodes   int      `json:"rated_episodes"`
	WatchedEpisodes int      `json:"watched_episodes"`
}
type EpisodeRatingInput struct {
	Rating  *int `json:"rating"`
	Version int  `json:"version"`
}

func (s Store) DerivedRating(ctx context.Context, a identity.Actor, id int64, season *int) (RatingSummary, error) {
	var out RatingSummary
	if _, err := s.Get(ctx, a, id); err != nil {
		return out, err
	}
	// Every rated episode counts, watched or not; each episode has one rating, so
	// rewatches never weigh twice.
	err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT avg(r.rating) FROM episode_ratings r JOIN catalog_items c ON c.id=r.catalog_item_id WHERE r.profile_id=$1 AND r.entry_id=$2 AND r.rating IS NOT NULL AND c.kind='episode' AND ($3::integer IS NULL OR c.season_number=$3)),
		(SELECT count(*) FROM episode_ratings r JOIN catalog_items c ON c.id=r.catalog_item_id WHERE r.profile_id=$1 AND r.entry_id=$2 AND r.rating IS NOT NULL AND c.kind='episode' AND ($3::integer IS NULL OR c.season_number=$3)),
		(SELECT count(DISTINCT p.catalog_item_id) FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id WHERE p.profile_id=$1 AND p.entry_id=$2 AND NOT p.unmapped AND c.kind='episode' AND ($3::integer IS NULL OR c.season_number=$3))`, a.TrackingID(), id, season).Scan(&out.Rating, &out.RatedEpisodes, &out.WatchedEpisodes)
	return out, err
}
func (s Store) RateEpisode(ctx context.Context, a identity.Actor, id, child int64, in EpisodeRatingInput) (EpisodeRating, error) {
	if in.Version < 0 {
		return EpisodeRating{}, fault.Invalid("Invalid version.")
	}
	if in.Rating != nil && (*in.Rating < 0 || *in.Rating > 10) {
		return EpisodeRating{}, fault.Invalid("Invalid rating.")
	}
	return identity.Write(ctx, s.Pool, a, "library:write", func(tx pgx.Tx, a identity.Actor) (EpisodeRating, error) {
		var out EpisodeRating
		if err := lockChanges(ctx, tx, a.TrackingID()); err != nil {
			return out, err
		}
		var allowed bool
		// An episode of the entry's show can be rated without being watched; an episode
		// played under this entry (for example before a remap) stays rateable too.
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_entries e JOIN catalog_items c ON c.id=$3 AND c.kind='episode' WHERE e.profile_id=$1 AND e.id=$2 AND (c.root_id=e.catalog_item_id OR EXISTS(SELECT 1 FROM plays p WHERE p.profile_id=e.profile_id AND p.entry_id=e.id AND p.catalog_item_id=c.id AND NOT p.unmapped)))`, a.TrackingID(), id, child).Scan(&allowed); err != nil {
			return out, err
		}
		if !allowed {
			return out, fault.NotFound
		}
		if in.Version == 0 && in.Rating == nil {
			err := tx.QueryRow(ctx, `SELECT c.id,c.title,c.season_number,c.episode_number FROM catalog_items c WHERE c.id=$1 AND NOT EXISTS(SELECT 1 FROM episode_ratings WHERE profile_id=$2 AND entry_id=$3 AND catalog_item_id=$1)`, child, a.TrackingID(), id).Scan(&out.CatalogID, &out.Title, &out.Season, &out.Episode)
			if err == pgx.ErrNoRows {
				return out, fault.Conflict
			}
			return out, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO episode_ratings(profile_id,entry_id,catalog_item_id,rating) SELECT $1,$2,$3,$4 WHERE $5::integer=0 ON CONFLICT(profile_id,entry_id,catalog_item_id) DO NOTHING`, a.TrackingID(), id, child, in.Rating, in.Version)
		if err != nil {
			return out, err
		}
		if tag.RowsAffected() == 0 {
			tag, err = tx.Exec(ctx, `UPDATE episode_ratings SET scored_at=CASE WHEN rating IS DISTINCT FROM $4::integer THEN clock_timestamp() ELSE scored_at END,rating=$4,version=version+1 WHERE profile_id=$1 AND entry_id=$2 AND catalog_item_id=$3 AND version=$5`, a.TrackingID(), id, child, in.Rating, in.Version)
			if err != nil {
				return out, err
			}
			if tag.RowsAffected() != 1 {
				return out, fault.Conflict
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE library_entries SET version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2`, a.TrackingID(), id); err != nil {
			return out, err
		}
		err = tx.QueryRow(ctx, `SELECT r.catalog_item_id,c.title,c.season_number,c.episode_number,r.rating,r.scored_at,r.version FROM episode_ratings r JOIN catalog_items c ON c.id=r.catalog_item_id WHERE r.profile_id=$1 AND r.entry_id=$2 AND r.catalog_item_id=$3`, a.TrackingID(), id, child).Scan(&out.CatalogID, &out.Title, &out.Season, &out.Episode, &out.Rating, &out.ScoredAt, &out.Version)
		return out, err
	})
}

func (s Store) EpisodeRatings(ctx context.Context, a identity.Actor, id int64, cursor string, limit int) (page.Result[EpisodeRating], error) {
	out := page.Result[EpisodeRating]{Items: []EpisodeRating{}}
	if _, err := s.Get(ctx, a, id); err != nil {
		return out, err
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := "episode-ratings|" + strconv.FormatInt(a.TrackingID(), 10) + "|" + strconv.FormatInt(id, 10)
	last, err := page.Decode(scope, cursor)
	if err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id,c.title,c.season_number,c.episode_number,r.rating,r.scored_at,COALESCE(r.version,0) FROM catalog_items c LEFT JOIN episode_ratings r ON r.profile_id=$1 AND r.entry_id=$2 AND r.catalog_item_id=c.id WHERE c.kind='episode' AND ($3::bigint=0 OR c.id<$3) AND (r.catalog_item_id IS NOT NULL OR EXISTS(SELECT 1 FROM plays p WHERE p.profile_id=$1 AND p.entry_id=$2 AND p.catalog_item_id=c.id AND NOT p.unmapped)) ORDER BY c.id DESC LIMIT $4`, a.TrackingID(), id, last, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v EpisodeRating
		if err = rows.Scan(&v.CatalogID, &v.Title, &v.Season, &v.Episode, &v.Rating, &v.ScoredAt, &v.Version); err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode(scope, out.Items[limit-1].CatalogID)
		out.NextCursor = &next
	}
	return out, rows.Err()
}
