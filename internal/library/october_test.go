package library_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestLibraryFindsAnEntryByProviderRecordOrExternalID(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var movie, other int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,external_ids) VALUES('tmdb','603','movie','Synthetic Movie','minutes','{"imdb":"tt0133093"}') RETURNING id`).Scan(&movie); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('tmdb','604','movie','Other Movie','minutes') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{movie, other} {
		if _, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ provider, id string }{{"tmdb", "603"}, {"imdb", "tt0133093"}} {
		got, err := s.Entries(ctx, f.Owner, library.Filter{Provider: c.provider, ProviderID: c.id})
		if err != nil || len(got.Items) != 1 || got.Items[0].Title != "Synthetic Movie" {
			t.Fatal(c, got.Items, err)
		}
	}
	if _, err := s.Entries(ctx, f.Owner, library.Filter{Provider: "tmdb"}); err == nil {
		t.Fatal("provider without an id accepted")
	}
	other2 := f.User(t, "provider_other", "member")
	if got, err := s.Entries(ctx, other2, library.Filter{Provider: "tmdb", ProviderID: "603"}); err != nil || len(got.Items) != 0 {
		t.Fatal("another account's entry found", got.Items, err)
	}
}

func TestTierBoardReplacesTiersWithoutReorderingTheList(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	list, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Synthetic tiers"})
	if err != nil {
		t.Fatal(err)
	}
	version := list.Version
	var entries []string
	for _, title := range []string{"One", "Two", "Three"} {
		e, err := s.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: title, MediaType: "movie", Unit: "minutes"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := s.Membership(ctx, f.Owner, list.ID, e.ID, testkit.Key(t, f), version, true)
		if err != nil {
			t.Fatal(err)
		}
		version = m.Version
		entries = append(entries, strconv.FormatInt(e.ID, 10))
	}
	in := library.TierInput{Version: version}
	color := "#FF0000"
	in.Tiers = append(in.Tiers, struct {
		Label    string   `json:"label"`
		Color    *string  `json:"color"`
		EntryIDs []string `json:"entry_ids"`
	}{"S", &color, []string{entries[2], entries[0]}}, struct {
		Label    string   `json:"label"`
		Color    *string  `json:"color"`
		EntryIDs []string `json:"entry_ids"`
	}{"A", nil, nil})
	saved, err := s.SaveTiers(ctx, f.Owner, list.ID, testkit.Key(t, f), in)
	if err != nil {
		t.Fatal(err)
	}
	board, err := s.Tiers(ctx, f.Owner, list.ID)
	if err != nil || board.Version != saved.Version || len(board.Tiers) != 2 || board.Tiers[0].Label != "S" || *board.Tiers[0].Color != "#ff0000" {
		t.Fatal(board, err)
	}
	if fmt.Sprint(board.Tiers[0].EntryIDs) != fmt.Sprint([]string{entries[2], entries[0]}) || len(board.Tiers[1].EntryIDs) != 0 || fmt.Sprint(board.Unranked) != fmt.Sprint([]string{entries[1]}) {
		t.Fatal("board order", board)
	}
	members, err := s.Entries(ctx, f.Owner, library.Filter{ListID: list.ID})
	if err != nil || len(members.Items) != 3 || members.Items[0].Title != "One" {
		t.Fatal("tiers reordered the list", members.Items, err)
	}
	// A member twice, a non-member and a stale version are refused.
	in.Version = saved.Version
	in.Tiers[1].EntryIDs = []string{entries[2]}
	if _, err = s.SaveTiers(ctx, f.Owner, list.ID, testkit.Key(t, f), in); err == nil {
		t.Fatal("member placed twice")
	}
	in.Tiers[1].EntryIDs = []string{"999999999"}
	if _, err = s.SaveTiers(ctx, f.Owner, list.ID, testkit.Key(t, f), in); err == nil {
		t.Fatal("non-member placed")
	}
	in.Tiers[1].EntryIDs = nil
	in.Version = version
	if _, err = s.SaveTiers(ctx, f.Owner, list.ID, testkit.Key(t, f), in); err == nil {
		t.Fatal("stale version accepted")
	}
	other := f.User(t, "tier_other", "member")
	if _, err = s.Tiers(ctx, other, list.ID); err == nil {
		t.Fatal("another account read the board")
	}
}

func TestHomeMixRowsAndCardFieldsAreValidated(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	p, err := s.Preferences(ctx, f.Owner)
	if err != nil {
		t.Fatal(err)
	}
	p.HomeRows = append(p.HomeRows, library.HomeRow{Kind: "mix", ID: "watch", Title: "Watch next", MediaTypes: []string{"movie", "series"}, Statuses: []string{"planned"}, Visible: true})
	p.CardFields = map[string][]string{"book": {"year", "progress"}}
	saved, err := s.SavePreferences(ctx, f.Owner, p)
	if err != nil {
		t.Fatal(err)
	}
	mix := saved.HomeRows[len(saved.HomeRows)-1]
	if mix.Sort != "activity" || mix.Limit != 20 {
		t.Fatal("mix defaults", mix)
	}
	got, err := s.Preferences(ctx, f.Owner)
	if err != nil || len(got.CardFields["book"]) != 2 || got.HomeRows[len(got.HomeRows)-1].Title != "Watch next" {
		t.Fatal(got, err)
	}
	for _, bad := range []func(*library.Preferences){
		func(p *library.Preferences) { p.HomeRows[len(p.HomeRows)-1].MediaTypes = nil },
		func(p *library.Preferences) { p.HomeRows[len(p.HomeRows)-1].MediaTypes = []string{"spaceship"} },
		func(p *library.Preferences) { p.HomeRows[len(p.HomeRows)-1].Limit = 31 },
		func(p *library.Preferences) { p.HomeRows[0].Title = "Not a mix" },
		func(p *library.Preferences) { p.CardFields = map[string][]string{"book": {"year", "year"}} },
		func(p *library.Preferences) { p.CardFields = map[string][]string{"book": {"secret"}} },
	} {
		next := got
		next.HomeRows = append([]library.HomeRow(nil), got.HomeRows...)
		bad(&next)
		if _, err := s.SavePreferences(ctx, f.Owner, next); err == nil {
			t.Fatal("invalid preferences accepted", next)
		}
	}
}

func TestCalendarShowsRegionalMovieReleaseDates(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var movie int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,release_date,details) VALUES('tmdb','9001','movie','Regional Movie','minutes','2030-10-20',
		'{"release_dates":{"NL":{"theatrical":"2030-11-01","digital":"2030-12-01","physical":"2031-01-10"}}}') RETURNING id`).Scan(&movie); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: movie}); err != nil {
		t.Fatal(err)
	}
	from, to := time.Date(2030, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)
	got, err := s.Calendar(ctx, f.Owner, from, to)
	if err != nil || len(got) != 1 || got[0].Date != "2030-10-20" || got[0].ReleaseType != "" {
		t.Fatal("primary date", got, err)
	}
	p, _ := s.Preferences(ctx, f.Owner)
	p.ReleaseRegion, p.CalendarReleaseTypes = "nl", []string{"digital", "theatrical"}
	if _, err = s.SavePreferences(ctx, f.Owner, p); err != nil {
		t.Fatal(err)
	}
	got, err = s.Calendar(ctx, f.Owner, from, to)
	if err != nil || len(got) != 2 || got[0].Date != "2030-11-01" || got[0].ReleaseType != "theatrical" || got[1].Date != "2030-12-01" || got[1].ReleaseType != "digital" {
		t.Fatal("regional dates", got, err)
	}
	// A region without dates for the movie keeps its primary date.
	p, _ = s.Preferences(ctx, f.Owner)
	p.ReleaseRegion = "US"
	if _, err = s.SavePreferences(ctx, f.Owner, p); err != nil {
		t.Fatal(err)
	}
	got, err = s.Calendar(ctx, f.Owner, from, to)
	if err != nil || len(got) != 1 || got[0].Date != "2030-10-20" {
		t.Fatal("fallback date", got, err)
	}
}

