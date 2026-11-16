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

// A title's history names each play by id and episode, so a client can delete one
// without matching plays by time; History can be narrowed to one title.
func TestPlayHistoryIdentifiesPlaysAndHistoryFiltersByEntry(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	show := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,runtime_minutes) VALUES(0,'series','tmdb','9001','Record Show','episodes',30) RETURNING id`)
	season := catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number) VALUES('tmdb','rs1','season','Season 1','episodes',$1,1) RETURNING id`, show)
	var eps []int64
	for n := 1; n <= 2; n++ {
		eps = append(eps, catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number,runtime_minutes) VALUES('tmdb',$1,'episode',$1,'episodes',$2,1,$3,30) RETURNING id`, "re"+strconv.Itoa(n), season, n))
	}
	film := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,runtime_minutes) VALUES(0,'movie','tmdb','9002','Record Film','minutes',90) RETURNING id`)
	showEntry, filmEntry := track(t, f, s, show), track(t, f, s, film)
	base := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
	playAt(t, f, showEntry, eps[0], base)
	playAt(t, f, showEntry, eps[1], base.Add(time.Hour))
	playAt(t, f, filmEntry, film, base.Add(2*time.Hour))

	h, err := s.PlayHistory(ctx, f.Owner, showEntry)
	if err != nil || len(h.Items) != 2 {
		t.Fatalf("history %+v %v", h, err)
	}
	newest := h.Items[0]
	if newest.PlayID == nil || newest.CatalogID != eps[1] || newest.SeasonNumber == nil || *newest.SeasonNumber != 1 || newest.EpisodeNumber == nil || *newest.EpisodeNumber != 2 {
		t.Fatalf("newest play %+v", newest)
	}
	var want int64
	if err = f.Pool.QueryRow(ctx, `SELECT id FROM plays WHERE profile_id=$1 AND catalog_item_id=$2`, f.Owner.TrackingID(), eps[1]).Scan(&want); err != nil || *newest.PlayID != want {
		t.Fatalf("play id %d, want %d (%v)", *newest.PlayID, want, err)
	}
	if fh, _ := s.PlayHistory(ctx, f.Owner, filmEntry); len(fh.Items) != 1 || fh.Items[0].CatalogID != film || fh.Items[0].SeasonNumber != nil {
		t.Fatalf("film history %+v", fh.Items)
	}

	page, err := s.History(ctx, f.Owner, library.HistoryFilter{EntryID: showEntry, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].EntryID != showEntry || page.NextCursor == nil {
		t.Fatalf("filtered history %+v %v", page, err)
	}
	next, err := s.History(ctx, f.Owner, library.HistoryFilter{EntryID: showEntry, Limit: 1, Cursor: *page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].CatalogID != eps[0] || next.NextCursor != nil {
		t.Fatalf("second page %+v %v", next, err)
	}
	// The cursor is bound to the entry filter.
	if _, err = s.History(ctx, f.Owner, library.HistoryFilter{EntryID: filmEntry, Limit: 1, Cursor: *page.NextCursor}); err == nil {
		t.Fatal("a cursor for one title paged another")
	}
	if all, _ := s.History(ctx, f.Owner, library.HistoryFilter{Limit: 10}); len(all.Items) != 3 {
		t.Fatalf("unfiltered history %d", len(all.Items))
	}
}

// The lists a title is on: manual lists holding it and smart lists that select it.
func TestEntryListsNamesManualAndSmartLists(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	other := f.User(t, "lists_other", "member")
	film := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'movie','tmdb','9101','Listed Film','minutes') RETURNING id`)
	entry := track(t, f, s, film)
	save := func(in library.ListInput) library.Mutation {
		t.Helper()
		m, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), in)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	shelf := save(library.ListInput{Name: "shelf"})
	if _, err := s.Membership(ctx, f.Owner, shelf.ID, entry, testkit.Key(t, f), shelf.Version, true); err != nil {
		t.Fatal(err)
	}
	save(library.ListInput{Name: "Empty"})
	save(library.ListInput{Name: "All films", Kind: "smart", Sort: "added", Rules: library.Rules{MediaTypes: []string{"movie"}}})
	save(library.ListInput{Name: "Books", Kind: "smart", Sort: "added", Rules: library.Rules{MediaTypes: []string{"book"}}})

	lists, err := s.EntryLists(ctx, f.Owner, entry)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, l := range lists {
		names = append(names, l.Name)
	}
	if len(names) != 2 || names[0] != "All films" || names[1] != "shelf" {
		t.Fatalf("lists %v", names)
	}
	if _, err = s.EntryLists(ctx, other, entry); !errors.Is(err, fault.NotFound) {
		t.Fatalf("another account read the lists: %v", err)
	}
}

// Activity carries the title's cover and the import run that wrote an event.
func TestActivityNamesCoverAndImportRun(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	film := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,image_url) VALUES(0,'movie','tmdb','9201','Imported Film','minutes','https://img.example/poster.jpg') RETURNING id`)
	other := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'movie','tmdb','9202','Tracked Film','minutes') RETURNING id`)
	var run string
	if err := f.Pool.QueryRow(ctx, "INSERT INTO import_runs(user_id,source,status) VALUES($1,'trakt','running') RETURNING id::text", f.Owner.User.ID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "trakt", library.ImportItem{RunID: run, TitleID: film, Status: "planned"}); err != nil {
		t.Fatal(err)
	}
	track(t, f, s, other)
	page, err := s.Activity(ctx, f.Owner, 0, "", 30)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("activity %+v %v", page, err)
	}
	tracked, imported := page.Items[0], page.Items[1]
	if imported.ImportID == nil || *imported.ImportID != run || imported.ImportSource == nil || *imported.ImportSource != "trakt" || imported.ImageURL != "https://img.example/poster.jpg" {
		t.Fatalf("imported event %+v", imported)
	}
	if tracked.ImportID != nil || tracked.ImportSource != nil || tracked.ImageURL != "" || tracked.LocalizedTitle != nil {
		t.Fatalf("tracked event %+v", tracked)
	}
}
