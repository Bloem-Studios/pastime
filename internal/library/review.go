package library

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

// Streak is a run of consecutive UTC days with at least one play or finished session.
type Streak struct {
	Days int    `json:"days"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Streaks holds the run that is still going (it ends today or yesterday, so a day
// without activity yet does not break it) and the longest run inside a window.
type Streaks struct {
	Current Streak `json:"current"`
	Longest Streak `json:"longest"`
}

func utcDate(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// longestStreak finds the longest run of consecutive dates in days, which is sorted.
func longestStreak(days []DayStats) Streak {
	var best, run Streak
	var prev time.Time
	for _, d := range days {
		day, err := time.Parse(time.DateOnly, d.Date)
		if err != nil {
			continue
		}
		if run.Days > 0 && day.Equal(prev.AddDate(0, 0, 1)) {
			run.Days++
			run.To = d.Date
		} else {
			run = Streak{Days: 1, From: d.Date, To: d.Date}
		}
		prev = day
		if run.Days > best.Days {
			best = run
		}
	}
	return best
}

// activeDaysSQL lists a profile's active UTC days ($1) up to and including day $2.
const activeDaysSQL = `SELECT day FROM play_days WHERE profile_id=$1 AND day<=$2::date
	UNION SELECT (finished_at AT TIME ZONE 'UTC')::date FROM checkins
	WHERE profile_id=$1 AND kind='session' AND status='finished' AND play_id IS NULL AND finished_at<(($2::date+1)::timestamp AT TIME ZONE 'UTC')`

func currentStreak(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, profile int64, today time.Time) (Streak, error) {
	var from, to time.Time
	var days int
	err := q.QueryRow(ctx, `WITH d AS (`+activeDaysSQL+`), g AS (SELECT day,day-(row_number() OVER (ORDER BY day))::int grp FROM d)
		SELECT min(day),max(day),count(*)::int FROM g GROUP BY grp ORDER BY max(day) DESC LIMIT 1`, profile, today).Scan(&from, &to, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		return Streak{}, nil
	}
	if err != nil {
		return Streak{}, err
	}
	if to.Before(today.AddDate(0, 0, -1)) {
		return Streak{}, nil
	}
	return Streak{Days: days, From: from.Format(time.DateOnly), To: to.Format(time.DateOnly)}, nil
}

// YearType is one media type's share of a year.
type YearType struct {
	MediaType string `json:"media_type"`
	Plays     int    `json:"plays"`
	Sessions  int    `json:"sessions"`
	Minutes   int    `json:"minutes"`
}

// YearStats totals one calendar year (UTC).
type YearStats struct {
	Year       int        `json:"year"`
	Plays      int        `json:"plays"`
	Sessions   int        `json:"sessions"`
	Minutes    int        `json:"minutes"`
	ActiveDays int        `json:"active_days"`
	Completed  int        `json:"completed"`
	ByType     []YearType `json:"by_type"`
}

// Years totals every year with activity, oldest first. Plays come from the daily
// rollup, sessions from the finished sessions that recorded no play, and titles
// finished from each title's latest completion, as in the window statistics.
func (s Store) Years(ctx context.Context, a identity.Actor) ([]YearStats, error) {
	out := []YearStats{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	thisYear := time.Now().UTC().Year()
	rows, err := s.Pool.Query(ctx, `WITH days AS (
		SELECT day,media_type,plays,minutes,0 AS sessions FROM play_days WHERE profile_id=$1
		UNION ALL
		SELECT (k.finished_at AT TIME ZONE 'UTC')::date,e.media_type,0,COALESCE(k.minutes,0),1 FROM checkins k JOIN library_entries e ON e.profile_id=k.profile_id AND e.id=k.entry_id
		WHERE k.profile_id=$1 AND k.kind='session' AND k.status='finished' AND k.play_id IS NULL),
	years AS (SELECT extract(year FROM day)::int y,media_type,plays,minutes,sessions,day FROM days WHERE extract(year FROM day) BETWEEN 1900 AND $2),
	done AS (SELECT extract(year FROM e.finished_at AT TIME ZONE 'UTC')::int y,count(*)::int n FROM library_entries e
		WHERE e.profile_id=$1 AND e.status='completed' AND e.finished_at IS NOT NULL AND NOT `+finishedByMark+` GROUP BY 1)
	SELECT x.y,x.media_type,x.plays,x.sessions,x.minutes,x.active,COALESCE(done.n,0) FROM (
		SELECT y,CASE WHEN GROUPING(media_type)=1 THEN '' ELSE media_type END media_type,sum(plays)::int plays,sum(sessions)::int sessions,sum(minutes)::bigint minutes,count(DISTINCT day)::int active
		FROM years GROUP BY GROUPING SETS ((y),(y,media_type))) x LEFT JOIN done ON done.y=x.y
	ORDER BY x.y,x.media_type<>'',x.minutes DESC,x.media_type`, a.TrackingID(), thisYear)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var y YearStats
		var t YearType
		var active int
		if err = rows.Scan(&y.Year, &t.MediaType, &t.Plays, &t.Sessions, &t.Minutes, &active, &y.Completed); err != nil {
			return out, err
		}
		if t.MediaType == "" {
			y.Plays, y.Sessions, y.Minutes, y.ActiveDays, y.ByType = t.Plays, t.Sessions, t.Minutes, active, []YearType{}
			out = append(out, y)
			continue
		}
		if n := len(out); n > 0 && out[n-1].Year == y.Year {
			out[n-1].ByType = append(out[n-1].ByType, t)
		}
	}
	return out, rows.Err()
}

// ReviewTitle is a title and the time and plays it took in the reviewed year.
type ReviewTitle struct {
	Summary
	Plays   int `json:"plays"`
	Minutes int `json:"minutes"`
}

// ReviewType is one media type's year with its three biggest titles by time.
type ReviewType struct {
	MediaType string        `json:"media_type"`
	Plays     int           `json:"plays"`
	Sessions  int           `json:"sessions"`
	Minutes   int           `json:"minutes"`
	Titles    []ReviewTitle `json:"titles"`
}

// MonthStats totals one month of the reviewed year.
type MonthStats struct {
	Month      int `json:"month"`
	Plays      int `json:"plays"`
	Sessions   int `json:"sessions"`
	Minutes    int `json:"minutes"`
	ActiveDays int `json:"active_days"`
}

// Review is a person's year: its statistics, last year to compare with, and the
// moments worth showing.
type Review struct {
	Year int `json:"year"`
	// Complete is false while the year is still running.
	Complete   bool         `json:"complete"`
	Stats      Insights     `json:"stats"`
	Previous   *YearStats   `json:"previous"`
	Months     []MonthStats `json:"months"`
	Types      []ReviewType `json:"types"`
	BusiestDay *DayStats    `json:"busiest_day"`
	FirstPlay  *HistoryItem `json:"first_play"`
	// NewTitles were played for the first time this year; Returning had earlier plays.
	NewTitles int `json:"new_titles"`
	Returning int `json:"returning"`
}

// Review builds the caller's Year in Review for a calendar year (UTC) up to this one.
func (s Store) Review(ctx context.Context, a identity.Actor, year int, now time.Time) (Review, error) {
	out := Review{Year: year, Months: []MonthStats{}, Types: []ReviewType{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	now = now.UTC()
	if year < 1900 || year > now.Year() {
		return out, fault.Invalid("Choose a year from 1900 up to this one.")
	}
	fromT := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	toT := fromT.AddDate(1, 0, 0)
	out.Complete = !now.Before(toT)
	var err error
	if out.Stats, err = s.Insights(ctx, a, fromT, toT); err != nil {
		return out, err
	}
	years, err := s.Years(ctx, a)
	if err != nil {
		return out, err
	}
	for i := range years {
		if years[i].Year == year-1 {
			out.Previous = &years[i]
		}
	}
	for m := 1; m <= 12; m++ {
		out.Months = append(out.Months, MonthStats{Month: m})
	}
	for i, d := range out.Stats.Days {
		day, err := time.Parse(time.DateOnly, d.Date)
		if err != nil || day.Year() != year {
			continue
		}
		m := &out.Months[day.Month()-1]
		m.Plays += d.Plays
		m.Sessions += d.Sessions
		m.Minutes += d.Minutes
		m.ActiveDays++
		if b := out.BusiestDay; b == nil || d.Minutes > b.Minutes || d.Minutes == b.Minutes && d.Plays+d.Sessions > b.Plays+b.Sessions {
			out.BusiestDay = &out.Stats.Days[i]
		}
	}
	uid := a.TrackingID()
	byType := map[string]int{}
	for _, t := range out.Stats.ByType {
		if t.Plays == 0 && t.Sessions == 0 {
			continue
		}
		byType[t.MediaType] = len(out.Types)
		out.Types = append(out.Types, ReviewType{MediaType: t.MediaType, Plays: t.Plays, Sessions: t.Sessions, Minutes: t.Minutes, Titles: []ReviewTitle{}})
	}
	rows, err := s.Pool.Query(ctx, `WITH w AS (
		SELECT entry_id,count(*) plays,COALESCE(sum(minutes),0) minutes FROM plays WHERE profile_id=$1 AND played_at>=$2 AND played_at<$3 AND NOT marked GROUP BY 1
		UNION ALL
		SELECT entry_id,0,COALESCE(sum(minutes),0) FROM checkins WHERE profile_id=$1 AND kind='session' AND status='finished' AND play_id IS NULL AND finished_at>=$2 AND finished_at<$3 GROUP BY 1),
	t AS (SELECT entry_id,sum(plays)::int plays,sum(minutes)::int minutes FROM w GROUP BY 1),
	r AS (SELECT t.*,row_number() OVER (PARTITION BY e.media_type ORDER BY t.minutes DESC,t.plays DESC,t.entry_id DESC) n
		FROM t JOIN library_entries e ON e.profile_id=$1 AND e.id=t.entry_id)
	SELECT `+columns+`,r.plays,r.minutes`+from+` JOIN r ON r.entry_id=e.id WHERE e.profile_id=$1 AND r.n<=3 ORDER BY e.media_type,r.n`, uid, fromT, toT)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t ReviewTitle
		if err = rows.Scan(append(fields(&t.Summary), &t.Plays, &t.Minutes)...); err != nil {
			rows.Close()
			return out, err
		}
		if i, ok := byType[t.MediaType]; ok {
			out.Types[i].Titles = append(out.Types[i].Titles, t)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	var first HistoryItem
	err = s.Pool.QueryRow(ctx, historySelect+` AND p.played_at>=$2 AND p.played_at<$3 AND NOT p.marked ORDER BY p.played_at,p.id LIMIT 1`, uid, fromT, toT).Scan(historyFields(&first)...)
	switch {
	case err == nil:
		out.FirstPlay = &first
	case !errors.Is(err, pgx.ErrNoRows):
		return out, err
	}
	err = s.Pool.QueryRow(ctx, `WITH hit AS (SELECT DISTINCT entry_id FROM plays WHERE profile_id=$1 AND played_at>=$2 AND played_at<$3 AND NOT marked
			UNION SELECT entry_id FROM checkins WHERE profile_id=$1 AND kind='session' AND status='finished' AND finished_at>=$2 AND finished_at<$3),
		seen AS (SELECT hit.entry_id,EXISTS(SELECT 1 FROM plays q WHERE q.profile_id=$1 AND q.entry_id=hit.entry_id AND q.played_at<$2)
			OR EXISTS(SELECT 1 FROM checkins k WHERE k.profile_id=$1 AND k.entry_id=hit.entry_id AND k.kind='session' AND k.status='finished' AND k.finished_at<$2) before FROM hit)
		SELECT count(*) FILTER(WHERE NOT before)::int,count(*) FILTER(WHERE before)::int FROM seen`, uid, fromT, toT).Scan(&out.NewTitles, &out.Returning)
	return out, err
}