func TestRemapAcrossProvidersMovesEpisodeRatingsAndReportsMissing(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	show := func(provider string, episodes int) (int64, []int64) {
		var root, season int64
		if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,total) VALUES($1,'show','series','Synthetic Show','episodes',$2) RETURNING id", provider, episodes).Scan(&root); err != nil {
			t.Fatal(err)
		}
		if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,children_loaded) VALUES($1,'show:1','season','Season 1','episodes',$2,1,true) RETURNING id", provider, root).Scan(&season); err != nil {
			t.Fatal(err)
		}
		ids := []int64{}
		for n := 1; n <= episodes; n++ {
			var id int64
			if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number) VALUES($1,$2,'episode',$3,'episodes',$4,1,$5) RETURNING id",
				provider, fmt.Sprintf("ep%d", n), fmt.Sprintf("Episode %d", n), season, n).Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return root, ids
	}
	oldRoot, oldEps := show("tmdb", 3)
	newRoot, newEps := show("tvdb", 2)
	if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "test", library.ImportItem{TitleID: oldRoot, Plays: []library.ImportPlay{{TargetID: oldEps[0], At: time.Now(), ExternalID: "a"}, {TargetID: oldEps[2], At: time.Now(), ExternalID: "c"}}}); err != nil {
		t.Fatal(err)
	}
	entry, err := s.EntryForCatalog(ctx, f.Owner, oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	seven, three := 7, 3
	if _, err = s.RateEpisode(ctx, f.Owner, entry.ID, oldEps[1], library.EpisodeRatingInput{Rating: &seven}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RateEpisode(ctx, f.Owner, entry.ID, oldEps[2], library.EpisodeRatingInput{Rating: &three}); err != nil {
		t.Fatal(err)
	}
	seasons, err := s.RemapSeasons(ctx, f.Owner, entry.ID)
	if err != nil || len(seasons) != 1 || seasons[0] != 1 {
		t.Fatal(seasons, err)
	}
	entry, _ = s.EntryForCatalog(ctx, f.Owner, oldRoot)
	preview, err := s.PreviewRemap(ctx, f.Owner, entry.ID, entry.Version, newRoot)
	if err != nil || preview.PlaysMoved != 1 || preview.PlaysRetained != 1 || preview.RatingsMoved != 1 || preview.RatingsDropped != 1 {
		t.Fatal("preview counts", preview, err)
	}
	if len(preview.Missing) != 1 || *preview.Missing[0].Episode != 3 {
		t.Fatal("missing episodes", preview.Missing)
	}
	if _, err = s.Remap(ctx, f.Owner, entry.ID, testkit.Key(t, f), preview.Version, newRoot); err != nil {
		t.Fatal(err)
	}
	var rated int64
	var score int
	if err = f.Pool.QueryRow(ctx, "SELECT catalog_item_id,rating FROM episode_ratings WHERE user_id=$1 AND entry_id=$2", f.Owner.User.ID, entry.ID).Scan(&rated, &score); err != nil || rated != newEps[1] || score != 7 {
		t.Fatal("episode rating did not follow its episode", rated, score, err)
	}
}

func TestVideoIsTrackedAndCompletedByAPlay(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var video int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('youtube','dQw4w9WgXcQ','video','Synthetic Video','minutes') RETURNING id").Scan(&video); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "test", library.ImportItem{TitleID: video, Plays: []library.ImportPlay{{TargetID: video, At: time.Now().Add(-time.Hour), ExternalID: "watch"}}}); err != nil {
		t.Fatal(err)
	}
	entry, err := s.EntryForCatalog(ctx, f.Owner, video)
	if err != nil || entry.MediaType != "video" || entry.Status != "completed" {
		t.Fatal(entry, err)
	}
	videos, err := s.Entries(ctx, f.Owner, library.Filter{MediaTypes: []string{"video"}})
	if err != nil || len(videos.Items) != 1 {
		t.Fatal(videos.Items, err)
	}
}
