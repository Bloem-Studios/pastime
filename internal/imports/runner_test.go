package imports_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/exports"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// fakeTMDB: movie 42 (IMDb tt42) and show 100 with one three-episode season.
type fakeTMDB struct{}

func n(v int) *int { return &v }

func (fakeTMDB) Name() string    { return "tmdb" }
func (fakeTMDB) Kinds() []string { return []string{"movie", "series"} }
func (fakeTMDB) Search(_ context.Context, kind, q string) ([]provider.Record, error) {
	return nil, nil
}
func (fakeTMDB) Fetch(_ context.Context, kind, id string) (provider.Record, error) {
	if kind == "movie" {
		if id != "42" {
			return provider.Record{}, provider.ErrNotFound
		}
		return provider.Record{Provider: "tmdb", Kind: "movie", ProviderID: "42", Title: "Synthetic Film", RuntimeMinutes: n(100), Total: n(100), ExternalIDs: map[string]string{"imdb": "tt42"}}, nil
	}
	return provider.Record{Provider: "tmdb", Kind: "series", ProviderID: id, Title: "Synthetic Harbour", Total: n(3), ChildrenLoaded: true,
		Children: []provider.Record{{Provider: "tmdb", Kind: "season", ProviderID: id + ":1", Title: "Season 1", SeasonNumber: n(1), Total: n(3)}}}, nil
}
func (fakeTMDB) FetchChild(_ context.Context, parent, child provider.Record) (provider.Record, error) {
	out := child
	out.ChildrenLoaded = true
	for e := 1; e <= 3; e++ {
		out.Children = append(out.Children, provider.Record{Provider: "tmdb", Kind: "episode", ProviderID: "e" + strconv.Itoa(e), Title: "Episode " + strconv.Itoa(e), SeasonNumber: n(1), EpisodeNumber: n(e)})
	}
	return out, nil
}
func (fakeTMDB) Find(_ context.Context, source, id string) (provider.Found, error) {
	if source == "imdb" && id == "tt42" {
		return provider.Found{Movies: []string{"42"}}, nil
	}
	return provider.Found{}, provider.ErrNotFound
}

