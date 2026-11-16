package library

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TrackInput adds a catalog record to the caller's library.
type TrackInput struct {
	CatalogID int64  `json:"catalog_id,string"`
	Status    string `json:"status"`
	Rating    *int   `json:"rating"`
}

// PlayInput records consumption of the entry's own record or one of its descendants.
// Each item is a distinct fact; repeating an episode is recorded by restarting the entry.
type PlayInput struct {
	Version int        `json:"version"`
	Items   []PlayItem `json:"items"`
}
type PlayItem struct {
	CatalogID int64      `json:"catalog_id,string"`
	PlayedAt  *time.Time `json:"played_at"`
}
type Play struct {
	Unmapped  bool      `json:"unmapped"`
	ID        int64     `json:"id,string"`
	CatalogID int64     `json:"catalog_id,string"`
	Cycle     int       `json:"cycle"`
	PlayedAt  time.Time `json:"played_at"`
	Source    string    `json:"source"`
}

// catalogKinds maps each trackable catalog kind to the library media type and the
// kinds of descendants whose plays count toward progress.
var catalogKinds = map[string]struct {
	mediaType string
	counted   []string
}{
	"movie": {"movie", nil}, "series": {"series", []string{"episode"}}, "anime": {"anime", nil}, "manga": {"manga", nil},
	"book": {"book", nil}, "audiobook": {"audiobook", nil}, "comic": {"comic", []string{"comic_issue"}}, "game": {"game", nil},
	"boardgame": {"boardgame", nil}, "music": {"music", []string{"track"}}, "podcast": {"podcast", []string{"podcast_episode"}},
	"video": {"video", nil},
}

func (s Store) Track(ctx context.Context, a identity.Actor, key string, in TrackInput) (Mutation, error) {
	if in.Status == "" {
		in.Status = "planned"
	}
	if in.CatalogID < 1 || !validStatus(in.Status) || (in.Rating != nil && (*in.Rating < 0 || *in.Rating > 10)) {
		return Mutation{}, fault.Invalid("Choose a catalog record, a supported status and a rating from 0 to 10.")
	}
	return s.mutate(ctx, a, "library:write", key, "entry.track", in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		return s.trackTx(ctx, tx, a, in)
	})
}

// trackTx adds a top-level catalog record to the caller's library inside tx.
func (s Store) trackTx(ctx context.Context, tx pgx.Tx, a identity.Actor, in TrackInput) (Mutation, error) {
	var kind, title, unit string
	var total *int
	var released *time.Time
	var parent *int64
	err := tx.QueryRow(ctx, "SELECT kind,title,unit,total,release_date,parent_id FROM catalog_items WHERE id=$1", in.CatalogID).Scan(&kind, &title, &unit, &total, &released, &parent)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mutation{}, fault.NotFound
	}
	if err != nil {
		return Mutation{}, err
	}
	meta, ok := catalogKinds[kind]
	if !ok || parent != nil {
		return Mutation{}, fault.Invalid("Add the show, podcast or series rather than one of its parts.")
	}
	var year *int
	if released != nil {
		y := released.Year()
		year = &y
	}
	if runes := []rune(title); len(runes) > 200 {
		title = string(runes[:200])
	}
	progress, completed := 0, 0
	if in.Status == "completed" {
		completed = 1
		if total != nil {
			progress = *total
		}
	}
	if _, err = tx.Exec(ctx, "DELETE FROM library_tombstones WHERE profile_id=$1 AND catalog_item_id=$2", a.TrackingID(), in.CatalogID); err != nil {
		return Mutation{}, err
	}
	out := Mutation{}
	err = tx.QueryRow(ctx, `INSERT INTO library_entries(profile_id,title,media_type,publication_year,unit,total,status,progress,rating,completed_cycles,catalog_item_id)
	VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id,version`, a.TrackingID(), title, meta.mediaType, year, unit, total, in.Status, progress, in.Rating, completed, in.CatalogID).Scan(&out.ID, &out.Version)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Mutation{}, &fault.Error{Status: 409, Code: "already_tracked", Message: "This is already in your library."}
	}
	if err == nil && in.Status == "planned" {
		_, err = s.ApplyPersonalStateTx(ctx, tx, a, in.CatalogID, PersonalState{Kind: "watchlist", Present: true}, time.Now().UTC(), "user:entry.track")
	}
	if err == nil {
		err = event(ctx, tx, a, out.ID, "added")
	}
	if err == nil && in.Status == "completed" {
		err = completionPlay(ctx, tx, a, out.ID)
	}
	if err == nil {
		err = initializeTracking(ctx, tx, a, out.ID, in.CatalogID)
	}
	return out, err
}

