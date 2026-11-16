package library_test

import (
	"context"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestSavedViewsOwnershipAndVersion(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	v, err := s.SaveView(ctx, f.Owner, 0, library.SavedViewInput{Name: "Books to read", Rules: library.Rules{MediaTypes: []string{"book"}, Statuses: []string{"planned"}}, Sort: "title", Layout: "list"})
	if err != nil {
		t.Fatal(err)
	}
	other := f.User(t, "view_other", "member")
	if _, err = s.SaveView(ctx, other, v.ID, library.SavedViewInput{Name: "Stolen", Sort: "added", Layout: "grid", Version: v.Version}); err == nil {
		t.Fatal("cross-account write")
	}
	if err = s.DeleteView(ctx, other, v.ID, v.Version); err == nil {
		t.Fatal("cross-account delete")
	}
	list, err := s.SavedViews(ctx, other)
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if _, err = s.SaveView(ctx, f.Owner, v.ID, library.SavedViewInput{Name: "New", Sort: "added", Layout: "grid", Version: v.Version + 1}); err == nil {
		t.Fatal("stale version accepted")
	}
	list, err = s.SavedViews(ctx, f.Owner)
	if err != nil || len(list) != 1 || list[0].Rules.MediaTypes[0] != "book" {
		t.Fatal(list, err)
	}
	if _, err = s.SaveView(ctx, f.Owner, 0, library.SavedViewInput{Name: "Invalid", Sort: "position", Layout: "grid"}); err == nil {
		t.Fatal("list-only sort accepted")
	}
}

func TestSavedViewReorderRejectsDuplicateIdentities(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var ids []string
	for _, name := range []string{"One", "Two"} {
		v, err := s.SaveView(ctx, f.Owner, 0, library.SavedViewInput{Name: name, Sort: "added", Layout: "grid"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, fmt.Sprint(v.ID))
	}
	if err := s.ReorderViews(ctx, f.Owner, []string{ids[0], "0" + ids[0]}); err == nil {
		t.Fatal("duplicate numeric identity accepted")
	}
	if err := s.ReorderViews(ctx, f.Owner, []string{ids[1], ids[0]}); err != nil {
		t.Fatal(err)
	}
	views, err := s.SavedViews(ctx, f.Owner)
	if err != nil || views[0].Name != "Two" {
		t.Fatal(views, err)
	}
}
