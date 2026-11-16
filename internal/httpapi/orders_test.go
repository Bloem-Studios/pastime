package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// orderedTMDB lists a story order that starts with episode 3.
type orderedTMDB struct{ syntheticTMDB }

func (orderedTMDB) EpisodeOrders(context.Context, string) ([]provider.EpisodeOrder, error) {
	return []provider.EpisodeOrder{{ID: "arc1", Name: "Story order", Kind: "Story arc"}}, nil
}
func (orderedTMDB) EpisodeOrder(_ context.Context, id string) (provider.EpisodeOrder, error) {
	if id != "arc1" {
		return provider.EpisodeOrder{}, provider.ErrNotFound
	}
	return provider.EpisodeOrder{ID: "arc1", Name: "Story order", Kind: "Story arc", Groups: []provider.EpisodeOrderGroup{{Name: "Prologue", Episodes: []string{"e3"}}, {Name: "Main", Episodes: []string{"e1", "e2"}}}}, nil
}

func TestEpisodeOrders(t *testing.T) {
	f := testkit.New(t)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": orderedTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{f, s, t}
	call := func(method, path string, body any) (int, string) {
		t.Helper()
		key := ""
		if method != "GET" {
			key = testkit.Key(t, f)
		}
		rec := h.request(method, path, body, f.OwnerRaw, f.Owner.CSRF, "http://127.0.0.1:8080", key)
		return rec.Code, rec.Body.String()
	}
	show, _ := cat.Resolve(context.Background(), "tmdb", "series", "100")
	base := "/api/v1/catalog/" + itoa(show.ID) + "/episode-orders"
	if code, body := call("GET", base, nil); code != 200 || !strings.Contains(body, `"id":"tmdb:arc1"`) {
		t.Fatal(code, body)
	}
	code, body := call("GET", base+"/tmdb%3Aarc1", nil)
	if code != 200 || strings.Index(body, "Prologue") > strings.Index(body, "Main") || !strings.Contains(body, "Episode 3") {
		t.Fatal(code, body)
	}
	_, body = call("POST", "/api/v1/library/track", map[string]any{"catalog_id": itoa(show.ID), "status": "in_progress"})
	var tracked struct {
		ID      string
		Version int
	}
	_ = json.Unmarshal([]byte(body), &tracked)
	next := func() string {
		var title string
		_ = f.Pool.QueryRow(context.Background(), "SELECT c.title FROM library_entries e JOIN catalog_items c ON c.id=e.next_item_id WHERE e.id=$1", tracked.ID).Scan(&title)
		return title
	}
	if n := next(); n != "Episode 1" {
		t.Fatal("aired order next", n)
	}
	if code, body = call("PUT", "/api/v1/library/"+tracked.ID+"/episode-order", map[string]any{"version": tracked.Version, "order_id": "tmdb:nope"}); code != 404 {
		t.Fatal("unknown order", code, body)
	}
	if code, body = call("PUT", "/api/v1/library/"+tracked.ID+"/episode-order", map[string]any{"version": tracked.Version, "order_id": "tmdb:arc1"}); code != 200 {
		t.Fatal(code, body)
	}
	if n := next(); n != "Episode 3" {
		t.Fatal("story order next", n)
	}
	if code, body = call("GET", "/api/v1/library/"+tracked.ID, nil); code != 200 || !strings.Contains(body, `"episode_order":"tmdb:arc1"`) {
		t.Fatal(code, body)
	}
}

func TestLocalizedTitles(t *testing.T) {
	f := testkit.New(t)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": syntheticTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{f, s, t}
	call := func(method, path string, body any) (int, string) {
		t.Helper()
		key := ""
		if path == "/api/v1/library/track" {
			key = testkit.Key(t, f)
		}
		rec := h.request(method, path, body, f.OwnerRaw, f.Owner.CSRF, "http://127.0.0.1:8080", key)
		return rec.Code, rec.Body.String()
	}
	ctx := context.Background()
	film, _ := cat.Resolve(ctx, "tmdb", "movie", "42")
	_, _ = f.Pool.Exec(ctx, `UPDATE catalog_items SET details=jsonb_set(details,'{titles}','{"de":"Synthetischer Film"}') WHERE id=$1`, film.ID)
	call("POST", "/api/v1/library/track", map[string]any{"catalog_id": itoa(film.ID), "status": "planned"})
	if _, body := call("GET", "/api/v1/library", nil); !strings.Contains(body, `"localized_title":null`) {
		t.Fatal("localized before choosing a language", body)
	}
	_, prefs := call("GET", "/api/v1/me/preferences", nil)
	var p map[string]any
	_ = json.Unmarshal([]byte(prefs), &p)
	p["title_language"] = "de"
	if code, body := call("PUT", "/api/v1/me/preferences", p); code != 200 {
		t.Fatal(code, body)
	}
	if _, body := call("GET", "/api/v1/library", nil); !strings.Contains(body, `"localized_title":"Synthetischer Film"`) {
		t.Fatal(body)
	}
	p["title_language"] = "german"
	if code, _ := call("PUT", "/api/v1/me/preferences", p); code != 422 && code != 400 {
		t.Fatal("bad language accepted", code)
	}
}
