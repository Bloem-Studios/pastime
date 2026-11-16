package connections_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/connections"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// fakeTMDB stands in for TMDB: show 100 has one season of three episodes; movie 42 exists.
// TVDB 9001 names S1E2 of show 100; IMDb tt42 names the movie.
type fakeTMDB struct{}

func n(v int) *int { return &v }

func (fakeTMDB) Name() string    { return "tmdb" }
func (fakeTMDB) Kinds() []string { return []string{"movie", "series"} }
func (fakeTMDB) Search(ctx context.Context, kind, q string) ([]provider.Record, error) {
	return []provider.Record{{Provider: "tmdb", Kind: kind, ProviderID: "100", Title: "Synthetic Harbour"}}, nil
}
func (fakeTMDB) Fetch(ctx context.Context, kind, id string) (provider.Record, error) {
	if kind == "movie" {
		return provider.Record{Provider: "tmdb", Kind: "movie", ProviderID: id, Title: "Synthetic Film", RuntimeMinutes: n(90), Total: n(90), ExternalIDs: map[string]string{"imdb": "tt42"}}, nil
	}
	return provider.Record{Provider: "tmdb", Kind: "series", ProviderID: id, Title: "Synthetic Harbour", Total: n(3), ChildrenLoaded: true,
		Children: []provider.Record{{Provider: "tmdb", Kind: "season", ProviderID: id + ":1", Title: "Season 1", SeasonNumber: n(1), Total: n(3)}}}, nil
}
func (fakeTMDB) FetchChild(ctx context.Context, parent, child provider.Record) (provider.Record, error) {
	out := child
	out.ChildrenLoaded = true
	for e := 1; e <= 3; e++ {
		out.Children = append(out.Children, provider.Record{Provider: "tmdb", Kind: "episode", ProviderID: "ep" + strconv.Itoa(e), Title: "Episode " + strconv.Itoa(e), SeasonNumber: n(1), EpisodeNumber: n(e)})
	}
	return out, nil
}
func (fakeTMDB) Find(ctx context.Context, source, id string) (provider.Found, error) {
	switch source + ":" + id {
	case "tvdb:9001":
		return provider.Found{Episodes: []provider.FoundEpisode{{ShowID: "100", Season: 1, Episode: 2}}}, nil
	case "imdb:tt42":
		return provider.Found{Movies: []string{"42"}}, nil
	}
	return provider.Found{}, provider.ErrNotFound
}

