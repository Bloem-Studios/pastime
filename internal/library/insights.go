package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
)

// HistoryItem is one consumption fact with enough context to render it without joins
// on the client: the library entry, and the specific episode or issue when there is one.
type HistoryItem struct {
	Unmapped      bool      `json:"unmapped"`
	PlayID        int64     `json:"play_id,string"`
	PlayedAt      time.Time `json:"played_at"`
	Cycle         int       `json:"cycle"`
	Source        string    `json:"source"`
	EntryID       int64     `json:"entry_id,string"`
	EntryTitle    string    `json:"entry_title"`
	MediaType     string    `json:"media_type"`
	ImageURL      string    `json:"image_url"`
	CatalogID     int64     `json:"catalog_id,string"`
	ItemTitle     string    `json:"item_title"`
	SeasonNumber  *int      `json:"season_number"`
	EpisodeNumber *int      `json:"episode_number"`
	Minutes       *int      `json:"minutes"`
	// Marked is a bulk "mark as played" (three or more items in one minute): it counts
	// toward progress but not toward time, plays or streaks.
	Marked bool `json:"marked"`
}

// HistoryFilter bounds the timeline to a time window, an optional media type and an
// optional library entry.
type HistoryFilter struct {
	From, To  *time.Time
	MediaType string
	EntryID   int64
	Cursor    string
	Limit     int
}

func (s Store) History(ctx context.Context, a identity.Actor, f HistoryFilter) (page.Result[HistoryItem], error) {
	out := page.Result[HistoryItem]{Items: []HistoryItem{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	limit, err := page.Limit(f.Limit)
	if err != nil {
		return out, err
	}
	if f.MediaType != "" && !validType(f.MediaType) {
		return out, fault.Invalid("Choose a supported media type.")
	}
	scope := "history|" + strconv.FormatInt(a.TrackingID(), 10) + "|" + f.MediaType + "|" + stamp(f.From) + "|" + stamp(f.To)
	if f.EntryID > 0 {
		scope += "|" + strconv.FormatInt(f.EntryID, 10)
	}
	at, last, more, err := page.DecodeAt(scope, f.Cursor)
	if err != nil {
		return out, err
	}
	query := historySelect
	args := []any{a.TrackingID()}
	add := func(clause string, v ...any) {
		for _, x := range v {
			args = append(args, x)
			clause = replaceFirst(clause, "?", "$"+strconv.Itoa(len(args)))
		}
		query += " AND " + clause
	}
	if f.From != nil {
		add("p.played_at>=?", *f.From)
	}
	if f.To != nil {
		add("p.played_at<?", *f.To)
	}
	if f.MediaType != "" {
		add("p.media_type=?", f.MediaType)
	}
	if f.EntryID > 0 {
		add("p.entry_id=?", f.EntryID)
	}
	if more {
		add("(p.played_at,p.id)<(?,?)", at, last)
	}
	args = append(args, limit+1)
	query += " ORDER BY p.played_at DESC,p.id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var h HistoryItem
		if err = rows.Scan(historyFields(&h)...); err != nil {
			return out, err
		}
		out.Items = append(out.Items, h)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		tail := out.Items[len(out.Items)-1]
		next := page.EncodeAt(scope, tail.PlayedAt, tail.PlayID)
		out.NextCursor = &next
	}
	return out, nil
}

// finishedByMark is true for a completed entry e whose finishing play is a marked one.
const finishedByMark = `EXISTS(SELECT 1 FROM plays fp WHERE fp.profile_id=e.profile_id AND fp.entry_id=e.id AND fp.played_at=e.finished_at AND fp.marked)`

// historySelect reads HistoryItem rows of profile $1; callers append AND clauses.
const historySelect = `SELECT p.id,p.played_at,p.cycle,p.source,e.id,e.title,e.media_type,COALESCE(top.image_url,''),c.id,c.title,c.season_number,c.episode_number,p.minutes,p.unmapped,p.marked
	FROM plays p JOIN library_entries e ON e.profile_id=p.profile_id AND e.id=p.entry_id JOIN catalog_items c ON c.id=p.catalog_item_id
	LEFT JOIN catalog_items top ON top.id=e.catalog_item_id WHERE p.profile_id=$1`

func historyFields(h *HistoryItem) []any {
	return []any{&h.PlayID, &h.PlayedAt, &h.Cycle, &h.Source, &h.EntryID, &h.EntryTitle, &h.MediaType, &h.ImageURL, &h.CatalogID, &h.ItemTitle, &h.SeasonNumber, &h.EpisodeNumber, &h.Minutes, &h.Unmapped, &h.Marked}
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return strconv.FormatInt(t.UnixMicro(), 10)
}

func replaceFirst(s, old, repl string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + repl + s[i+len(old):]
		}
	}
	return s
}

