package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// discoverTMDB: movie 42 lists movies 50-54 as related; trending is 42 and 60-64.
type discoverTMDB struct{ syntheticTMDB }

func (discoverTMDB) Fetch(_ context.Context, kind, id string) (provider.Record, error) {
	related := []map[string]any{}
	for i := 50; i < 55; i++ {
		related = append(related, map[string]any{"provider": "tmdb", "kind": "movie", "id": strconv.Itoa(i), "title": "Related " + strconv.Itoa(i), "image_url": "https://images.example/r.jpg", "year": 2020})
	}
	return provider.Record{Provider: "tmdb", Kind: kind, ProviderID: id, Title: "Synthetic Film", Genres: []string{"Drama"}, Total: iptr(100), RuntimeMinutes: iptr(100),
		Details: map[string]any{"related": related, "people": []map[string]any{{"id": "7", "name": "Sam Director", "role": "Director"}}}}, nil
}
func (discoverTMDB) Trending(_ context.Context, kind string) ([]provider.Record, error) {
	out := []provider.Record{{Provider: "tmdb", Kind: kind, ProviderID: "42", Title: "Synthetic Film"}}
	for i := 60; i < 65; i++ {
		out = append(out, provider.Record{Provider: "tmdb", Kind: kind, ProviderID: strconv.Itoa(i), Title: "Popular " + strconv.Itoa(i)})
	}
	return out, nil
}
func (discoverTMDB) Person(_ context.Context, id string) (provider.Person, error) {
	if id != "7" {
		return provider.Person{}, provider.ErrNotFound
	}
	return provider.Person{Provider: "tmdb", ID: id, Name: "Sam Director", KnownFor: "Directing", Credits: []provider.Credit{{Record: provider.Record{Provider: "tmdb", Kind: "movie", ProviderID: "42", Title: "Synthetic Film"}, Role: "Director"}}}, nil
}
func (discoverTMDB) Studio(_ context.Context, kind, id string) (provider.Studio, error) {
	return provider.Studio{Provider: "tmdb", Kind: kind, ID: id, Name: "Synthetic Pictures", Titles: []provider.Record{{Provider: "tmdb", Kind: "movie", ProviderID: "61", Title: "Popular 61"}}}, nil
}

func TestDiscoveryPeopleAndRecommendations(t *testing.T) {
	f := testkit.New(t)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": discoverTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{f, s, t}
	origin := "http://127.0.0.1:8080"
	call := func(method, path string, body any) (int, string) {
		t.Helper()
		key := ""
		if path == "/api/v1/library/track" {
			key = testkit.Key(t, f)
		}
		rec := h.request(method, path, body, f.OwnerRaw, f.Owner.CSRF, origin, key)
		return rec.Code, rec.Body.String()
	}
	code, body := call("POST", "/api/v1/catalog/resolve", map[string]string{"provider": "tmdb", "media_type": "movie", "provider_id": "42"})
	if code != 200 {
		t.Fatal(code, body)
	}
	var item struct{ ID string }
	_ = json.Unmarshal([]byte(body), &item)
	if code, body = call("POST", "/api/v1/library/track", map[string]any{"catalog_id": item.ID, "status": "completed"}); code != 200 && code != 201 {
		t.Fatal(code, body)
	}
	feed := func() map[string]int {
		t.Helper()
		code, body := call("GET", "/api/v1/discover?media_type=movie", nil)
		if code != 200 {
			t.Fatal(code, body)
		}
		var out struct {
			Rows []struct {
				Key   string
				Items []struct {
					ProviderID string `json:"provider_id"`
				}
			}
		}
		_ = json.Unmarshal([]byte(body), &out)
		counts := map[string]int{}
		for _, r := range out.Rows {
			counts[r.Key] = len(r.Items)
			for _, it := range r.Items {
				if it.ProviderID == "42" {
					t.Fatal("tracked title suggested")
				}
			}
		}
		return counts
	}
	if got := feed(); got["trending"] != 5 || got["related:Synthetic Film"] != 5 {
		t.Fatalf("feed %v", got)
	}
	if code, _ = call("POST", "/api/v1/discover/dismissals", map[string]string{"provider": "tmdb", "media_type": "movie", "provider_id": "50"}); code != 204 {
		t.Fatal("dismiss", code)
	}
	if got := feed(); got["related:Synthetic Film"] != 4 {
		t.Fatalf("after dismissal %v", got)
	}
	if code, _ = call("DELETE", "/api/v1/discover/dismissals?provider=tmdb&media_type=movie&provider_id=50", nil); code != 204 {
		t.Fatal("restore", code)
	}
	if code, _ = call("GET", "/api/v1/discover?media_type=season", nil); code != 422 {
		t.Fatal("unsupported kind", code)
	}
	if code, body = call("GET", "/api/v1/people/tmdb/7", nil); code != 200 || !strings.Contains(body, `"catalog_id":"`+item.ID+`"`) {
		t.Fatal(code, body)
	}
	if code, _ = call("GET", "/api/v1/people/tmdb/8", nil); code != 404 {
		t.Fatal("unknown person", code)
	}
	if code, _ = call("GET", "/api/v1/people/bgg/7", nil); code != 404 {
		t.Fatal("provider without people", code)
	}
	if code, body = call("GET", "/api/v1/studios/tmdb/company/3", nil); code != 200 || !strings.Contains(body, "Synthetic Pictures") {
		t.Fatal(code, body)
	}

	// Recommendations between people.
	ctx := context.Background()
	member := f.User(t, "rec_member", "member")
	if code, body = call("GET", "/api/v1/recommendation-recipients?q=rec", nil); code != 200 || !strings.Contains(body, "rec_member") {
		t.Fatal(code, body)
	}
	if code, _ = call("POST", "/api/v1/recommendations", map[string]string{"to": "fixture_owner", "catalog_id": item.ID}); code != 422 {
		t.Fatal("recommended to self", code)
	}
	if code, body = call("POST", "/api/v1/recommendations", map[string]string{"to": "rec_member", "catalog_id": item.ID, "note": "You will like this"}); code != 201 {
		t.Fatal(code, body)
	}
	var rec struct{ ID string }
	_ = json.Unmarshal([]byte(body), &rec)
	lib := library.Store{Pool: f.Pool}
	inbox, _ := lib.Recommendations(ctx, member, "inbox")
	if len(inbox) != 1 || inbox[0].Note != "You will like this" || inbox[0].From.Username != "fixture_owner" || inbox[0].InLibrary {
		t.Fatalf("inbox %+v", inbox)
	}
	id, _ := strconv.ParseInt(rec.ID, 10, 64)
	if _, err := lib.Respond(ctx, f.Owner, id, true); err == nil {
		t.Fatal("sender accepted their own recommendation")
	}
	accepted, err := lib.Respond(ctx, member, id, true)
	if err != nil || accepted.Status != "accepted" || !accepted.InLibrary {
		t.Fatal(accepted, err)
	}
	if inbox, _ = lib.Recommendations(ctx, member, "inbox"); len(inbox) != 0 {
		t.Fatal("accepted recommendation still pending")
	}
	if code, body = call("GET", "/api/v1/recommendations?box=sent", nil); code != 200 || !strings.Contains(body, `"status":"accepted"`) {
		t.Fatal(code, body)
	}
}
