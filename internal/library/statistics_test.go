package library_test

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func track(t *testing.T, f *testkit.Fixture, s library.Store, catalogID int64) int64 {
	t.Helper()
	m, err := s.Track(t.Context(), f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: catalogID, Status: "in_progress"})
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func playAt(t *testing.T, f *testkit.Fixture, entryID, catalogID int64, at time.Time) {
	t.Helper()
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,source) VALUES($1,$2,$3,1,$4,'manual')`,
		f.Owner.TrackingID(), entryID, catalogID, at); err != nil {
		t.Fatal(err)
	}
}

func session(t *testing.T, f *testkit.Fixture, s library.Store, catalogID int64, start time.Time, minutes int, in library.FinishInput) {
	t.Helper()
	m, err := s.CheckIn(t.Context(), f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: catalogID, StartedAt: &start})
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(time.Duration(minutes) * time.Minute)
	in.FinishedAt, in.Minutes = &end, &minutes
	if _, err = s.FinishCheckin(t.Context(), f.Owner, m.ID, testkit.Key(t, f), in); err != nil {
		t.Fatal(err)
	}
}

func day(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

func TestSessionTimeCountsOnceInStatistics(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	book := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,credits) VALUES(0,'book','openlibrary','OL2W','Book','pages',300,'{"Ann Author"}') RETURNING id`)
	// Both sessions on one UTC day in the past (yesterday at noon), whatever the time the test runs.
	start := day(time.Now().UTC()).AddDate(0, 0, -1).Add(12 * time.Hour)
	pages := 40
	session(t, f, s, book, start, 30, library.FinishInput{Amount: &pages})
	session(t, f, s, book, start.Add(time.Hour), 45, library.FinishInput{Completed: true})
	today := day(time.Now().UTC())
	stats, err := s.Insights(ctx, f.Owner, today.AddDate(0, 0, -1), today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	// The first session recorded no play; the second finished the book, and its time
	// rides on the completion play.
	if stats.Minutes != 75 || stats.Plays != 1 || stats.Sessions != 1 || len(stats.Days) == 0 {
		t.Fatalf("totals %+v", stats)
	}
	var bookType *library.TypeStats
	for i := range stats.ByType {
		if stats.ByType[i].MediaType == "book" {
			bookType = &stats.ByType[i]
		}
	}
	if bookType == nil || bookType.Minutes != 75 || bookType.Sessions != 1 || bookType.Plays != 1 {
		t.Fatalf("by type %+v", stats.ByType)
	}
	if stats.Streaks.Current.Days != 1 || stats.Streaks.Longest.Days != 1 {
		t.Fatalf("streaks %+v", stats.Streaks)
	}
	if len(stats.Creators) != 1 || stats.Creators[0] != (library.Creator{MediaType: "book", Name: "Ann Author", Count: 1}) {
		t.Fatalf("creators %+v", stats.Creators)
	}
	entry, _ := s.EntryForCatalog(ctx, f.Owner, book)
	h, err := s.PlayHistory(ctx, f.Owner, entry.ID)
	if err != nil || len(h.Items) != 2 || h.Minutes != 75 || h.Plays != 1 || h.Sessions != 1 {
		t.Fatalf("history lists the finishing session once: %+v %v", h, err)
	}
}