func TestWebhookScrobblesThroughTheQueue(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	store := connections.Store{Pool: f.Pool}
	member := f.User(t, "webhook_member", "member")
	c, raw, err := store.Connect(ctx, member, "plex", connections.Settings{Username: "Viewer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Authenticate(ctx, "jellyfin", raw); err == nil {
		t.Fatal("secret accepted for another server type")
	}
	authed, err := store.Authenticate(ctx, "plex", raw)
	if err != nil || authed.ID != c.ID {
		t.Fatal("secret rejected", err)
	}
	run := store.Handler(cat, lib)
	deliver := func(body string) {
		t.Helper()
		ev, err := connections.ParsePlex([]byte(body), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Enqueue(ctx, authed, ev); err != nil {
			t.Fatal(err)
		}
		var payload json.RawMessage
		if err = f.Pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE kind=$1 ORDER BY created_at DESC LIMIT 1", connections.ScrobbleJob).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if err = run(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	episode := `{"event":"media.scrobble","Account":{"title":"Viewer"},"Metadata":{"type":"episode","grandparentTitle":"Synthetic Harbour","parentIndex":1,"index":2,"Guid":[{"id":"tvdb://9001"}]}}`
	deliver(episode)
	deliver(episode) // A repeated delivery within the window records once.
	deliver(`{"event":"media.scrobble","Metadata":{"type":"movie","title":"Synthetic Film","Guid":[{"id":"imdb://tt42"}]}}`)
	// An episode the show does not list yet is reported as not matched, not retried.
	deliver(`{"event":"media.scrobble","Account":{"title":"Viewer"},"Metadata":{"type":"episode","grandparentTitle":"Synthetic Harbour","parentIndex":1,"index":4}}`)
	if list, _ := store.List(ctx, member); len(list) != 1 || !strings.HasPrefix(list[0].LastOutcome, "Not matched") {
		t.Fatalf("unlisted episode outcome: %+v", list)
	}
	history, err := lib.History(ctx, member, library.HistoryFilter{})
	if err != nil || len(history.Items) != 2 || history.Items[1].EpisodeNumber == nil || *history.Items[1].EpisodeNumber != 2 || history.Items[0].Source != "plex" {
		t.Fatalf("history after webhooks: %+v %v", history.Items, err)
	}
	list, _ := store.List(ctx, member)
	if len(list) != 1 || list[0].EventsRecorded != 2 || list[0].LastOutcome == "" {
		t.Fatalf("connection status: %+v", list)
	}
	// Nothing reaches another account, and a deleted connection records nothing more.
	if other, _ := lib.History(ctx, f.Owner, library.HistoryFilter{}); len(other.Items) != 0 {
		t.Fatal("scrobble leaked to another account")
	}
	if recorded, err := lib.Scrobble(ctx, member.User.ID, c.ID, "plex", history.Items[0].CatalogID, history.Items[0].CatalogID, time.Now().Add(24*time.Hour)); err != nil || !recorded {
		t.Fatal("a later viewing should record", err)
	}
	if err = store.Disconnect(ctx, member, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = lib.Scrobble(ctx, member.User.ID, c.ID, "plex", history.Items[0].CatalogID, history.Items[0].CatalogID, time.Now().Add(48*time.Hour)); err == nil {
		t.Fatal("deleted connection still records plays")
	}
	// A disabled account's connection cannot authenticate.
	_, raw2, err := store.Connect(ctx, member, "emby", connections.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := f.Accounts.Authenticate(ctx, f.OwnerRaw, "session")
	current, _ := f.Accounts.User(ctx, fresh, member.User.ID)
	if _, err = f.Accounts.UpdateUser(ctx, fresh, member.User.ID, identity.UserPatch{Version: current.Version, Active: ptr(false)}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Authenticate(ctx, "emby", raw2); err == nil {
		t.Fatal("disabled account webhook accepted")
	}
}

func ptr[T any](v T) *T { return &v }

// A media request adds the show as planned once and never downgrades a tracked title.
func TestRequestsArePlanned(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	store := connections.Store{Pool: f.Pool}
	member := f.User(t, "request_member", "member")
	c, _, err := store.Connect(ctx, member, "seerr", connections.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	run := store.Handler(cat, lib)
	deliver := func(body string) {
		t.Helper()
		ev, err := connections.ParseSeerr([]byte(body), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Enqueue(ctx, c, ev); err != nil {
			t.Fatal(err)
		}
		var payload json.RawMessage
		_ = f.Pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE kind=$1 ORDER BY created_at DESC LIMIT 1", connections.ScrobbleJob).Scan(&payload)
		if err = run(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	deliver(`{"media_type":"tv","media_tmdbid":100,"media_status":"PENDING"}`)
	deliver(`{"media_type":"tv","media_tmdbid":100,"media_status":"AVAILABLE"}`)
	entries, _ := lib.Entries(ctx, member, library.Filter{Limit: 10})
	if len(entries.Items) != 1 || entries.Items[0].Status != "planned" {
		t.Fatalf("entries %+v", entries.Items)
	}
	list, _ := store.List(ctx, member)
	if list[0].EventsRecorded != 1 || list[0].LastOutcome == "" {
		t.Fatalf("connection %+v", list)
	}
}

func TestIdentifiedWebhookEventsPreserveRewatchesAndReplay(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	store := connections.Store{Pool: f.Pool}
	lib := library.Store{Pool: f.Pool}
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb"}}
	c, _, err := store.Connect(ctx, f.Owner, "plex", connections.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	ev := connections.Event{EventID: "view-1", Reference: catalog.Reference{Kind: "movie", TMDB: "42"}, PlayedAt: time.Now().UTC()}
	for _, id := range []string{"view-1", "view-2", "view-1"} {
		ev.EventID = id
		if err = store.Enqueue(ctx, c, ev); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind=$1", connections.ScrobbleJob).Scan(&count); err != nil || count != 2 {
		t.Fatal("delivery receipts did not deduplicate", count, err)
	}
	rows, err := f.Pool.Query(ctx, "SELECT payload FROM jobs WHERE kind=$1 ORDER BY id", connections.ScrobbleJob)
	if err != nil {
		t.Fatal(err)
	}
	var payloads []json.RawMessage
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, raw)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	handler := store.Handler(cat, lib)
	for _, raw := range append(payloads, payloads[0]) {
		if err = handler(ctx, raw); err != nil {
			t.Fatal(err)
		}
	}
	history, err := lib.History(ctx, f.Owner, library.HistoryFilter{})
	if err != nil || len(history.Items) != 2 {
		t.Fatal("rapid rewatch lost or replay duplicated", len(history.Items), err)
	}
	ev.EventID = "view-1"
	ev.Reference.TMDB = "another"
	if err = store.Enqueue(ctx, c, ev); err == nil {
		t.Fatal("reused event identity changed intent")
	}
}

func TestReplacementConnectionRejectsOldInFlightWork(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	store := connections.Store{Pool: f.Pool}
	lib := library.Store{Pool: f.Pool}
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb"}}
	old, _, err := store.Connect(ctx, f.Owner, "generic", connections.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	ev := connections.Event{Reference: catalog.Reference{Kind: "movie", TMDB: "42"}, PlayedAt: time.Now().UTC(), EventID: "before-rotation"}
	if err = store.Enqueue(ctx, old, ev); err != nil {
		t.Fatal(err)
	}
	var payload json.RawMessage
	if err = f.Pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE kind=$1", connections.ScrobbleJob).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Connect(ctx, f.Owner, "generic", connections.Settings{}); err != nil {
		t.Fatal(err)
	}
	if err = store.Enqueue(ctx, old, ev); err == nil {
		t.Fatal("stale authenticated request queued after rotation")
	}
	if err = store.Handler(cat, lib)(ctx, payload); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM plays WHERE user_id=$1", f.Owner.User.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old queued work survived rotation", count, err)
	}
}