// DeletePlay removes one play by its identity, locking and reprojecting its entry.
// Unlike RemovePlay it needs no entry version: deleting a named fact cannot lose an update.
func (s Store) DeletePlay(ctx context.Context, a identity.Actor, playID int64, key string) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "play.delete/"+strconv.FormatInt(playID, 10), playID, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		var entryID int64
		err := tx.QueryRow(ctx, "SELECT entry_id FROM plays WHERE profile_id=$1 AND id=$2", a.TrackingID(), playID).Scan(&entryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Mutation{}, fault.NotFound
		}
		if err != nil {
			return Mutation{}, err
		}
		e, err := scanEntry(tx.QueryRow(ctx, "SELECT "+entryColumns+from+" WHERE e.profile_id=$1 AND e.id=$2 FOR UPDATE OF e", a.TrackingID(), entryID))
		if err != nil {
			return Mutation{}, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM plays WHERE profile_id=$1 AND id=$2", a.TrackingID(), playID); err != nil {
			return Mutation{}, err
		}
		return s.reproject(ctx, tx, a, e, "unplayed")
	})
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type TypeStats struct {
	UnknownRuntime int    `json:"unknown_runtime"`
	MediaType      string `json:"media_type"`
	Entries        int    `json:"entries"`
	Completed      int    `json:"completed"`
	Plays          int    `json:"plays"`
	Sessions       int    `json:"sessions"`
	Minutes        int    `json:"minutes"`
}
type DayStats struct {
	UnknownRuntime int    `json:"unknown_runtime"`
	Date           string `json:"date"`
	Plays          int    `json:"plays"`
	Sessions       int    `json:"sessions"`
	Minutes        int    `json:"minutes"`
}
type Ranked struct {
	Summary
	Plays int `json:"plays"`
}

// Creator is a credit (author, developer, artist, cast or crew) and how many of the
// window's titles of one media type carry it.
type Creator struct {
	MediaType string `json:"media_type"`
	Name      string `json:"name"`
	Count     int    `json:"count"`
}

// Insights summarizes one account over a window. Every aggregate is a bounded,
// indexed query over that account's rows; nothing is cached across accounts.
type Insights struct {
	UnknownRuntime int         `json:"unknown_runtime"`
	From           time.Time   `json:"from"`
	To             time.Time   `json:"to"`
	Plays          int         `json:"plays"`
	Sessions       int         `json:"sessions"`
	Minutes        int         `json:"minutes"`
	Completed      int         `json:"completed"`
	Streaks        Streaks     `json:"streaks"`
	ByType         []TypeStats `json:"by_type"`
	Statuses       []Count     `json:"statuses"`
	Ratings        []Count     `json:"ratings"`
	Genres         []Count     `json:"genres"`
	People         []Count     `json:"people"`
	Studios        []Count     `json:"studios"`
	Creators       []Creator   `json:"creators"`
	Days           []DayStats  `json:"days"`
	TopRated       []Summary   `json:"top_rated"`
	MostPlayed     []Ranked    `json:"most_played"`
}

