package library_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestLastPlayedNextItemAndUpNextOrder(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	show := func(name string, episodes int) (int64, []int64) {
		root := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,runtime_minutes) VALUES(0,'series','tmdb',$1,$1,'episodes',43) RETURNING id`, name)
		season := catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number) VALUES('tmdb',$1,'season','Season 1','episodes',$2,1) RETURNING id`, name+"-s1", root)
		var eps []int64
		for n := 1; n <= episodes; n++ {
			code := name + "-e" + strconv.Itoa(n)
			eps = append(eps, catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number,runtime_minutes,release_date) VALUES('tmdb',$1,'episode',$1,'episodes',$2,1,$3,43,'2020-01-01') RETURNING id`, code, season, n))
		}
		return root, eps
	}
	play := func(entryID int64, at time.Time, items ...int64) {
		t.Helper()
		e, err := s.Get(ctx, f.Owner, entryID)
		if err != nil {
			t.Fatal(err)
		}
		in := library.PlayInput{Version: e.Version}
		for _, id := range items {
			in.Items = append(in.Items, library.PlayItem{CatalogID: id, PlayedAt: &at})
		}
		if _, err = s.AddPlays(ctx, f.Owner, entryID, testkit.Key(t, f), in); err != nil {
			t.Fatal(err)
		}
	}
	marked, markedEps := show("marked-show", 6)
	watched, watchedEps := show("watched-show", 3)
	a, b := track(t, f, s, marked), track(t, f, s, watched)
	today := day(time.Now().UTC())
	older, recent := today.AddDate(0, 0, -5).Add(20*time.Hour), today.AddDate(0, 0, -2).Add(21*time.Hour)
	play(b, recent, watchedEps[0])
	play(a, older, markedEps[3])
	// The marked show's newest activity is a bulk mark, which is not a viewing.
	play(a, today.AddDate(0, 0, -1).Add(19*time.Hour), markedEps[0], markedEps[1], markedEps[2])

	entry, err := s.Get(ctx, f.Owner, a)
	if err != nil {
		t.Fatal(err)
	}
	if entry.LastPlayedAt == nil || !entry.LastPlayedAt.Equal(older) {
		t.Fatalf("last played %v, want the real play %v", entry.LastPlayedAt, older)
	}
	next := entry.NextItem
	if next == nil || next.CatalogID != markedEps[4] || next.SeasonNumber == nil || *next.SeasonNumber != 1 || next.EpisodeNumber == nil || *next.EpisodeNumber != 5 ||
		next.Title != "marked-show-e5" || next.ReleaseDate == nil || *next.ReleaseDate != "2020-01-01" || next.RuntimeMinutes == nil || *next.RuntimeMinutes != 43 {
		t.Fatalf("next item %+v", next)
	}
	home, err := s.Home(ctx, f.Owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(home.UpNext) != 2 || home.UpNext[0].Entry.ID != b || home.UpNext[1].Entry.ID != a || home.UpNext[1].Episode.CatalogID != markedEps[4] {
		t.Fatalf("up next is not ordered by the latest real play: %+v", home.UpNext)
	}

	// A finished movie dates by its completion play; a title never played comes last.
	movie := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','tmdb','m1','Finished film','minutes',100) RETURNING id`)
	m, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: movie, Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	film, _ := s.Get(ctx, f.Owner, m.ID)
	if film.NextItem != nil || film.LastPlayedAt == nil {
		t.Fatalf("film next %+v last played %v", film.NextItem, film.LastPlayedAt)
	}
	never := create(t, f, s, f.Owner, "Unplayed book")
	want := []int64{m.ID, b, a, never.ID}
	var got []int64
	cursor := ""
	for range want {
		page, err := s.Entries(ctx, f.Owner, library.Filter{Sort: "played", Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Items {
			got = append(got, e.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("played order %v, want %v", got, want)
	}
	// A saved view of the library keeps the played order; a list keeps its own fixed orders.
	if v, err := s.SaveView(ctx, f.Owner, 0, library.SavedViewInput{Name: "Recently played", Sort: "played", Layout: "list"}); err != nil || v.Sort != "played" {
		t.Fatal("a saved view refused the played order", v, err)
	}
	if _, err = s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Played order", Sort: "played"}); err == nil || fault.Public(err).Status != 422 {
		t.Fatal("a list stored the played order", err)
	}
}

func TestLibraryFacetsCountUnderTheOtherFilters(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	add := func(title, mediaType, unit, status string) int64 {
		m, err := s.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: title, MediaType: mediaType, Unit: unit, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	add("Atlas one", "book", "pages", "planned")
	second := add("Atlas two", "book", "pages", "planned")
	add("Atlas three", "book", "pages", "completed")
	film := add("Harbor film", "movie", "minutes", "completed")
	add("Harbor sequel", "movie", "minutes", "in_progress")
	add("Harbor game", "game", "items", "planned")
	check := func(name string, filter library.Filter, total int, types, statuses string) {
		t.Helper()
		out, err := s.Facets(ctx, f.Owner, filter)
		if err != nil {
			t.Fatal(name, err)
		}
		gotTypes, gotStatuses := "", ""
		for _, c := range out.MediaTypes {
			gotTypes += c.MediaType + "=" + strconv.Itoa(c.Count) + " "
		}
		for _, c := range out.Statuses {
			gotStatuses += c.Status + "=" + strconv.Itoa(c.Count) + " "
		}
		if out.Total != total || gotTypes != types || gotStatuses != statuses {
			t.Fatalf("%s: total %d types %q statuses %q", name, out.Total, gotTypes, gotStatuses)
		}
	}
	check("whole library", library.Filter{}, 6, "movie=2 book=3 game=1 ", "planned=3 in_progress=1 completed=2 ")
	check("books", library.Filter{MediaTypes: []string{"book"}}, 3, "movie=2 book=3 game=1 ", "planned=2 completed=1 ")
	check("finished books", library.Filter{MediaTypes: []string{"book"}, Statuses: []string{"completed"}}, 1, "movie=1 book=1 ", "planned=2 completed=1 ")
	check("a search", library.Filter{Query: "Harbor"}, 3, "movie=2 game=1 ", "planned=1 in_progress=1 completed=1 ")
	list, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Synthetic picks"})
	if err != nil {
		t.Fatal(err)
	}
	version := list.Version
	for _, id := range []int64{second, film} {
		m, err := s.Membership(ctx, f.Owner, list.ID, id, testkit.Key(t, f), version, true)
		if err != nil {
			t.Fatal(err)
		}
		version = m.Version
	}
	check("a manual list", library.Filter{ListID: list.ID}, 2, "movie=1 book=1 ", "planned=1 completed=1 ")
	other := f.User(t, "facets_other", "member")
	if out, err := s.Facets(ctx, other, library.Filter{}); err != nil || out.Total != 0 || len(out.MediaTypes) != 0 || len(out.Statuses) != 0 {
		t.Fatalf("another account's titles counted: %+v %v", out, err)
	}
	if _, err := s.Facets(ctx, other, library.Filter{ListID: list.ID}); !errors.Is(err, fault.NotFound) {
		t.Fatal("another account's list counted", err)
	}
	if _, err := s.Facets(ctx, f.Owner, library.Filter{MediaTypes: []string{"vinyl"}}); err == nil {
		t.Fatal("an unknown media type accepted")
	}
}
