package catalog_test

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"sync"
	"testing"
	"time"
)

func TestUsabilityProviderInventoryIncludesMissingAndNewAdapters(t *testing.T) {
	ps, defaults, _ := catalog.Providers(catalog.Credentials{})
	s := &catalog.Service{Providers: ps, Defaults: defaults}
	raw, _ := json.Marshal(s.Choices())
	var choices []map[string]any
	_ = json.Unmarshal(raw, &choices)
	byName := map[string]map[string]any{}
	for _, c := range choices {
		byName[c["name"].(string)] = c
	}
	for _, name := range []string{"tmdb", "tvdb", "igdb", "bgg", "comicvine", "mangabaka", "gcd", "youtube", "rss"} {
		if byName[name] == nil {
			t.Errorf("missing provider %s", name)
		}
	}
	for _, name := range []string{"igdb", "bgg", "tmdb", "tvdb"} {
		if c := byName[name]; c != nil && c["availability"] != "not_configured" {
			t.Errorf("%s lacks honest unconfigured availability: %v", name, c)
		}
	}
	if c := byName["anilist"]; c == nil || c["browse"] == nil {
		t.Errorf("AniList browse capability missing: %v", c)
	}
}

func TestUsabilityMembershipRemainsCallerPrivate(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	item, err := s.Resolve(ctx, "faketv", "movie", "100")
	if err != nil {
		t.Fatal(err)
	}
	other := f.User(t, "synthetic_member", "member")
	entry, err := (library.Store{Pool: f.Pool}).Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: item.ID, Status: "planned"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, "movie", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Membership(ctx, f.Owner, hits.Items); err != nil {
		t.Fatal(err)
	}
	if len(hits.Items) != 1 || hits.Items[0].LibraryEntryID == nil || *hits.Items[0].LibraryEntryID != entry.ID || hits.Items[0].LibraryStatus == nil || *hits.Items[0].LibraryStatus != "planned" {
		t.Fatalf("owner membership absent: %+v", hits.Items)
	}
	if err = s.Membership(ctx, other, hits.Items); err != nil {
		t.Fatal(err)
	}
	if hits.Items[0].LibraryEntryID != nil || hits.Items[0].LibraryStatus != nil || hits.Items[0].CatalogID == nil {
		t.Fatalf("another caller's membership leaked: %+v", hits.Items)
	}
	fresh, err := s.Search(ctx, "movie", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Items[0].LibraryEntryID != nil {
		t.Fatal("private membership cached as shared metadata")
	}
}

// A token bound to one household profile sees that profile's entry only: never one
// another profile of the same account holds, and never in place of its own.
func TestUsabilityMembershipStaysWithBoundProfile(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	item, err := s.Resolve(ctx, "faketv", "movie", "100")
	if err != nil {
		t.Fatal(err)
	}
	lib := library.Store{Pool: f.Pool}
	if _, err = lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: item.ID, Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := f.Accounts.MintToken(ctx, selected, identity.TokenInput{Name: "Child reader", Scopes: []string{"library:read"}, ExpiresInDays: 30, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.Accounts.Authenticate(ctx, raw, "token")
	if err != nil || token.TrackingID() != child.ID {
		t.Fatal("child token", token.TrackingID(), err)
	}
	hits, err := s.Search(ctx, "movie", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Membership(ctx, token, hits.Items); err != nil {
		t.Fatal(err)
	}
	if len(hits.Items) != 1 || hits.Items[0].LibraryEntryID != nil || hits.Items[0].LibraryStatus != nil {
		t.Fatalf("another household profile's entry leaked: %+v", hits.Items)
	}
	own, err := lib.Track(ctx, selected, testkit.Key(t, f), library.TrackInput{CatalogID: item.ID, Status: "planned"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = s.Membership(ctx, token, hits.Items); err != nil {
			t.Fatal(err)
		}
		if hits.Items[0].LibraryEntryID == nil || *hits.Items[0].LibraryEntryID != own.ID || *hits.Items[0].LibraryStatus != "planned" {
			t.Fatalf("bound profile's own entry replaced: %+v", hits.Items[0])
		}
	}
}

func TestUsabilityPagedCacheSeparatesOptionsAndPrivateMembership(t *testing.T) {
	f, s, tv := setup(t)
	ctx := context.Background()
	q := &cachedUsabilityProvider{fakeTV: tv}
	s.Providers["faketv"] = q
	s.Cache = &usabilityCache{items: map[string][]byte{}}
	item, err := s.Resolve(ctx, "faketv", "movie", "100")
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := (library.Store{Pool: f.Pool}).Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.Query(ctx, "movie", "", provider.QueryOptions{Query: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Membership(ctx, f.Owner, owner.Items); err != nil {
		t.Fatal(err)
	}
	if owner.Items[0].LibraryEntryID == nil || *owner.Items[0].LibraryEntryID != tracked.ID {
		t.Fatal("owner membership missing")
	}
	member := f.User(t, "cached_member", "member")
	other, err := s.Query(ctx, "movie", "", provider.QueryOptions{Query: "Synthetic", Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Membership(ctx, member, other.Items); err != nil {
		t.Fatal(err)
	}
	if q.calls != 1 || other.Items[0].LibraryEntryID != nil || other.Items[0].LibraryStatus != nil || other.Items[0].CatalogID == nil {
		t.Fatalf("shared cache lost privacy/caching: calls=%d %+v", q.calls, other)
	}
	second, err := s.Query(ctx, "movie", "", provider.QueryOptions{Query: "Synthetic", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if q.calls != 2 || second.Items[0].ProviderID != "101" || second.NextPage != nil {
		t.Fatalf("page options not separated: calls=%d %+v", q.calls, second)
	}
}

type cachedUsabilityProvider struct {
	*fakeTV
	calls int
}

func (*cachedUsabilityProvider) QueryCapabilities() (provider.QueryCapabilities, provider.QueryCapabilities) {
	return provider.QueryCapabilities{Supported: true, Pagination: true, Sorts: []string{}}, provider.QueryCapabilities{Sorts: []string{}}
}
func (q *cachedUsabilityProvider) Query(_ context.Context, kind string, o provider.QueryOptions) (provider.RecordPage, error) {
	q.calls++
	id := "100"
	next := 2
	if o.Page == 2 {
		id = "101"
		next = 0
	}
	return provider.RecordPage{Items: []provider.Record{{Provider: "faketv", Kind: kind, ProviderID: id, Title: "Synthetic film"}}, NextPage: next}, nil
}

type usabilityCache struct {
	mu    sync.Mutex
	items map[string][]byte
}

func (c *usabilityCache) Get(_ context.Context, key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	return append([]byte(nil), v...), ok
}
func (c *usabilityCache) Set(_ context.Context, key string, value []byte, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = append([]byte(nil), value...)
}