// completionPlay records that a single work was finished, so marking a movie or book
// completed shows in history and statistics like any other play. Shows, podcasts and
// comics complete through their episodes instead.
func completionPlay(ctx context.Context, tx pgx.Tx, a identity.Actor, entryID int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at)
	SELECT e.profile_id,e.id,e.catalog_item_id,e.cycle,clock_timestamp() FROM library_entries e JOIN catalog_items c ON c.id=e.catalog_item_id
	WHERE e.profile_id=$1 AND e.id=$2 AND c.kind NOT IN ('series','podcast','comic')
	AND NOT EXISTS(SELECT 1 FROM plays p WHERE p.profile_id=e.profile_id AND p.entry_id=e.id AND p.cycle=e.cycle)`, a.TrackingID(), entryID)
	return err
}

// NextEpisodeSQL recomputes next_item_id: the first regular episode, in watch order (or
// the chosen alternate order), with no play in the entry's current cycle. Callers append the row filter.
const NextEpisodeSQL = `UPDATE library_entries e SET next_item_id=(SELECT x.id FROM catalog_items x
	LEFT JOIN episode_order_items o ON o.order_id=e.episode_order AND o.catalog_item_id=x.id
	WHERE x.root_id=e.catalog_item_id AND x.kind='episode' AND CASE WHEN e.episode_order IS NULL THEN x.season_number>0 ELSE o.catalog_item_id IS NOT NULL END
	AND NOT EXISTS(SELECT 1 FROM plays p WHERE p.profile_id=e.profile_id AND p.entry_id=e.id AND p.cycle=e.cycle AND p.catalog_item_id=x.id)
	AND NOT EXISTS(SELECT 1 FROM skipped_items k WHERE k.profile_id=e.profile_id AND k.entry_id=e.id AND k.cycle=e.cycle AND k.catalog_item_id=x.id)
	ORDER BY o.position NULLS LAST,x.season_number,x.episode_number LIMIT 1)
	WHERE e.media_type='series' AND e.catalog_item_id IS NOT NULL AND `

func refreshNext(ctx context.Context, tx pgx.Tx, userID, entryID int64) error {
	_, err := tx.Exec(ctx, NextEpisodeSQL+"e.profile_id=$1 AND e.id=$2", userID, entryID)
	return err
}

// EntryForCatalog returns the caller's entry for a catalog record, if any.
func (s Store) EntryForCatalog(ctx context.Context, a identity.Actor, catalogID int64) (*Entry, error) {
	if !a.Allows("library:read") {
		return nil, fault.Forbidden
	}
	e, err := scanEntry(s.Pool.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.catalog_item_id=$2", a.TrackingID(), catalogID))
	if errors.Is(err, fault.NotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Plays lists the entry's plays for one cycle (0 means the current cycle).
func (s Store) Plays(ctx context.Context, a identity.Actor, entryID int64, cycle int) ([]Play, error) {
	if !a.Allows("library:read") {
		return nil, fault.Forbidden
	}
	var current int
	if err := s.Pool.QueryRow(ctx, "SELECT cycle FROM library_entries WHERE profile_id=$1 AND id=$2", a.TrackingID(), entryID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fault.NotFound
		}
		return nil, err
	}
	if cycle == 0 {
		cycle = current
	}
	rows, err := s.Pool.Query(ctx, "SELECT id,catalog_item_id,cycle,played_at,source,unmapped FROM plays WHERE profile_id=$1 AND entry_id=$2 AND cycle=$3 ORDER BY played_at,id LIMIT 5000", a.TrackingID(), entryID, cycle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Play{}
	for rows.Next() {
		var p Play
		if err = rows.Scan(&p.ID, &p.CatalogID, &p.Cycle, &p.PlayedAt, &p.Source, &p.Unmapped); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddPlays records up to 500 plays atomically and recomputes the entry's progress.
func (s Store) AddPlays(ctx context.Context, a identity.Actor, entryID int64, key string, in PlayInput) (Mutation, error) {
	if in.Version < 1 || len(in.Items) < 1 || len(in.Items) > 500 {
		return Mutation{}, fault.Invalid("Include the current version and 1 to 500 plays.")
	}
	now := time.Now()
	for _, item := range in.Items {
		if item.CatalogID < 1 {
			return Mutation{}, fault.Invalid("Each play needs a catalog record.")
		}
		// A missing time stays nil in the idempotency intent so a retry hashes the same;
		// the database assigns the commit time.
		if item.PlayedAt != nil && (item.PlayedAt.After(now.Add(24*time.Hour)) || item.PlayedAt.Year() < 1900) {
			return Mutation{}, fault.Invalid("Play dates must be between 1900 and today.")
		}
	}
	return s.mutate(ctx, a, "library:write", key, "entry.plays/"+strconv.FormatInt(entryID, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		e, err := current(ctx, tx, a, entryID, in.Version)
		if err != nil {
			return Mutation{}, err
		}
		if e.CatalogID == nil {
			return Mutation{}, fault.Invalid("Plays can be recorded only for items linked to the catalog.")
		}
		ids := make([]int64, len(in.Items))
		for i, item := range in.Items {
			ids[i] = item.CatalogID
		}
		// Every play must target the entry's record or a descendant (season → episode).
		var valid int
		if err = tx.QueryRow(ctx, `WITH RECURSIVE tree AS (SELECT id FROM catalog_items WHERE id=$1
			UNION ALL SELECT c.id FROM catalog_items c JOIN tree t ON c.parent_id=t.id)
			SELECT count(DISTINCT id) FROM tree WHERE id=ANY($2)`, *e.CatalogID, ids).Scan(&valid); err != nil {
			return Mutation{}, err
		}
		distinct := map[int64]bool{}
		for _, id := range ids {
			distinct[id] = true
		}
		if valid != len(distinct) {
			return Mutation{}, fault.Invalid("A play targets a record outside this library item.")
		}
		batch := &pgx.Batch{}
		for _, item := range in.Items {
			batch.Queue("INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at) VALUES($1,$2,$3,$4,COALESCE($5,clock_timestamp()))", a.TrackingID(), entryID, item.CatalogID, e.Cycle, item.PlayedAt)
		}
		if err = tx.SendBatch(ctx, batch).Close(); err != nil {
			return Mutation{}, err
		}
		return s.reproject(ctx, tx, a, e, "played")
	})
}

// RemovePlay deletes one play fact, for correcting a mistaken mark.
func (s Store) RemovePlay(ctx context.Context, a identity.Actor, entryID, playID int64, key string, version int) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "entry.unplay/"+strconv.FormatInt(entryID, 10)+"/"+strconv.FormatInt(playID, 10), version, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		e, err := current(ctx, tx, a, entryID, version)
		if err != nil {
			return Mutation{}, err
		}
		tag, err := tx.Exec(ctx, "DELETE FROM plays WHERE profile_id=$1 AND entry_id=$2 AND id=$3", a.TrackingID(), entryID, playID)
		if err != nil {
			return Mutation{}, err
		}
		if tag.RowsAffected() == 0 {
			return Mutation{}, fault.NotFound
		}
		return s.reproject(ctx, tx, a, e, "unplayed")
	})
}

// reproject derives progress and status from the current cycle's plays. For a show,
// progress is the number of distinct regular-season episodes watched; specials are
// recorded but do not count toward completion. For single works a play completes it.
func (s Store) reproject(ctx context.Context, tx pgx.Tx, a identity.Actor, e Entry, kind string) (Mutation, error) {
	var catalogKind, production string
	var catalogTotal *int
	if err := tx.QueryRow(ctx, "SELECT kind,total,release_status FROM catalog_items WHERE id=$1", *e.CatalogID).Scan(&catalogKind, &catalogTotal, &production); err != nil {
		return Mutation{}, err
	}
	meta := catalogKinds[catalogKind]
	progress, status, total := e.Progress, e.Status, e.Total
	if e.Edition != nil && e.Edition.Pages != nil && e.Unit == "pages" {
		total = e.Edition.Pages
	} else if catalogTotal != nil {
		total = catalogTotal
	}
	counted := len(meta.counted) > 0
	if catalogKind == "music" {
		// An album counts distinct tracks once its track list is known; until then, or
		// for listens recorded against the whole album, a listen completes it.
		var tracks, trackPlays int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM catalog_items WHERE parent_id=$4 AND kind='track'),
			(SELECT count(*) FROM plays p WHERE p.profile_id=$1 AND p.entry_id=$2 AND p.cycle=$3 AND NOT p.unmapped AND p.catalog_item_id<>$4)`, a.TrackingID(), e.ID, e.Cycle, *e.CatalogID).Scan(&tracks, &trackPlays); err != nil {
			return Mutation{}, err
		}
		counted = tracks > 0 && trackPlays > 0
	}
	if counted {
		// Played and deliberately skipped items both fill a slot toward completion.
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (
			SELECT catalog_item_id FROM plays WHERE profile_id=$1 AND entry_id=$2 AND cycle=$3 AND NOT unmapped
			UNION SELECT catalog_item_id FROM skipped_items WHERE profile_id=$1 AND entry_id=$2 AND cycle=$3) u
			JOIN catalog_items c ON c.id=u.catalog_item_id WHERE c.kind=ANY($4) AND COALESCE(c.season_number,1)>0`, a.TrackingID(), e.ID, e.Cycle, meta.counted).Scan(&progress); err != nil {
			return Mutation{}, err
		}
		if total != nil && progress > *total {
			total = &progress
		}
		switch {
		case total != nil && progress >= *total:
			status = "completed"
		case progress > 0 && (status == "planned" || status == "completed"):
			status = "in_progress"
		case progress == 0 && status == "completed":
			status = "in_progress"
		}
	} else {
		var plays int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id=$1 AND entry_id=$2 AND cycle=$3 AND NOT unmapped", a.TrackingID(), e.ID, e.Cycle).Scan(&plays); err != nil {
			return Mutation{}, err
		}
		if plays > 0 {
			status = "completed"
			if total != nil {
				progress = *total
			}
		} else if status == "completed" {
			status, progress = "in_progress", 0
		}
	}
	automatic := a.Kind == "import" || a.Kind == "connection"
	if automatic && e.Status == "completed" {
		var manual bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT source LIKE 'user%' FROM entry_status_history WHERE profile_id=$1 AND entry_id=$2 ORDER BY id DESC LIMIT 1),false)`, a.TrackingID(), e.ID).Scan(&manual); err != nil {
			return Mutation{}, err
		}
		if manual {
			status = e.Status
		}
	}
	if automatic && (e.Status == "paused" || e.Status == "dropped" || e.Status == "rated") {
		status = e.Status
	}
	if automatic && status == "completed" && e.Status != "completed" && (catalogKind == "series" || catalogKind == "anime") && !terminalProduction(production) {
		status = "in_progress"
	}
	out := Mutation{ID: e.ID}
	// Plays carry their own dates, which for synced and imported history lie in the
	// past: an entry starts at its cycle's first play and, when the plays complete it,
	// finishes at the latest one. event fills either from the clock when no play exists.
	err := tx.QueryRow(ctx, `WITH d AS (SELECT min(played_at) first_play,LEAST(max(played_at),clock_timestamp()) last_play FROM plays
			WHERE profile_id=$1 AND entry_id=$2 AND cycle=$7 AND NOT unmapped)
		UPDATE library_entries SET progress=$3,status=$4,total=$5,completed_cycles=completed_cycles+$6,version=version+1,updated_at=clock_timestamp(),
			started_at=CASE WHEN started_at IS NULL OR started_at>d.first_play THEN COALESCE(d.first_play,started_at) ELSE started_at END,
			finished_at=CASE WHEN $4='completed' AND status<>'completed' THEN d.last_play ELSE finished_at END
		FROM d WHERE profile_id=$1 AND id=$2 RETURNING version`, a.TrackingID(), e.ID, progress, status, total, completionDelta(e.Status, status), e.Cycle).Scan(&out.Version)
	if err == nil {
		err = event(ctx, tx, a, e.ID, kind)
	}
	if err == nil {
		err = refreshNext(ctx, tx, a.TrackingID(), e.ID)
	}
	return out, err
}

// initializeTracking is shared by direct tracking and accepted recommendations.
func initializeTracking(ctx context.Context, tx pgx.Tx, a identity.Actor, entryID, catalogID int64) error {
	if err := refreshNext(ctx, tx, a.TrackingID(), entryID); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"catalog_id": strconv.FormatInt(catalogID, 10)})
	_, err := jobs.Enqueue(ctx, tx, "catalog_refresh", "catalog_refresh:"+strconv.FormatInt(catalogID, 10)+":"+time.Now().UTC().Format("2006-01-02"), "idempotent", payload, 3)
	return err
}

func terminalProduction(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ended", "canceled", "cancelled", "finished", "finished_airing":
		return true
	}
	return false
}