// Insights are cached per person and window. The key carries the head of their change
// feed and their preferences version, so any library change or preference change is
// seen at once; the TTL only bounds how long catalog metadata (titles, genres) can lag.
func (s Store) Insights(ctx context.Context, a identity.Actor, fromT, toT time.Time) (Insights, error) {
	if s.Cache == nil || !a.Allows("library:read") {
		return s.insights(ctx, a, fromT, toT)
	}
	var head, prefs int64
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT max(id) FROM change_log WHERE profile_id=$1),0),
		COALESCE((SELECT version FROM user_preferences WHERE profile_id=$1),0)`, a.TrackingID()).Scan(&head, &prefs); err != nil {
		return Insights{}, err
	}
	// The current streak depends on today's date as well as on the library.
	key := fmt.Sprintf("insights:v4:%d:%d:%d:%d:%d:%s", a.TrackingID(), fromT.UnixNano(), toT.UnixNano(), head, prefs, utcDate(time.Now()).Format(time.DateOnly))
	if data, ok := s.Cache.Get(ctx, key); ok {
		var out Insights
		if json.Unmarshal(data, &out) == nil {
			return out, nil
		}
	}
	out, err := s.insights(ctx, a, fromT, toT)
	if err == nil {
		if data, e := json.Marshal(out); e == nil {
			s.Cache.Set(ctx, key, data, 30*time.Minute)
		}
	}
	return out, err
}

func (s Store) insights(ctx context.Context, a identity.Actor, fromT, toT time.Time) (Insights, error) {
	out := Insights{From: fromT, To: toT, ByType: []TypeStats{}, Statuses: []Count{}, Ratings: []Count{}, Genres: []Count{}, People: []Count{}, Studios: []Count{}, Creators: []Creator{}, Days: []DayStats{}, TopRated: []Summary{}, MostPlayed: []Ranked{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	if !toT.After(fromT) || toT.Sub(fromT) > 3700*24*time.Hour {
		return out, fault.Invalid("Choose a range of at most ten years.")
	}
	uid := a.TrackingID()
	// Daily rollups cover complete UTC days; raw plays cover only the boundary days.
	// Finished sessions that recorded no play add their time on the day they ended.
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, windowDaysSQL+` SELECT to_char(day,'YYYY-MM-DD'),sum(plays)::int,sum(sessions)::int,sum(minutes)::int,sum(unknown_runtime)::int FROM window_days GROUP BY day ORDER BY day`, uid, fromT, toT)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var d DayStats
		if err = rows.Scan(&d.Date, &d.Plays, &d.Sessions, &d.Minutes, &d.UnknownRuntime); err != nil {
			rows.Close()
			return out, err
		}
		out.Plays += d.Plays
		out.Sessions += d.Sessions
		out.Minutes += d.Minutes
		out.UnknownRuntime += d.UnknownRuntime
		out.Days = append(out.Days, d)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Streaks.Longest = longestStreak(out.Days)
	if out.Streaks.Current, err = currentStreak(ctx, tx, uid, utcDate(time.Now())); err != nil {
		return out, err
	}
	// A title finished by a bulk "mark as played" was not finished then.
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM library_entries e WHERE e.profile_id=$1 AND e.status='completed' AND e.finished_at>=$2 AND e.finished_at<$3 AND NOT `+finishedByMark, uid, fromT, toT).Scan(&out.Completed); err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, windowDaysSQL+`, t AS (SELECT media_type,sum(plays)::int plays,sum(sessions)::int sessions,sum(minutes)::int minutes,sum(unknown_runtime)::int unknown_runtime FROM window_days GROUP BY 1),
		l AS (SELECT media_type,count(*) entries,count(*) FILTER(WHERE status='completed') completed FROM library_entries WHERE profile_id=$1 GROUP BY 1)
		SELECT l.media_type,l.entries,l.completed,COALESCE(t.plays,0),COALESCE(t.sessions,0),COALESCE(t.minutes,0),COALESCE(t.unknown_runtime,0) FROM l LEFT JOIN t USING(media_type) ORDER BY COALESCE(t.minutes,0) DESC,l.entries DESC`, uid, fromT, toT)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t TypeStats
		if err = rows.Scan(&t.MediaType, &t.Entries, &t.Completed, &t.Plays, &t.Sessions, &t.Minutes, &t.UnknownRuntime); err != nil {
			rows.Close()
			return out, err
		}
		out.ByType = append(out.ByType, t)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	// Statuses and ratings come from one pass over the library.
	rows, err = tx.Query(ctx, `SELECT CASE WHEN GROUPING(status)=0 THEN 's' ELSE 'r' END, COALESCE(status, rating::text), count(*)
		FROM library_entries WHERE profile_id=$1 GROUP BY GROUPING SETS ((status), (rating)) HAVING GROUPING(status)=0 OR rating IS NOT NULL
		ORDER BY 1, 3 DESC`, uid)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var kind string
		var c Count
		if err = rows.Scan(&kind, &c.Name, &c.Count); err != nil {
			rows.Close()
			return out, err
		}
		if kind == "s" {
			out.Statuses = append(out.Statuses, c)
		} else {
			out.Ratings = append(out.Ratings, c)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	sort.SliceStable(out.Ratings, func(i, j int) bool { return atoi(out.Ratings[i].Name) > atoi(out.Ratings[j].Name) })
	// Genres, people, studios and creators of the titles used in the window (played, or
	// read or played in a session), each title counted once, in one pass: the title set
	// is found once and each facet ranked separately. Creators rank within a media type.
	rows, err = tx.Query(ctx, `WITH titles AS MATERIALIZED (SELECT DISTINCT e.catalog_item_id id,e.media_type FROM library_entries e
			WHERE e.profile_id=$1 AND e.catalog_item_id IS NOT NULL
			AND (EXISTS(SELECT 1 FROM plays p WHERE p.profile_id=$1 AND p.entry_id=e.id AND p.played_at>=$2 AND p.played_at<$3 AND NOT p.marked)
			  OR e.id IN (SELECT k.entry_id FROM checkins k WHERE k.profile_id=$1 AND k.kind='session' AND k.status='finished' AND k.finished_at>=$2 AND k.finished_at<$3))),
		facets AS (SELECT f.kind,CASE WHEN f.kind='c' THEN t.media_type ELSE '' END mt,f.name FROM titles t JOIN catalog_items c ON c.id=t.id CROSS JOIN LATERAL (
			SELECT 'g',g FROM unnest(c.genres) g UNION ALL SELECT 'p',p FROM unnest(c.credits) p UNION ALL SELECT 'c',p FROM unnest(c.credits[1:10]) p
			UNION ALL SELECT 's',st FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(c.details->'studios')='array' THEN c.details->'studios' ELSE '[]'::jsonb END) st) f(kind,name)),
		counted AS (SELECT kind,mt,name,count(*) n FROM facets GROUP BY 1,2,3),
		ranked AS (SELECT kind,mt,name,n,row_number() OVER (PARTITION BY kind,mt ORDER BY n DESC,name) r FROM counted WHERE kind<>'p' OR n>1)
		SELECT kind,mt,name,n FROM ranked WHERE r<=CASE WHEN kind='c' THEN 5 ELSE 15 END ORDER BY kind,mt,r`, uid, fromT, toT)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var kind, mt string
		var c Count
		if err = rows.Scan(&kind, &mt, &c.Name, &c.Count); err != nil {
			rows.Close()
			return out, err
		}
		switch kind {
		case "g":
			out.Genres = append(out.Genres, c)
		case "p":
			out.People = append(out.People, c)
		case "s":
			out.Studios = append(out.Studios, c)
		case "c":
			out.Creators = append(out.Creators, Creator{MediaType: mt, Name: c.Name, Count: c.Count})
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, "SELECT "+columns+from+" WHERE e.profile_id=$1 AND e.rating IS NOT NULL ORDER BY e.rating DESC,e.last_activity_at DESC NULLS LAST,e.id DESC LIMIT 10", uid)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var e Summary
		if err = rows.Scan(fields(&e)...); err != nil {
			rows.Close()
			return out, err
		}
		out.TopRated = append(out.TopRated, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, "WITH w AS (SELECT entry_id,count(*) n FROM plays WHERE profile_id=$1 AND played_at>=$2 AND played_at<$3 AND NOT marked GROUP BY 1 ORDER BY 2 DESC LIMIT 10) SELECT "+columns+",w.n"+from+" JOIN w ON w.entry_id=e.id WHERE e.profile_id=$1 ORDER BY w.n DESC,e.id DESC", uid, fromT, toT)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r Ranked
		if err = rows.Scan(append(fields(&r.Summary), &r.Plays)...); err != nil {
			rows.Close()
			return out, err
		}
		out.MostPlayed = append(out.MostPlayed, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// Release is a dated upcoming (or recent) item from something the caller tracks.
type Release struct {
	Date          string `json:"date"`
	CatalogID     int64  `json:"catalog_id,string"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	SeasonNumber  *int   `json:"season_number"`
	EpisodeNumber *int   `json:"episode_number"`
	EntryID       int64  `json:"entry_id,string"`
	EntryTitle    string `json:"entry_title"`
	MediaType     string `json:"media_type"`
	ImageURL      string `json:"image_url"`
	BackdropURL   string `json:"backdrop_url,omitempty"`
	ParentCatalog int64  `json:"title_catalog_id,string"`
	// ReleaseType is theatrical, digital or physical for a movie's regional date, and
	// empty for a primary release date.
	ReleaseType string `json:"release_type,omitempty"`
	// Why the title is on the calendar. The calendar projection publishes these as
	// its followed/favourite/watchlist flags; they never change this JSON shape.
	Following bool `json:"-"`
	Favourite bool `json:"-"`
	Watchlist bool `json:"-"`
}

// Calendar lists releases between two dates for titles the caller follows through a
// planned, in-progress or paused library entry, and for favourite and watchlist
// titles with or without a library entry.
func (s Store) Calendar(ctx context.Context, a identity.Actor, fromD, toD time.Time) ([]Release, error) {
	if !a.Allows("library:read") {
		return nil, fault.Forbidden
	}
	if toD.Before(fromD) || toD.Sub(fromD) > 400*24*time.Hour {
		return nil, fault.Invalid("Choose a calendar range of at most 400 days.")
	}
	// The calendar honours the account's release-date choice for every caller,
	// including the calendar feed, so it reads the stored document directly.
	var region string
	var types []string
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(prefs->>'release_region',''),COALESCE(ARRAY(SELECT jsonb_array_elements_text(CASE WHEN jsonb_typeof(prefs->'calendar_release_types')='array' THEN prefs->'calendar_release_types' ELSE '[]'::jsonb END)),'{}')
		FROM user_preferences WHERE profile_id=$1`, a.TrackingID()).Scan(&region, &types); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if len(types) == 0 {
		region = ""
	}
	// Every dated release carries its top-level title (root_id), so one range scan over
	// release dates joins directly to the titles this profile follows. Favourite and
	// watchlist titles count even without a library entry (entry_id 0), and each title
	// appears once however many reasons it has. With a region chosen, a movie that has
	// dates for that region shows those instead of its primary date; the followed
	// movies are few, so reading their dates is cheap.
	rows, err := s.Pool.Query(ctx, `WITH reasons AS (
	SELECT catalog_item_id,true AS following,false AS favourite,false AS watchlist FROM library_entries
	WHERE profile_id=$1 AND catalog_item_id IS NOT NULL AND status IN ('planned','in_progress','paused')
	UNION ALL
	SELECT catalog_item_id,false,kind='favorite',kind='watchlist' FROM profile_catalog_memberships
	WHERE profile_id=$1 AND present AND kind IN ('favorite','watchlist')
), followed AS (
	SELECT r.catalog_item_id,COALESCE(e.id,0) AS entry_id,COALESCE(e.title,left(top.title,200)) AS entry_title,COALESCE(e.media_type,top.kind) AS media_type,
	 bool_or(r.following) AS following,bool_or(r.favourite) AS favourite,bool_or(r.watchlist) AS watchlist
	FROM reasons r
	JOIN catalog_items top ON top.id=r.catalog_item_id AND top.parent_id IS NULL
	LEFT JOIN library_entries e ON e.profile_id=$1 AND e.catalog_item_id=r.catalog_item_id
	GROUP BY r.catalog_item_id,e.id,e.title,e.media_type,top.title,top.kind
)
SELECT d,id,kind,title,season_number,episode_number,entry_id,entry_title,media_type,image_url,backdrop_url,catalog_item_id,release_type,following,favourite,watchlist FROM (
	SELECT x.release_date AS d,x.id,x.kind,x.title,x.season_number,x.episode_number,f.entry_id,f.entry_title,f.media_type,top.image_url,top.backdrop_url,f.catalog_item_id,'' AS release_type,f.following,f.favourite,f.watchlist
	FROM catalog_items x
	JOIN followed f ON f.catalog_item_id=x.root_id
	JOIN catalog_items top ON top.id=x.root_id
	WHERE x.release_date BETWEEN $2 AND $3 AND x.kind IN ('movie','episode','podcast_episode','comic_issue')
	  AND NOT COALESCE(x.kind='movie' AND $4<>'' AND jsonb_typeof(x.details->'release_dates'->$4)='object',false)
	UNION ALL
	SELECT r.value::date,x.id,x.kind,x.title,NULL,NULL,f.entry_id,f.entry_title,f.media_type,x.image_url,x.backdrop_url,f.catalog_item_id,r.key,f.following,f.favourite,f.watchlist
	FROM followed f
	JOIN catalog_items x ON x.id=f.catalog_item_id AND x.kind='movie'
	CROSS JOIN LATERAL jsonb_each_text(CASE WHEN jsonb_typeof(x.details->'release_dates'->$4)='object' THEN x.details->'release_dates'->$4 ELSE '{}'::jsonb END) r
	WHERE $4<>'' AND r.key=ANY($5::text[])
	  AND r.value ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' AND r.value::date BETWEEN $2 AND $3
	) releases
	ORDER BY d,entry_title,season_number NULLS FIRST,episode_number NULLS FIRST,release_type LIMIT 1000`, a.TrackingID(), fromD, toD, region, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		var r Release
		var d time.Time
		if err = rows.Scan(&d, &r.CatalogID, &r.Kind, &r.Title, &r.SeasonNumber, &r.EpisodeNumber, &r.EntryID, &r.EntryTitle, &r.MediaType, &r.ImageURL, &r.BackdropURL, &r.ParentCatalog, &r.ReleaseType, &r.Following, &r.Favourite, &r.Watchlist); err != nil {
			return nil, err
		}
		r.Date = d.Format("2006-01-02")
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpNext is the next unwatched, already aired regular episode of an in-progress show.
// Home lists them by the show's latest real play, so a bulk mark does not move a show up.
type UpNext struct {
	Entry   Summary `json:"entry"`
	Episode Release `json:"episode"`
}

// Home bundles the first screen in one round trip.
type Home struct {
	Continue []Summary `json:"continue"`
	UpNext   []UpNext  `json:"up_next"`
	Upcoming []Release `json:"upcoming"`
	Planned  []Summary `json:"planned"`
	Recent   []Summary `json:"recently_finished"`
	Stats    Stats     `json:"stats"`
}

func (s Store) Home(ctx context.Context, a identity.Actor, now time.Time) (Home, error) {
	out := Home{Continue: []Summary{}, UpNext: []UpNext{}, Upcoming: []Release{}, Planned: []Summary{}, Recent: []Summary{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	var err error
	list := func(where, order string, limit int) ([]Summary, error) {
		rows, err := s.Pool.Query(ctx, "SELECT "+columns+from+" WHERE e.profile_id=$1 AND "+where+" ORDER BY "+order+" LIMIT "+strconv.Itoa(limit), a.TrackingID())
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []Summary{}
		for rows.Next() {
			var e Summary
			if err = rows.Scan(fields(&e)...); err != nil {
				return nil, err
			}
			items = append(items, e)
		}
		return items, rows.Err()
	}
	if out.Continue, err = list("e.status='in_progress'", "e.last_activity_at DESC NULLS LAST,e.id DESC", 12); err != nil {
		return out, err
	}
	if out.Planned, err = list("e.status='planned'", "e.id DESC", 12); err != nil {
		return out, err
	}
	if out.Recent, err = list("e.status='completed'", "e.finished_at DESC NULLS LAST,e.id DESC", 12); err != nil {
		return out, err
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	rows, err := s.Pool.Query(ctx, "SELECT "+columns+`,n.release_date,n.id,n.title,n.season_number,n.episode_number`+from+`
	JOIN catalog_items n ON n.id=e.next_item_id
	WHERE e.profile_id=$1 AND e.next_item_id IS NOT NULL AND e.status IN ('in_progress','planned') AND e.progress>0 AND (n.release_date IS NULL OR n.release_date<=$2)
	ORDER BY `+lastPlayedSQL+` DESC NULLS LAST,e.last_activity_at DESC NULLS LAST,e.id DESC LIMIT 12`, a.TrackingID(), today)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var u UpNext
		var d *time.Time
		if err = rows.Scan(append(fields(&u.Entry), &d, &u.Episode.CatalogID, &u.Episode.Title, &u.Episode.SeasonNumber, &u.Episode.EpisodeNumber)...); err != nil {
			rows.Close()
			return out, err
		}
		u.Episode.Kind = "episode"
		if d != nil {
			u.Episode.Date = d.Format("2006-01-02")
		}
		u.Episode.EntryID, u.Episode.EntryTitle, u.Episode.MediaType, u.Episode.ImageURL = u.Entry.ID, u.Entry.Title, u.Entry.MediaType, u.Entry.ImageURL
		u.Episode.BackdropURL = u.Entry.BackdropURL
		if u.Entry.CatalogID != nil {
			u.Episode.ParentCatalog = *u.Entry.CatalogID
		}
		out.UpNext = append(out.UpNext, u)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	upcoming, err := s.Calendar(ctx, a, today, today.AddDate(0, 0, 14))
	if err != nil {
		return out, err
	}
	if len(upcoming) > 10 {
		upcoming = upcoming[:10]
	}
	out.Upcoming = upcoming
	out.Stats, err = s.Stats(ctx, a)
	return out, err
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

const windowDaysSQL = `WITH bounds AS (
 SELECT (date_trunc('day',$2::timestamptz AT TIME ZONE 'UTC') + CASE WHEN $2::timestamptz AT TIME ZONE 'UTC'=date_trunc('day',$2::timestamptz AT TIME ZONE 'UTC') THEN interval '0' ELSE interval '1 day' END) AT TIME ZONE 'UTC' AS lo,
 date_trunc('day',$3::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS hi),
 window_days AS (
 SELECT d.day,d.media_type,d.plays,d.minutes,d.unknown_runtime,0 AS sessions FROM play_days d,bounds b WHERE d.profile_id=$1 AND d.day>=(b.lo AT TIME ZONE 'UTC')::date AND d.day<(b.hi AT TIME ZONE 'UTC')::date
 UNION ALL
 SELECT (p.played_at AT TIME ZONE 'UTC')::date,p.media_type,count(*)::int,COALESCE(sum(p.minutes),0)::int,count(*) FILTER(WHERE p.minutes IS NULL)::int,0 FROM plays p,bounds b
 WHERE p.profile_id=$1 AND p.played_at>=$2 AND p.played_at<$3 AND (p.played_at<b.lo OR p.played_at>=b.hi) AND NOT p.marked
 GROUP BY 1,2
 UNION ALL
 SELECT (k.finished_at AT TIME ZONE 'UTC')::date,e.media_type,0,COALESCE(sum(k.minutes),0)::int,0,count(*)::int FROM checkins k JOIN library_entries e ON e.profile_id=k.profile_id AND e.id=k.entry_id
 WHERE k.profile_id=$1 AND k.kind='session' AND k.status='finished' AND k.play_id IS NULL AND k.finished_at>=$2 AND k.finished_at<$3
 GROUP BY 1,2)`
