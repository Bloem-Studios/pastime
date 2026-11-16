package discover

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"strconv"
	"testing"
)

type shortProvider struct{}

func (shortProvider) Name() string    { return "synthetic" }
func (shortProvider) Kinds() []string { return []string{"movie"} }
func (shortProvider) Search(context.Context, string, string) ([]provider.Record, error) {
	return nil, nil
}
func (shortProvider) Fetch(context.Context, string, string) (provider.Record, error) {
	return provider.Record{}, provider.ErrNotFound
}
func (shortProvider) Trending(_ context.Context, kind string) ([]provider.Record, error) {
	out := []provider.Record{}
	for i := 1; i <= 3; i++ {
		out = append(out, provider.Record{Provider: "synthetic", Kind: kind, ProviderID: strconv.Itoa(i), Title: "Synthetic " + strconv.Itoa(i)})
	}
	return out, nil
}
func TestUsabilityRetainsShortShelf(t *testing.T) {
	f := testkit.New(t)
	s := Store{Pool: f.Pool, Catalog: &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"synthetic": shortProvider{}}, Defaults: map[string]string{"movie": "synthetic"}}}
	out, err := s.Feed(context.Background(), f.Owner, "movie")
	if err != nil || len(out.Rows) != 1 || len(out.Rows[0].Items) != 3 {
		t.Fatalf("useful three-title shelf hidden: %+v %v", out, err)
	}
}
func TestUsabilityDedupOnlyConsumesVisibleItems(t *testing.T) {
	f := testkit.New(t)
	s := Store{Pool: f.Pool}
	first := Row{Key: "first"}
	for i := 1; i <= 21; i++ {
		first.Items = append(first.Items, catalog.Result{Provider: "synthetic", MediaType: "movie", ProviderID: strconv.Itoa(i), Title: "Synthetic"})
	}
	rows := []Row{first, {Key: "second", Items: []catalog.Result{first.Items[20]}}}
	if err := s.filter(context.Background(), f.Owner.User.ID, rows); err != nil {
		t.Fatal(err)
	}
	if len(rows[0].Items) != 20 || len(rows[1].Items) != 1 {
		t.Fatalf("non-displayed item consumed later suggestion: %d, %d", len(rows[0].Items), len(rows[1].Items))
	}
}

func TestUsabilityDismissalReviewIsPersistentAndPrivate(t *testing.T) {
	f := testkit.New(t)
	s := Store{Pool: f.Pool}
	ctx := context.Background()
	other := f.User(t, "dismissal_member", "member")
	for _, id := range []string{"1", "2", "3"} {
		if err := s.Dismiss(ctx, f.Owner, "synthetic", "movie", id, false); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Dismissals(ctx, f.Owner, "movie", "", 2)
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page %+v %v", first, err)
	}
	last, err := s.Dismissals(ctx, f.Owner, "movie", *first.NextCursor, 2)
	if err != nil || len(last.Items) != 1 || last.NextCursor != nil {
		t.Fatalf("last page %+v %v", last, err)
	}
	if hidden, err := s.Dismissals(ctx, other, "movie", "", 2); err != nil || len(hidden.Items) != 0 {
		t.Fatalf("private dismissals leaked: %+v %v", hidden, err)
	}
	if _, err := s.Dismissals(ctx, other, "movie", *first.NextCursor, 2); err == nil {
		t.Fatal("another user's cursor accepted")
	}
	if err := s.Dismiss(ctx, f.Owner, "synthetic", "movie", "1", true); err != nil {
		t.Fatal(err)
	}
	after, err := s.Dismissals(ctx, f.Owner, "movie", "", 2)
	if err != nil || len(after.Items) != 2 || after.Items[0].ProviderID != "2" {
		t.Fatalf("undo not persistent: %+v %v", after, err)
	}
}

func TestUsabilityColdStartUsesHonestLocalMetadata(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	_, err := f.Pool.Exec(ctx, `INSERT INTO catalog_items(provider,kind,provider_id,title,image_url,genres,credits,external_ids,details,unit) VALUES('synthetic','book','1','Synthetic local atlas','','{}','{}','{}','{}','pages')`)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{Pool: f.Pool, Catalog: &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"openlibrary": provider.OpenLibrary{}}, Defaults: map[string]string{"book": "openlibrary"}}}
	out, err := s.Feed(ctx, f.Owner, "book")
	if err != nil || out.Availability != "search_only" || len(out.Rows) != 1 || out.Rows[0].Reason.Code != "catalog" || out.Rows[0].Reason.Source != "local" || len(out.Rows[0].Items) != 1 {
		t.Fatalf("cold-start source misrepresented: %+v %v", out, err)
	}
	if out.Rows[0].Items[0].LibraryEntryID != nil {
		t.Fatal("another caller's private tracking state exposed")
	}
}
