package library_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func id(v int64) string { return strconv.FormatInt(v, 10) }

func TestTagsBulkAndFilteredSortedBrowsing(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	var ids []string
	for i, title := range []string{"Cedar Atlas", "Birch Atlas", "Aspen Atlas"} {
		in := input(title)
		in.Rating = ptr(4 + i*3)
		m, err := s.Create(ctx, f.Owner, testkit.Key(t, f), in)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id(m.ID))
	}
	tag, err := s.SaveTag(ctx, f.Owner, 0, testkit.Key(t, f), library.TagInput{Name: "Favourites", Color: "#5F74EE"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveTag(ctx, f.Owner, 0, testkit.Key(t, f), library.TagInput{Name: "favourites"}); fault.Public(err).Code != "duplicate_tag" {
		t.Fatal("case-insensitive duplicate tag accepted", err)
	}
	res, err := s.Bulk(ctx, f.Owner, testkit.Key(t, f), library.BulkInput{EntryIDs: ids[:2], Action: "add_tags", TagIDs: []string{id(tag.ID)}})
	if err != nil || res.Changed != 2 {
		t.Fatalf("bulk tag %+v %v", res, err)
	}
	tagged, err := s.Entries(ctx, f.Owner, library.Filter{TagIDs: []int64{tag.ID}})
	if err != nil || len(tagged.Items) != 2 {
		t.Fatalf("tag filter %d %v", len(tagged.Items), err)
	}
	// Title order pages with a keyset cursor.
	first, err := s.Entries(ctx, f.Owner, library.Filter{Sort: "title", Limit: 2})
	if err != nil || len(first.Items) != 2 || first.Items[0].Title != "Aspen Atlas" || first.NextCursor == nil {
		t.Fatalf("title page %+v %v", first.Items, err)
	}
	second, err := s.Entries(ctx, f.Owner, library.Filter{Sort: "title", Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].Title != "Cedar Atlas" {
		t.Fatalf("title page 2 %+v %v", second.Items, err)
	}
	if _, err = s.Entries(ctx, f.Owner, library.Filter{Sort: "rating", Cursor: *first.NextCursor}); err == nil {
		t.Fatal("cursor reused across sort orders")
	}
	rated, err := s.Entries(ctx, f.Owner, library.Filter{Sort: "rating", MinRating: ptr(7)})
	if err != nil || len(rated.Items) != 2 || *rated.Items[0].Rating != 10 {
		t.Fatalf("rating filter %+v %v", rated.Items, err)
	}
	// Bulk status is atomic: one foreign id fails the batch.
	member := f.User(t, "bulk_member", "member")
	foreign := create(t, f, s, member, "Not yours")
	if _, err = s.Bulk(ctx, f.Owner, testkit.Key(t, f), library.BulkInput{EntryIDs: []string{ids[0], id(foreign.ID)}, Action: "set_status", Status: "completed"}); !errors.Is(err, fault.NotFound) {
		t.Fatal("bulk touched another account", err)
	}
	res, err = s.Bulk(ctx, f.Owner, testkit.Key(t, f), library.BulkInput{EntryIDs: ids, Action: "set_status", Status: "completed"})
	if err != nil || res.Changed != 3 {
		t.Fatalf("bulk status %+v %v", res, err)
	}
	e, _ := s.Get(ctx, f.Owner, first.Items[0].ID)
	if e.Status != "completed" || e.Progress != 240 || e.CompletedCycles != 1 {
		t.Fatalf("bulk completion %+v", e.Summary)
	}
	if _, err = s.DeleteTag(ctx, f.Owner, tag.ID, testkit.Key(t, f)); err != nil {
		t.Fatal(err)
	}
	if tags, _ := s.EntryTags(ctx, f.Owner, first.Items[0].ID); len(tags) != 0 {
		t.Fatal("deleted tag still attached")
	}
}

func TestOwnedCopiesFieldsAndPreferences(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	e := create(t, f, s, f.Owner, "Collected Atlas")
	signed, err := s.SaveField(ctx, f.Owner, 0, testkit.Key(t, f), library.FieldInput{Name: "Signed", Kind: "boolean"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.SaveCopy(ctx, f.Owner, e.ID, 0, testkit.Key(t, f), library.CopyInput{Format: "hardcover", Edition: "First", Currency: "eur", PriceCents: ptr(2500), AcquiredOn: ptr("2026-01-02"), Custom: map[string]any{id(signed.ID): true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveCopy(ctx, f.Owner, e.ID, c.ID, testkit.Key(t, f), library.CopyInput{Format: "paperback", Version: c.Version + 1}); !errors.Is(err, fault.Conflict) {
		t.Fatal("stale copy version accepted", err)
	}
	if _, err = s.SaveCopy(ctx, f.Owner, e.ID, 0, testkit.Key(t, f), library.CopyInput{Format: "laserdisc"}); err == nil {
		t.Fatal("unknown format accepted")
	}
	copies, err := s.Copies(ctx, f.Owner, 0, "")
	if err != nil || len(copies) != 1 || copies[0].Currency != "EUR" || *copies[0].AcquiredOn != "2026-01-02" || copies[0].Custom[id(signed.ID)] != true || copies[0].EntryTitle != "Collected Atlas" {
		t.Fatalf("collection %+v %v", copies, err)
	}
	owned, err := s.Entries(ctx, f.Owner, library.Filter{Owned: ptr(true)})
	if err != nil || len(owned.Items) != 1 {
		t.Fatalf("owned filter %v", err)
	}
	member := f.User(t, "copy_member", "member")
	if copies, _ = s.Copies(ctx, member, 0, ""); len(copies) != 0 {
		t.Fatal("collection leaked across accounts")
	}
	if _, err = s.SaveCopy(ctx, member, e.ID, 0, testkit.Key(t, f), library.CopyInput{Format: "dvd"}); !errors.Is(err, fault.NotFound) {
		t.Fatal("copy added to another account's entry", err)
	}
	if _, err = s.SaveField(ctx, f.Owner, 0, testkit.Key(t, f), library.FieldInput{Name: "Shelf", Kind: "choice", Choices: []string{"B", "A", "A"}}); err != nil {
		t.Fatal(err)
	}
	if fields, _ := s.Fields(ctx, f.Owner); len(fields) != 2 || len(fields[1].Choices) != 2 {
		t.Fatalf("fields %+v", fields)
	}
	p, err := s.Preferences(ctx, f.Owner)
	if err != nil || p.Version != 0 || len(p.HomeRows) != 5 {
		t.Fatalf("default preferences %+v %v", p, err)
	}
	p.RatingScale, p.PublicProfile = "five", true
	p.HomeRows = append(p.HomeRows[:2], library.HomeRow{Kind: "list", ListID: "1", Visible: true})
	saved, err := s.SavePreferences(ctx, f.Owner, p)
	if err != nil || saved.Version != 1 {
		t.Fatalf("save preferences %+v %v", saved, err)
	}
	if _, err = s.SavePreferences(ctx, f.Owner, p); !errors.Is(err, fault.Conflict) {
		t.Fatal("stale preferences accepted", err)
	}
	if got, _ := s.Preferences(ctx, f.Owner); got.RatingScale != "five" || !got.PublicProfile || len(got.HomeRows) != 3 {
		t.Fatalf("read back %+v", got)
	}
	p.Version, p.RatingScale = 1, "stars"
	if _, err = s.SavePreferences(ctx, f.Owner, p); err == nil {
		t.Fatal("unknown rating scale accepted")
	}
}

func TestSmartOrderedAndPublicLists(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	a := create(t, f, s, f.Owner, "First Shelf Title")
	b := create(t, f, s, f.Owner, "Second Shelf Title")
	movie := input("Screen Title")
	movie.MediaType, movie.Unit, movie.Total = "movie", "minutes", ptr(100)
	if _, err := s.Create(ctx, f.Owner, testkit.Key(t, f), movie); err != nil {
		t.Fatal(err)
	}
	list, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Shelf", Visibility: "public", Slug: ptr("my-shelf")})
	if err != nil {
		t.Fatal(err)
	}
	v := list.Version
	for _, e := range []library.Mutation{a, b} {
		m, err := s.Membership(ctx, f.Owner, list.ID, e.ID, testkit.Key(t, f), v, true)
		if err != nil {
			t.Fatal(err)
		}
		v = m.Version
	}
	m, err := s.Reorder(ctx, f.Owner, list.ID, testkit.Key(t, f), v, []string{id(b.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetListNote(ctx, f.Owner, list.ID, b.ID, testkit.Key(t, f), m.Version, "Start here"); err != nil {
		t.Fatal(err)
	}
	ordered, err := s.Entries(ctx, f.Owner, library.Filter{ListID: list.ID})
	if err != nil || len(ordered.Items) != 2 || ordered.Items[0].ID != b.ID {
		t.Fatalf("manual order %+v %v", ordered.Items, err)
	}
	public, err := s.Public(ctx, f.Owner.User.Username, "my-shelf")
	if err != nil || len(public.Items) != 2 || public.Items[0].Note != "Start here" || public.Items[0].Title != "Second Shelf Title" {
		t.Fatalf("public list %+v %v", public, err)
	}
	if _, err = s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Clash", Visibility: "unlisted", Slug: ptr("my-shelf")}); fault.Public(err).Code != "duplicate_slug" {
		t.Fatal("duplicate slug accepted", err)
	}
	if _, err = s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "No link", Visibility: "public"}); err == nil {
		t.Fatal("shared list without a slug accepted")
	}
	smart, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Films", Kind: "smart", Rules: library.Rules{MediaTypes: []string{"movie"}}})
	if err != nil {
		t.Fatal(err)
	}
	films, err := s.Entries(ctx, f.Owner, library.Filter{ListID: smart.ID})
	if err != nil || len(films.Items) != 1 || films.Items[0].Title != "Screen Title" {
		t.Fatalf("smart list %+v %v", films.Items, err)
	}
	if _, err = s.Membership(ctx, f.Owner, smart.ID, a.ID, testkit.Key(t, f), smart.Version, true); err == nil {
		t.Fatal("manual member added to a smart list")
	}
	// Private lists and other people's slugs are invisible.
	private, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Hidden", Slug: ptr("hidden")})
	if err != nil {
		t.Fatal(err)
	}
	_ = private
	if _, err = s.Public(ctx, f.Owner.User.Username, "hidden"); !errors.Is(err, fault.NotFound) {
		t.Fatal("private list readable", err)
	}
	if _, _, err = s.PublicLists(ctx, f.Owner.User.Username); !errors.Is(err, fault.NotFound) {
		t.Fatal("profile readable before opting in", err)
	}
}
