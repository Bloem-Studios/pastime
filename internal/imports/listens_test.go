package imports_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

type fakeMusic struct{}

func (fakeMusic) Name() string    { return "musicbrainz" }
func (fakeMusic) Kinds() []string { return []string{"music"} }
func (fakeMusic) Search(context.Context, string, string) ([]provider.Record, error) {
	return nil, nil
}
func (fakeMusic) Fetch(_ context.Context, kind, id string) (provider.Record, error) {
	if id != "11111111-1111-4111-8111-111111111111" {
		return provider.Record{}, provider.ErrNotFound
	}
	track := func(disc, number int, rec, title string) provider.Record {
		return provider.Record{Provider: "musicbrainz", Kind: "track", ProviderID: rec, Title: title, SeasonNumber: n(disc), EpisodeNumber: n(number)}
	}
	return provider.Record{Provider: "musicbrainz", Kind: "music", ProviderID: id, Title: "First Album", Credits: []string{"Band"}, Total: n(2), ChildrenLoaded: true,
		Children: []provider.Record{track(1, 1, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "Song A"), track(1, 2, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "Song B")}}, nil
}

func TestSubmittedListensBecomeAlbumPlaysOnce(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"musicbrainz": fakeMusic{}}, Defaults: map[string]string{"music": "musicbrainz"}}
	lib := library.Store{Pool: f.Pool}
	store := imports.Store{Pool: f.Pool, Catalog: cat, Library: lib}
	member := f.User(t, "listen_member", "member")
	body := []byte(`{"listen_type":"import","payload":[
		{"listened_at":1767384000,"track_metadata":{"artist_name":"Band","track_name":"Song A","release_name":"First Album","additional_info":{"release_group_mbid":"11111111-1111-4111-8111-111111111111","recording_mbid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}},
		{"listened_at":1767384300,"track_metadata":{"artist_name":"Band","track_name":"Song B","release_name":"First Album","additional_info":{"release_group_mbid":"11111111-1111-4111-8111-111111111111"}}},
		{"listened_at":1767384600,"track_metadata":{"artist_name":"Nobody","track_name":"Loose Single"}}]}`)
	listens, err := imports.ParseListens(body, time.Now())
	if err != nil || len(listens) != 3 {
		t.Fatal(listens, err)
	}
	run := func() {
		t.Helper()
		if err := store.QueueListens(ctx, member.User.ID, listens); err != nil {
			t.Fatal(err)
		}
		var payload json.RawMessage
		_ = f.Pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE kind=$1 ORDER BY created_at DESC LIMIT 1", imports.ListensJob).Scan(&payload)
		if err := store.ListensHandler(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	run()
	_, _ = f.Pool.Exec(ctx, "DELETE FROM jobs WHERE kind=$1", imports.ListensJob)
	run() // Submitting the same listens again records nothing new.
	history, _ := lib.History(ctx, member, library.HistoryFilter{})
	if len(history.Items) != 2 || history.Items[0].Source != "listenbrainz" {
		t.Fatalf("history %+v", history.Items)
	}
	// The second track resolves by its exact title, so both distinct tracks are played.
	entries, _ := lib.Entries(ctx, member, library.Filter{Limit: 5})
	if len(entries.Items) != 1 || entries.Items[0].Progress != 2 || entries.Items[0].Status != "completed" || *entries.Items[0].Total != 2 {
		t.Fatalf("album %+v", entries.Items)
	}
}