func TestStreaksAndYears(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	film := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb','700','Film','minutes',100,100) RETURNING id`)
	entry := track(t, f, s, film)
	today := day(time.Now().UTC())
	// A run of two ending three days ago, then a gap: no current streak.
	for _, back := range []int{3, 4, 10, 11, 12} {
		playAt(t, f, entry, film, today.AddDate(0, 0, -back).Add(12*time.Hour))
	}
	stats, err := s.Insights(ctx, f.Owner, today.AddDate(0, 0, -30), today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Streaks.Current.Days != 0 || stats.Streaks.Longest.Days != 3 || stats.Streaks.Longest.To != today.AddDate(0, 0, -10).Format(time.DateOnly) {
		t.Fatalf("streaks %+v", stats.Streaks)
	}
	// Yesterday and the day before keep a streak going today, before any play today.
	playAt(t, f, entry, film, today.AddDate(0, 0, -1).Add(time.Hour))
	playAt(t, f, entry, film, today.AddDate(0, 0, -2).Add(time.Hour))
	if stats, err = s.Insights(ctx, f.Owner, today.AddDate(0, 0, -30), today.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if stats.Streaks.Current.Days != 4 || stats.Streaks.Current.To != today.AddDate(0, 0, -1).Format(time.DateOnly) || stats.Streaks.Longest.Days != 4 {
		t.Fatalf("streaks after yesterday %+v", stats.Streaks)
	}
	playAt(t, f, entry, film, time.Date(2024, 6, 1, 20, 0, 0, 0, time.UTC))
	years, err := s.Years(ctx, f.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(years) < 2 || years[0].Year != 2024 || years[0].Plays != 1 || years[0].Minutes != 100 || years[0].ActiveDays != 1 ||
		len(years[0].ByType) != 1 || years[0].ByType[0].MediaType != "movie" {
		t.Fatalf("years %+v", years)
	}
	total := 0
	for _, y := range years {
		total += y.Plays
	}
	if total != 8 {
		t.Fatalf("plays across years %d: %+v", total, years)
	}
}

func TestYearInReview(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	a := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes,credits) VALUES(0,'movie','tmdb','701','Alpha','minutes',120,120,'{"Dee Director"}') RETURNING id`)
	b := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb','702','Beta','minutes',90,90) RETURNING id`)
	game := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,credits) VALUES(0,'game','igdb','703','Gamma','minutes','{"Dev Co"}') RETURNING id`)
	ea, eb := track(t, f, s, a), track(t, f, s, b)
	playAt(t, f, ea, a, time.Date(2025, 1, 5, 20, 0, 0, 0, time.UTC))
	playAt(t, f, ea, a, time.Date(2025, 7, 1, 20, 0, 0, 0, time.UTC))
	playAt(t, f, eb, b, time.Date(2024, 12, 31, 20, 0, 0, 0, time.UTC))
	playAt(t, f, eb, b, time.Date(2025, 2, 10, 20, 0, 0, 0, time.UTC))
	// Check-ins start within the last week; move this finished session back into 2025.
	session(t, f, s, game, time.Now().UTC().Add(-6*time.Hour), 300, library.FinishInput{})
	if _, err := f.Pool.Exec(ctx, `UPDATE checkins SET started_at=$2::timestamptz,finished_at=$2::timestamptz+interval '5 hours' WHERE profile_id=$1 AND catalog_item_id=$3`,
		f.Owner.TrackingID(), time.Date(2025, 3, 3, 10, 0, 0, 0, time.UTC), game); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	r, err := s.Review(ctx, f.Owner, 2025, now)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || r.Stats.Plays != 3 || r.Stats.Sessions != 1 || r.Stats.Minutes != 120*2+90+300 {
		t.Fatalf("totals %+v", r.Stats)
	}
	if r.FirstPlay == nil || r.FirstPlay.EntryTitle != "Alpha" || r.NewTitles != 2 || r.Returning != 1 {
		t.Fatalf("first %+v new %d returning %d", r.FirstPlay, r.NewTitles, r.Returning)
	}
	if r.Previous == nil || r.Previous.Year != 2024 || r.Previous.Plays != 1 {
		t.Fatalf("previous %+v", r.Previous)
	}
	if len(r.Months) != 12 || r.Months[0].Plays != 1 || r.Months[2].Sessions != 1 || r.Months[2].Minutes != 300 || r.Months[6].Plays != 1 {
		t.Fatalf("months %+v", r.Months)
	}
	if r.BusiestDay == nil || r.BusiestDay.Date != "2025-03-03" {
		t.Fatalf("busiest %+v", r.BusiestDay)
	}
	if len(r.Types) != 2 || r.Types[0].MediaType != "movie" && r.Types[1].MediaType != "movie" {
		t.Fatalf("types %+v", r.Types)
	}
	for _, typ := range r.Types {
		switch typ.MediaType {
		case "movie":
			if len(typ.Titles) != 2 || typ.Titles[0].Title != "Alpha" || typ.Titles[0].Plays != 2 || typ.Titles[0].Minutes != 240 {
				t.Fatalf("movies %+v", typ.Titles)
			}
		case "game":
			if typ.Sessions != 1 || len(typ.Titles) != 1 || typ.Titles[0].Minutes != 300 {
				t.Fatalf("games %+v", typ)
			}
		}
	}
	creators := map[string]string{}
	for _, c := range r.Stats.Creators {
		creators[c.MediaType] = c.Name
	}
	if creators["game"] != "Dev Co" || creators["movie"] != "Dee Director" {
		t.Fatalf("creators %+v", r.Stats.Creators)
	}
	if r, err = s.Review(ctx, f.Owner, 2026, now); err != nil || r.Complete || r.Stats.Plays != 0 || r.Previous == nil || r.Previous.Year != 2025 {
		t.Fatalf("running year %+v %v", r, err)
	}
	if _, err = s.Review(ctx, f.Owner, 2027, now); err == nil {
		t.Fatal("a future year")
	}
}

func TestPlaysDateTheirEntry(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	film := func(id string) int64 {
		return catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb',$1,$1,'minutes',100,100) RETURNING id`, id)
	}
	// History synced today with a play from 2025 completes the entry on the play's date.
	synced := film("801")
	m, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: synced})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 5, 1, 20, 0, 0, 0, time.UTC)
	if _, err = s.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), library.PlayInput{Version: m.Version, Items: []library.PlayItem{{CatalogID: synced, PlayedAt: &at}}}); err != nil {
		t.Fatal(err)
	}
	e, _ := s.EntryForCatalog(ctx, f.Owner, synced)
	if e.Status != "completed" || e.FinishedAt == nil || !e.FinishedAt.Equal(at) || e.StartedAt == nil || !e.StartedAt.Equal(at) {
		t.Fatalf("entry dates %+v %v %v", e.Status, e.StartedAt, e.FinishedAt)
	}
	years, err := s.Years(ctx, f.Owner)
	if err != nil || len(years) != 1 || years[0].Year != 2025 || years[0].Completed != 1 {
		t.Fatalf("years %+v %v", years, err)
	}
	// Entries dated by the old rule are corrected; a status set by hand keeps its date.
	stale := film("802")
	m, _ = s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: stale})
	old := time.Date(2024, 3, 1, 21, 0, 0, 0, time.UTC)
	if _, err = s.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), library.PlayInput{Version: m.Version, Items: []library.PlayItem{{CatalogID: stale, PlayedAt: &old}}}); err != nil {
		t.Fatal(err)
	}
	manual := film("803")
	mm, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: manual, Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	playAt(t, f, mm.ID, manual, old)
	if _, err = f.Pool.Exec(ctx, `UPDATE library_entries SET started_at=clock_timestamp(),finished_at=clock_timestamp() WHERE profile_id=$1 AND id=$2`, f.Owner.TrackingID(), m.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := s.EntryForCatalog(ctx, f.Owner, manual)
	migration, err := os.ReadFile("../../migrations/00060_dates_from_plays.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if e, _ = s.EntryForCatalog(ctx, f.Owner, stale); !e.FinishedAt.Equal(old) || !e.StartedAt.Equal(old) {
		t.Fatalf("corrected dates %v %v", e.StartedAt, e.FinishedAt)
	}
	if e, _ = s.EntryForCatalog(ctx, f.Owner, manual); e.FinishedAt == nil || !e.FinishedAt.Equal(*before.FinishedAt) || !e.StartedAt.Equal(old) {
		t.Fatalf("hand-set completion %v %v (was %v)", e.StartedAt, e.FinishedAt, before.FinishedAt)
	}
}