func traktArchive(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range map[string]string{
		"watched-history-1.json": `[
			{"id":1,"watched_at":"2026-01-02T20:00:00.000Z","type":"movie","movie":{"title":"Synthetic Film","year":2001,"ids":{"imdb":"tt42"}}},
			{"id":2,"watched_at":"2026-02-01T21:00:00.000Z","type":"episode","show":{"title":"Synthetic Harbour","ids":{"tmdb":100}},"episode":{"season":1,"number":1}},
			{"id":3,"watched_at":"2026-02-02T21:00:00.000Z","type":"episode","show":{"title":"Synthetic Harbour","ids":{"tmdb":100}},"episode":{"season":1,"number":2}},
			{"id":4,"watched_at":"2026-02-03T21:00:00.000Z","type":"movie","movie":{"title":"Unknown Film","year":1990,"ids":{"imdb":"tt0"}}}]`,
		"ratings-movies.json": `[{"rating":8,"type":"movie","movie":{"title":"Synthetic Film","year":2001,"ids":{"imdb":"tt42"}}}]`,
	} {
		w, _ := z.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = z.Close()
	return b.Bytes()
}

func TestTraktImportIsIdempotentAndExportRoundTrips(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	store := imports.Store{Pool: f.Pool, Catalog: cat, Library: lib}
	member := f.User(t, "import_member", "member")
	run := func(source string, data []byte) imports.Run {
		t.Helper()
		r, err := store.Start(ctx, member, source, "export.zip", data, "")
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]string{"run_id": r.ID})
		if err = store.Handler(ctx, payload); err != nil {
			t.Fatal(err)
		}
		if r, err = store.Get(ctx, member, r.ID); err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := run("trakt", traktArchive(t))
	if first.Status != "partial_success" || first.Total != 3 || first.Imported != 2 || first.PlaysAdded != 3 || first.Unmatched != 1 || len(first.UnmatchedSample) != 1 {
		t.Fatalf("first import: %+v", first)
	}
	var upload []byte
	if err := f.Pool.QueryRow(ctx, "SELECT upload FROM import_runs WHERE id=$1", first.ID).Scan(&upload); err != nil || len(upload) == 0 {
		t.Fatal("partial import lost upload", err)
	}
	history, _ := lib.History(ctx, member, library.HistoryFilter{})
	if len(history.Items) != 3 {
		t.Fatalf("history after import: %d", len(history.Items))
	}
	stats, _ := lib.Stats(ctx, member)
	if stats.Completed != 1 || stats.InProgress != 1 {
		t.Fatalf("stats after import: %+v", stats)
	}
	// Running the same export again adds nothing.
	second := run("trakt", traktArchive(t))
	if second.PlaysAdded != 0 || second.Imported != 0 || second.Unchanged != 2 {
		t.Fatalf("rerun: %+v", second)
	}
	// The native export re-imports into another account with the same history.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := lib.Export(ctx, member, func(e library.ExportEntry) error {
		out := imports.NativeEntry{Type: "entry", Provider: e.Provider, ProviderID: e.ProviderID, Kind: e.Kind, Title: e.Title, Status: e.Status, Progress: e.Progress, Rating: e.Rating}
		for _, p := range e.Plays {
			out.Plays = append(out.Plays, imports.NativePlay{At: p.At, Season: p.Season, Episode: p.Episode, ChildID: p.ChildID, ID: strconv.FormatInt(p.ID, 10)})
		}
		return enc.Encode(out)
	}); err != nil {
		t.Fatal(err)
	}
	other := f.User(t, "import_other", "member")
	r, err := store.Start(ctx, other, "native", "library.jsonl", buf.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": r.ID})
	if err = store.Handler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	copied, _ := lib.History(ctx, other, library.HistoryFilter{})
	if len(copied.Items) != 3 {
		t.Fatalf("round trip plays: %d", len(copied.Items))
	}
	entries, _ := lib.Entries(ctx, other, library.Filter{})
	for _, e := range entries.Items {
		if e.Title == "Synthetic Film" && (e.Rating == nil || *e.Rating != 8) {
			t.Fatal("rating lost in round trip")
		}
	}
	// A second import cannot start while one is queued, and a queued import can be cancelled.
	queued, err := store.Start(ctx, member, "trakt", "x.zip", traktArchive(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Start(ctx, member, "trakt", "y.zip", traktArchive(t), ""); err == nil {
		t.Fatal("concurrent import accepted")
	}
	if c, err := store.Cancel(ctx, member, queued.ID); err != nil || c.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", c, err)
	}
	// A disabled account's import stops without writing.
	fresh, _ := f.Accounts.Authenticate(ctx, f.OwnerRaw, "session")
	next, err := store.Start(ctx, member, "trakt", "z.zip", traktArchive(t), "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := f.Accounts.User(ctx, fresh, member.User.ID)
	off := false
	if _, err = f.Accounts.UpdateUser(ctx, fresh, member.User.ID, identity.UserPatch{Version: u.Version, Active: &off}, false); err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(map[string]string{"run_id": next.ID})
	if err = store.Handler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	var status, message string
	_ = f.Pool.QueryRow(ctx, "SELECT status,error FROM import_runs WHERE id=$1", next.ID).Scan(&status, &message)
	if status != "failed" || message == "" {
		t.Fatalf("disabled account import: %s %q", status, message)
	}
}

func TestUnmatchedRepairAndRollback(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	lib := library.Store{Pool: f.Pool}
	store := imports.Store{Pool: f.Pool, Catalog: cat, Library: lib}
	member := f.User(t, "repair_member", "member")
	r, err := store.Start(ctx, member, "trakt", "export.zip", traktArchive(t), "")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": r.ID})
	if err = store.Handler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Unmatched(ctx, member, r.ID, "", "", 0)
	if err != nil || len(pending.Items) != 1 || pending.Items[0].Label == "" || pending.Items[0].Plays != 1 {
		t.Fatalf("unmatched queue: %+v %v", pending, err)
	}
	other := f.User(t, "repair_other", "member")
	if list, _ := store.Unmatched(ctx, other, "", "", "", 0); len(list.Items) != 0 {
		t.Fatal("unmatched titles leaked across accounts")
	}
	film, err := cat.Resolve(ctx, "tmdb", "movie", "42")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Resolve(ctx, other, pending.Items[0].ID, film.ID); err == nil {
		t.Fatal("another account resolved the title")
	}
	// A failure recording the repair must roll back the imported play as well.
	if _, err = f.Pool.Exec(ctx, `CREATE FUNCTION reject_repair() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic repair failure'; END $$;
		CREATE TRIGGER reject_repair BEFORE UPDATE ON import_unmatched FOR EACH ROW EXECUTE FUNCTION reject_repair()`); err != nil {
		t.Fatal(err)
	}
	before, _ := lib.History(ctx, member, library.HistoryFilter{})
	if _, err = store.Resolve(ctx, member, pending.Items[0].ID, film.ID); err == nil {
		t.Fatal("expected injected repair failure")
	}
	after, _ := lib.History(ctx, member, library.HistoryFilter{})
	if len(after.Items) != len(before.Items) {
		t.Fatal("failed repair committed library changes")
	}
	if _, err = f.Pool.Exec(ctx, "DROP TRIGGER reject_repair ON import_unmatched"); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Resolve(ctx, member, pending.Items[0].ID, film.ID)
	if err != nil || resolved.Status != "resolved" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	if _, err = store.Ignore(ctx, member, pending.Items[0].ID); err == nil {
		t.Fatal("handled title ignored again")
	}
	history, _ := lib.History(ctx, member, library.HistoryFilter{})
	if len(history.Items) != 4 {
		t.Fatalf("history after resolve: %d", len(history.Items))
	}
	// Rolling back removes every play the run added and the titles it created.
	back, entries, plays, err := store.Rollback(ctx, member, r.ID)
	if err != nil || back.Status != "rolled_back" || plays != 4 || entries != 2 {
		t.Fatalf("rollback: %+v entries %d plays %d %v", back, entries, plays, err)
	}
	if stats, _ := lib.Stats(ctx, member); stats.Total != 0 {
		t.Fatalf("library after rollback: %+v", stats)
	}
	if _, _, _, err = store.Rollback(ctx, member, r.ID); err == nil {
		t.Fatal("rolled back twice")
	}
}

// A CLZ export adds owned copies once, and extra columns become custom fields.
func TestCLZImportAddsCopiesOnce(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": fakeTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	store := imports.Store{Pool: f.Pool, Catalog: cat, Library: library.Store{Pool: f.Pool}}
	member := f.User(t, "clz_member", "member")
	data := []byte("Index,Title,Release Year,IMDb Number,Format,Case Type\n7,Synthetic Film,2001,tt42,DVD,Slipcover\n")
	run := func() imports.Run {
		t.Helper()
		r, err := store.Start(ctx, member, "clz", "movies.csv", data, "")
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]string{"run_id": r.ID})
		if err = store.Handler(ctx, payload); err != nil {
			t.Fatal(err)
		}
		r, _ = store.Get(ctx, member, r.ID)
		return r
	}
	if first := run(); first.Imported != 1 {
		t.Fatalf("first %+v", first)
	}
	run()
	var copies int
	var custom string
	_ = f.Pool.QueryRow(ctx, "SELECT count(*),max(custom->>'Case Type') FROM owned_copies WHERE user_id=$1 AND source='clz'", member.User.ID).Scan(&copies, &custom)
	if copies != 1 || custom != "Slipcover" {
		t.Fatal("copies", copies, custom)
	}
	var fields int
	_ = f.Pool.QueryRow(ctx, "SELECT count(*) FROM collection_fields WHERE user_id=$1 AND name='Case Type'", member.User.ID).Scan(&fields)
	if fields != 1 {
		t.Fatal("custom field not created")
	}
}

func TestNativeV2GzipRestoreThroughImportWorker(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	if _, err := lib.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: "Manual gzip round trip", MediaType: "book", Unit: "pages", Status: "planned", Notes: "Native note"}); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	if _, err := exports.Write(ctx, lib, f.Owner, "jsonl", zip); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	target := f.User(t, "gzip_target", "member")
	s := imports.Store{Pool: f.Pool, Library: lib}
	run, err := s.Start(ctx, target, "native", "snapshot.jsonl.gz", compressed.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": run.ID})
	if err = s.Handler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, target, run.ID)
	if err != nil || got.Status != "succeeded" || got.Imported != 1 {
		t.Fatal(got, err)
	}
	var notes string
	if err = f.Pool.QueryRow(ctx, "SELECT notes FROM library_entries WHERE user_id=$1", target.User.ID).Scan(&notes); err != nil || notes != "Native note" {
		t.Fatal(notes, err)
	}
}
