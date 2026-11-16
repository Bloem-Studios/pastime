package catalog_test

import (
	"context"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Search results say "in your library" for the profile in use only, so a
// profile person can't learn what the household's other profiles track.
func TestMembershipUsesActiveProfile(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	item, err := s.Resolve(ctx, "faketv", "movie", "100")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (library.Store{Pool: f.Pool}).Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: item.ID, Status: "planned"}); err != nil {
		t.Fatal(err)
	}
	_, sam, _ := f.ProfileLogin(t, "Sam", "sam")
	hits, err := s.Search(ctx, "movie", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Membership(ctx, sam, hits.Items); err != nil {
		t.Fatal(err)
	}
	if len(hits.Items) != 1 || hits.Items[0].LibraryEntryID != nil || hits.Items[0].LibraryStatus != nil {
		t.Fatalf("a sibling profile's library leaked into search: %+v", hits.Items)
	}
	if err = s.Membership(ctx, f.Owner, hits.Items); err != nil {
		t.Fatal(err)
	}
	if hits.Items[0].LibraryEntryID == nil {
		t.Fatal("the default profile lost its own membership")
	}
}