// A bulk "mark as played" (three or more episodes in one minute) counts toward progress
// but not toward time, plays or titles finished; two in a minute are still plays.
func TestBulkMarkedPlaysStayOutOfStatistics(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	show := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,runtime_minutes) VALUES(0,'series','tmdb','276515','Treasure Show','episodes',43) RETURNING id`)
	season := catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number) VALUES('tmdb','s1','season','Season 1','episodes',$1,1) RETURNING id`, show)
	var eps []int64
	for n := 1; n <= 6; n++ {
		eps = append(eps, catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number,runtime_minutes) VALUES('tmdb',$1,'episode',$1,'episodes',$2,1,$3,43) RETURNING id`, "e"+strconv.Itoa(n), season, n))
	}
	entry := track(t, f, s, show)
	today := day(time.Now().UTC())
	marked := today.AddDate(0, 0, -3).Add(19*time.Hour + 34*time.Minute)
	// Three episodes recorded in one minute, one statement each as Bloem sends them.
	for i, ep := range eps[:3] {
		playAt(t, f, entry, ep, marked.Add(time.Duration(i)*10*time.Second))
	}
	// Two in one minute (a real finish and a quick next) stay plays; one more elsewhere.
	real := today.AddDate(0, 0, -2).Add(21 * time.Hour)
	playAt(t, f, entry, eps[3], real)
	playAt(t, f, entry, eps[4], real.Add(20*time.Second))
	playAt(t, f, entry, eps[5], today.AddDate(0, 0, -1).Add(20*time.Hour))
	var flagged int
	if err := f.Pool.QueryRow(ctx, `SELECT count(*) FROM plays WHERE profile_id=$1 AND marked`, f.Owner.TrackingID()).Scan(&flagged); err != nil || flagged != 3 {
		t.Fatalf("marked plays = %d %v, want 3", flagged, err)
	}
	stats, err := s.Insights(ctx, f.Owner, today.AddDate(0, 0, -7), today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Plays != 3 || stats.Minutes != 3*43 || len(stats.Days) != 2 {
		t.Fatalf("stats count the marked burst: plays %d minutes %d days %d", stats.Plays, stats.Minutes, len(stats.Days))
	}
	page, err := s.History(ctx, f.Owner, library.HistoryFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	markedRows := 0
	for _, h := range page.Items {
		if h.Marked {
			markedRows++
		}
	}
	if len(page.Items) != 6 || markedRows != 3 {
		t.Fatalf("history lists %d items, %d marked", len(page.Items), markedRows)
	}
	// Removing a marked play leaves the daily totals alone.
	var markedID int64
	if err := f.Pool.QueryRow(ctx, `SELECT id FROM plays WHERE profile_id=$1 AND marked LIMIT 1`, f.Owner.TrackingID()).Scan(&markedID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeletePlay(ctx, f.Owner, markedID, testkit.Key(t, f)); err != nil {
		t.Fatal(err)
	}
	if stats, err = s.Insights(ctx, f.Owner, today.AddDate(0, 0, -7), today.AddDate(0, 0, 1)); err != nil || stats.Plays != 3 || stats.Minutes != 129 {
		t.Fatalf("after removing a marked play: %+v %v", stats, err)
	}
	var negative bool
	if err := f.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM play_days WHERE plays<0 OR minutes<0)`).Scan(&negative); err != nil || negative {
		t.Fatal("the daily rollup went negative", err)
	}
}
