package library_test

import (
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Favourite and watchlist titles reach the native calendar with or without a
// following library entry, once per release, and only for the profile that chose them.
func TestCalendarIncludesFavouriteAndWatchlistTitles(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := t.Context()
	member := f.User(t, "calendar_member", "member")
	day := time.Date(2031, 3, 10, 0, 0, 0, 0, time.UTC)
	title := func(kind, providerID, name string, release *time.Time) int64 {
		t.Helper()
		var id int64
		if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,release_date,image_url) VALUES('manual',$1,$2,$3,'minutes',$4,'https://example.test/poster.jpg') RETURNING id`, providerID, kind, name, release).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	choose := func(a library.PersonalMembershipInput, catalogID int64) {
		t.Helper()
		if err := s.SetMembership(ctx, f.Owner, testkit.Key(t, f), catalogID, a); err != nil {
			t.Fatal(err)
		}
	}
	favouriteOnly := title("movie", "favourite-only", "Favourite only", &day)
	completed := title("movie", "completed", "Completed favourite", &day)
	planned := title("movie", "planned", "Planned favourite", &day)
	removed := title("movie", "removed", "Removed favourite", &day)
	unrelated := title("movie", "unrelated", "Not chosen", &day)
	series := title("series", "series", "Watchlist series", nil)
	if _, err := f.Pool.Exec(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number,release_date) VALUES('manual','series:1:1','episode','New episode','minutes',$1,1,1,$2)`, series, day); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{favouriteOnly, completed, planned, removed} {
		choose(library.PersonalMembershipInput{Kind: "favorite", Present: true}, id)
	}
	choose(library.PersonalMembershipInput{Kind: "favorite", Present: false}, removed)
	choose(library.PersonalMembershipInput{Kind: "watchlist", Present: true}, series)
	if _, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: completed, Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: planned}); err != nil {
		t.Fatal(err)
	}
	// Another profile's choice never reaches this calendar, nor this one's reaches it.
	if err := s.SetMembership(ctx, member, testkit.Key(t, f), unrelated, library.PersonalMembershipInput{Kind: "favorite", Present: true}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Calendar(ctx, f.Owner, day, day)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]library.Release{}
	for _, r := range got {
		if _, dup := byTitle[r.EntryTitle]; dup {
			t.Fatalf("release listed twice: %+v", got)
		}
		byTitle[r.EntryTitle] = r
	}
	if len(got) != 4 {
		t.Fatalf("want 4 releases, got %+v", got)
	}
	if r := byTitle["Favourite only"]; r.EntryID != 0 || r.CatalogID != favouriteOnly || r.ParentCatalog != favouriteOnly || r.MediaType != "movie" || r.ImageURL == "" || r.Following || !r.Favourite || r.Watchlist {
		t.Fatalf("favourite without an entry: %+v", r)
	}
	if r := byTitle["Completed favourite"]; r.EntryID == 0 || r.Following || !r.Favourite {
		t.Fatalf("favourite with a finished entry: %+v", r)
	}
	if r := byTitle["Planned favourite"]; r.EntryID == 0 || !r.Following || !r.Favourite {
		t.Fatalf("favourite that is also followed: %+v", r)
	}
	// Joining a watchlist plans the title, so it is followed as well.
	if r := byTitle["Watchlist series"]; r.Kind != "episode" || r.ParentCatalog != series || !r.Watchlist || !r.Following || r.Favourite {
		t.Fatalf("watchlist episode: %+v", r)
	}
	if _, ok := byTitle["Removed favourite"]; ok {
		t.Fatal("removed favourite still on the calendar")
	}
	if other, err := s.Calendar(ctx, member, day, day); err != nil || len(other) != 1 || other[0].CatalogID != unrelated {
		t.Fatalf("profile isolation: %+v %v", other, err)
	}

	// Regional dates apply to favourite-only movies too.
	if _, err = f.Pool.Exec(ctx, `UPDATE catalog_items SET details='{"release_dates":{"NL":{"digital":"2031-04-02"}}}' WHERE id=$1`, favouriteOnly); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Preferences(ctx, f.Owner)
	p.ReleaseRegion, p.CalendarReleaseTypes = "nl", []string{"digital"}
	if _, err = s.SavePreferences(ctx, f.Owner, p); err != nil {
		t.Fatal(err)
	}
	got, err = s.Calendar(ctx, f.Owner, day, day.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	regional := 0
	for _, r := range got {
		if r.CatalogID == favouriteOnly {
			if r.Date != "2031-04-02" || r.ReleaseType != "digital" || !r.Favourite || r.EntryID != 0 {
				t.Fatalf("regional favourite release: %+v", r)
			}
			regional++
		}
	}
	if regional != 1 {
		t.Fatalf("regional favourite missing or primary date kept: %+v", got)
	}

	// Home's upcoming row uses the same selection.
	home, err := s.Home(ctx, f.Owner, day)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range home.Upcoming {
		found = found || r.EntryTitle == "Completed favourite"
	}
	if !found {
		t.Fatalf("favourite missing from home upcoming: %+v", home.Upcoming)
	}
}
