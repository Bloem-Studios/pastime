package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// syntheticTMDB: show 100 (TVDB 700) has one season of three episodes; movie 42.
type syntheticTMDB struct{}

func iptr(v int) *int { return &v }

func (syntheticTMDB) Name() string    { return "tmdb" }
func (syntheticTMDB) Kinds() []string { return []string{"movie", "series"} }
func (syntheticTMDB) Search(context.Context, string, string) ([]provider.Record, error) {
	return nil, provider.ErrUnavailable
}
func (syntheticTMDB) Fetch(_ context.Context, kind, id string) (provider.Record, error) {
	if kind == "movie" {
		return provider.Record{Provider: "tmdb", Kind: "movie", ProviderID: id, Title: "Synthetic Film", RuntimeMinutes: iptr(100), Total: iptr(100), ExternalIDs: map[string]string{"imdb": "tt42"}}, nil
	}
	return provider.Record{Provider: "tmdb", Kind: "series", ProviderID: id, Title: "Synthetic Harbour", Total: iptr(3), ChildrenLoaded: true, ExternalIDs: map[string]string{"tvdb": "700"},
		Children: []provider.Record{{Provider: "tmdb", Kind: "season", ProviderID: id + ":1", Title: "Season 1", SeasonNumber: iptr(1), Total: iptr(3)}}}, nil
}
func (syntheticTMDB) FetchChild(_ context.Context, parent, child provider.Record) (provider.Record, error) {
	out := child
	out.ChildrenLoaded = true
	for e := 1; e <= 3; e++ {
		out.Children = append(out.Children, provider.Record{Provider: "tmdb", Kind: "episode", ProviderID: "e" + strconv.Itoa(e), Title: "Episode " + strconv.Itoa(e), SeasonNumber: iptr(1), EpisodeNumber: iptr(e), RuntimeMinutes: iptr(45)})
	}
	return out, nil
}
func (syntheticTMDB) Find(_ context.Context, source, id string) (provider.Found, error) {
	if source == "tvdb" && id == "700" {
		return provider.Found{Shows: []string{"100"}}, nil
	}
	return provider.Found{}, provider.ErrNotFound
}

func TestFloppyPluginProtocol(t *testing.T) {
	f := testkit.New(t)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": syntheticTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	member := f.User(t, "silo_member", "member")
	_, token, err := f.Accounts.MintToken(ctx, member, identity.TokenInput{Name: "Silo", Scopes: []string{"library:read", "library:write"}, ExpiresInDays: 30, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	_, readOnly, err := f.Accounts.MintToken(ctx, member, identity.TokenInput{Name: "Read", Scopes: []string{"library:read"}, ExpiresInDays: 30, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, scheme, tok string, body any) (int, map[string]any) {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", scheme+" "+tok)
		}
		w := httptest.NewRecorder()
		s.Router.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, out := call("GET", "/apis/listenbrainz/1/validate-token", "Token", token, nil); code != 200 || out["valid"] != true || out["user_name"] != "silo_member" {
		t.Fatalf("validate: %d %v", code, out)
	}
	if _, out := call("GET", "/apis/listenbrainz/1/validate-token", "Token", readOnly, nil); out["valid"] != false {
		t.Fatal("read-only token reported valid for sync")
	}
	if code, _ := call("POST", "/api/v1/scrobble/", "Bearer", readOnly, map[string]any{"action": "stop"}); code != 403 {
		t.Fatalf("read-only scrobble: %d", code)
	}
	// Start with a position stores resume progress; episodes are identified by series ids.
	episode := map[string]any{"action": "pause", "media_type": "episode", "ids": map[string]string{"tvdb": "700"}, "series_title": "Synthetic Harbour",
		"season_number": 1, "episode_number": 2, "position_seconds": 600, "duration_seconds": 2700, "future_field": true}
	if code, out := call("POST", "/api/v1/scrobble/", "Bearer", token, episode); code != 200 || out["completed"] != false {
		t.Fatalf("pause: %d %v", code, out)
	}
	code, progress := call("GET", "/api/v1/playback/progress/?limit=200&offset=0&media_type=movie,episode&completed=false", "Bearer", token, nil)
	results, _ := progress["results"].([]any)
	if code != 200 || len(results) != 1 {
		t.Fatalf("progress: %d %v", code, progress)
	}
	first := results[0].(map[string]any)
	if first["media_id"] != "100" || first["source"] != "tmdb" || first["position_seconds"] != float64(600) || first["series_title"] != "Synthetic Harbour" || first["ids"].(map[string]any)["tvdb_id"] != "700" {
		t.Fatalf("progress entry: %v", first)
	}
	// A completed stop records a play at the reported time and clears the position.
	at := time.Date(2026, 9, 20, 20, 15, 30, 123000000, time.UTC)
	episode["action"], episode["completed"], episode["played_at"] = "stop", true, at.Format(time.RFC3339Nano)
	if code, out := call("POST", "/api/v1/scrobble/", "Bearer", token, episode); code != 200 || out["completed"] != true {
		t.Fatalf("stop: %d %v", code, out)
	}
	if _, progress = call("GET", "/api/v1/playback/progress/", "Bearer", token, nil); len(progress["results"].([]any)) != 0 {
		t.Fatal("finished item kept its resume position")
	}
	code, history := call("GET", "/api/v1/history/?limit=50&offset=0&logging_style=sessions&start_date=2026-09-19&end_date=2026-09-21", "Bearer", token, nil)
	days, _ := history["results"].([]any)
	if code != 200 || len(days) != 1 {
		t.Fatalf("history: %d %v", code, history)
	}
	entry := days[0].(map[string]any)["entries"].([]any)[0].(map[string]any)
	item := entry["item"].(map[string]any)
	if entry["status"] != "Completed" || entry["played_at_local"] != at.Format(time.RFC3339Nano) || entry["media_type"] != "episode" ||
		item["media_id"] != "100" || item["source"] != "tmdb" || item["season_number"] != float64(1) || item["episode_number"] != float64(2) || item["provider_external_ids"].(map[string]any)["tvdb_id"] != "700" {
		t.Fatalf("history entry: %v", entry)
	}
	page := history["pagination"].(map[string]any)
	if page["offset"] != float64(0) || page["total"] != float64(1) || page["next"] != "" {
		t.Fatalf("pagination: %v", page)
	}
	// Outside the window, and for another account, there is nothing.
	if _, other := call("GET", "/api/v1/history/?start_date=2026-09-22", "Bearer", token, nil); len(other["results"].([]any)) != 0 {
		t.Fatal("date filter ignored")
	}
	_, ownerToken, _ := f.Accounts.MintToken(ctx, f.Owner, identity.TokenInput{Name: "Owner", Scopes: []string{"library:read", "library:write"}, ExpiresInDays: 1, Password: testkit.Password})
	if _, other := call("GET", "/api/v1/history/", "Bearer", ownerToken, nil); len(other["results"].([]any)) != 0 {
		t.Fatal("history leaked across accounts")
	}
	// An unmatched identifier is rejected rather than recorded against a guess.
	if code, _ := call("POST", "/api/v1/scrobble/", "Bearer", token, map[string]any{"action": "stop", "completed": true, "media_type": "movie", "ids": map[string]string{"imdb": "tt999"}}); code != 404 {
		t.Fatalf("unmatched scrobble: %d", code)
	}
}
