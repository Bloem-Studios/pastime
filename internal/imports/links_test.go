package imports_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

type toServer struct{ target *url.URL }

func (r toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host, clone.Host = r.target.Scheme, r.target.Host, r.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// A linked Radarr is synced on schedule into planned titles, and a second sync adds nothing.
func TestLinkedServiceSyncsOnSchedule(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/movie" || r.Header.Get("X-Api-Key") != "k" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"title":"Synthetic Film","year":2001,"tmdbId":42,"monitored":true}]`))
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	store := imports.Store{Pool: f.Pool, Catalog: cat, Library: lib, Box: box, HTTP: &http.Client{Transport: toServer{target}}}
	member := f.User(t, "linked_member", "member")

	// Without SECRET_KEY nothing can be linked.
	if _, err := (imports.Store{Pool: f.Pool}).SaveLink(ctx, member, imports.LinkInput{Kind: "radarr", Schedule: "daily"}); err == nil {
		t.Fatal("linked without a key")
	}
	if _, err := store.SaveLink(ctx, member, imports.LinkInput{Kind: "radarr", Schedule: "daily", Config: imports.LinkConfig{URL: "http://arr.local"}}); err == nil {
		t.Fatal("missing token accepted")
	}
	link, err := store.SaveLink(ctx, member, imports.LinkInput{Kind: "radarr", Schedule: "daily", Config: imports.LinkConfig{URL: "http://arr.local:7878", Token: "k"}})
	if err != nil || link.Description != "arr.local:7878" {
		t.Fatal(link, err)
	}
	var sealedConfig []byte
	_ = f.Pool.QueryRow(ctx, "SELECT sealed_config FROM service_links WHERE id=$1", link.ID).Scan(&sealedConfig)
	if json.Valid(sealedConfig) {
		t.Fatal("configuration stored in the clear")
	}
	runDue := func() imports.Run {
		t.Helper()
		n, err := store.EnqueueDue(ctx)
		if err != nil || n != 1 {
			t.Fatalf("due %d %v", n, err)
		}
		links, _ := store.Links(ctx, member)
		payload, _ := json.Marshal(map[string]string{"run_id": *links[0].LastRunID})
		if err := store.Handler(ctx, payload); err != nil {
			t.Fatal(err)
		}
		r, _ := store.Get(ctx, member, *links[0].LastRunID)
		return r
	}
	first := runDue()
	if first.Status != "succeeded" || first.Imported != 1 || first.Source != "radarr" {
		t.Fatalf("first sync %+v", first)
	}
	entries, _ := lib.Entries(ctx, member, library.Filter{Limit: 10})
	if len(entries.Items) != 1 || entries.Items[0].Status != "planned" {
		t.Fatalf("entries %+v", entries.Items)
	}
	// Not due again until tomorrow; a manual sync adds nothing new.
	if n, _ := store.EnqueueDue(ctx); n != 0 {
		t.Fatal("scheduled twice")
	}
	again, err := store.SyncNow(ctx, member, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	var busy *fault.Error
	if _, err := store.SyncNow(ctx, member, link.ID); !errors.As(err, &busy) || busy.Code != "import_in_progress" {
		t.Fatal("concurrent sync allowed", err)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": again.ID})
	_ = store.Handler(ctx, payload)
	second, _ := store.Get(ctx, member, again.ID)
	if second.Imported != 0 || second.Unchanged != 1 {
		t.Fatalf("second sync %+v", second)
	}
	// Another person cannot see or delete it.
	other := f.User(t, "linked_other", "member")
	if err := store.DeleteLink(ctx, other, link.ID); err == nil {
		t.Fatal("deleted another person's link")
	}
	if err := store.DeleteLink(ctx, member, link.ID); err != nil {
		t.Fatal(err)
	}
}

// A list following MDBList mirrors it: new titles are planned, the list matches the
// source's order, and titles dropped from the source leave the list but not the library.
func TestFollowedListMirrorsSource(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	body := `[{"id":42,"title":"Synthetic Film","imdb_id":"tt42","mediatype":"movie","release_year":2001},{"id":100,"title":"Synthetic Harbour","mediatype":"show"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lists/curator/weekend/json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	store := imports.Store{Pool: f.Pool, Catalog: cat, Library: lib, HTTP: &http.Client{Transport: toServer{target}}}
	member := f.User(t, "follow_member", "member")
	made, err := lib.SaveList(ctx, member, 0, testkit.Key(t, f), library.ListInput{Name: "Weekend", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FollowList(ctx, member, made.ID, "https://example.com/not-a-list"); err == nil {
		t.Fatal("bad address accepted")
	}
	if err := store.FollowList(ctx, member, made.ID, "https://mdblist.com/lists/curator/weekend"); err != nil {
		t.Fatal(err)
	}
	sync := func() {
		t.Helper()
		var payload json.RawMessage
		if err := f.Pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE kind=$1 ORDER BY created_at DESC LIMIT 1", imports.ListSyncJob).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if err := store.ListSyncHandler(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	list, err := lib.List(ctx, member, made.ID)
	if err != nil || list.ItemCount != 2 || list.Source == nil || list.Source.URL != "https://mdblist.com/lists/curator/weekend" || list.Source.SyncedAt == nil {
		t.Fatalf("list %+v %v", list, err)
	}
	body = `[{"id":100,"title":"Synthetic Harbour","mediatype":"show"}]`
	if n, err := store.EnqueueListSyncs(ctx, "2099-01-01"); err != nil || n != 0 {
		t.Fatal("synced again too soon", n, err)
	}
	_, _ = f.Pool.Exec(ctx, "UPDATE personal_lists SET synced_at=now()-interval '2 days' WHERE id=$1", made.ID)
	if n, err := store.EnqueueListSyncs(ctx, "2099-01-01"); err != nil || n != 1 {
		t.Fatal("not scheduled", n, err)
	}
	sync()
	list, _ = lib.List(ctx, member, made.ID)
	entries, _ := lib.Entries(ctx, member, library.Filter{Limit: 10})
	if list.ItemCount != 1 || len(entries.Items) != 2 {
		t.Fatalf("after source change: list %d, library %d", list.ItemCount, len(entries.Items))
	}
	if err := store.UnfollowList(ctx, member, made.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = lib.List(ctx, member, made.ID); list.Source != nil || list.ItemCount != 1 {
		t.Fatalf("unfollowed %+v", list)
	}
}

// With push on, plays recorded here after linking are marked played on Jellyfin; plays
// from before the link and plays that came from Jellyfin are not.
func TestJellyfinPush(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	var pushed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/Users/u1/PlayedItems/"):
			pushed = append(pushed, strings.TrimPrefix(r.URL.Path, "/Users/u1/PlayedItems/"))
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/Users/u1/Items" && strings.Contains(r.URL.RawQuery, "AnyProviderIdEquals=Tmdb.42"):
			_, _ = w.Write([]byte(`{"Items":[{"Id":"jf42"}]}`))
		case r.URL.Path == "/Users/u1/Items":
			_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	store := imports.Store{Pool: f.Pool, Catalog: cat, Library: lib, Box: box, HTTP: &http.Client{Transport: toServer{target}}}
	member := f.User(t, "push_member", "member")
	film, _ := cat.Resolve(ctx, "tmdb", "movie", "42")
	play := func(source, id string) {
		t.Helper()
		if _, err := lib.ApplyImport(ctx, member.User.ID, source, library.ImportItem{TitleID: film.ID, Plays: []library.ImportPlay{{TargetID: film.ID, At: time.Now().UTC(), ExternalID: id}}}); err != nil {
			t.Fatal(err)
		}
	}
	play("native", "before-link")
	link, err := store.SaveLink(ctx, member, imports.LinkInput{Kind: "jellyfin", Schedule: "manual", Config: imports.LinkConfig{URL: "http://media.local", Token: "t", UserID: "u1", Push: true}})
	if err != nil {
		t.Fatal(err)
	}
	sync := func() {
		t.Helper()
		run, err := store.SyncNow(ctx, member, link.ID)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]string{"run_id": run.ID})
		if err := store.Handler(ctx, payload); err != nil {
			t.Fatal(err)
		}
		var push []byte
		if err := f.Pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE dedupe_key=$1", "playback_push:"+run.ID).Scan(&push); err != nil {
			t.Fatal(err)
		}
		if err := store.PushHandler(ctx, push); err != nil {
			t.Fatal(err)
		}
	}
	sync() // sets the starting point
	time.Sleep(10 * time.Millisecond)
	play("jellyfin", "from-jellyfin")
	play("native", "after-link")
	sync()
	if len(pushed) != 1 || pushed[0] != "jf42" {
		t.Fatalf("pushed %v", pushed)
	}
	sync()
	if len(pushed) != 1 {
		t.Fatalf("pushed twice %v", pushed)
	}
	// Newly entered, backdated plays must not fall behind a played-at cursor.
	if _, err := lib.ApplyImport(ctx, member.User.ID, "native", library.ImportItem{TitleID: film.ID, Plays: []library.ImportPlay{{TargetID: film.ID, At: time.Now().AddDate(-1, 0, 0), ExternalID: "backdated"}}}); err != nil {
		t.Fatal(err)
	}
	sync()
	if len(pushed) != 2 {
		t.Fatal("backdated play never pushed", pushed)
	}
}

// The same viewing reported by two services counts once; a later viewing still counts.
func TestPlaysDeduplicateAcrossSources(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	member := f.User(t, "dedupe_member", "member")
	film, _ := cat.Resolve(ctx, "tmdb", "movie", "42")
	at := time.Date(2026, 1, 2, 20, 0, 0, 0, time.UTC)
	add := func(source, id string, when time.Time) int {
		t.Helper()
		out, err := lib.ApplyImport(ctx, member.User.ID, source, library.ImportItem{TitleID: film.ID, Plays: []library.ImportPlay{{TargetID: film.ID, At: when, ExternalID: id}}})
		if err != nil {
			t.Fatal(err)
		}
		return out.PlaysAdded
	}
	if add("trakt", "t1", at) != 1 || add("plex", "p1", at.Add(20*time.Minute)) != 0 || add("plex", "p2", at.Add(48*time.Hour)) != 1 {
		t.Fatal("cross-source dedupe")
	}
}
