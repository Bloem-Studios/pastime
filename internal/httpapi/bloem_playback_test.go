package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// numericTMDB is syntheticTMDB with numeric episode ids 9001-9003, as Bloem sends them.
type numericTMDB struct{ syntheticTMDB }

func (t numericTMDB) FetchChild(ctx context.Context, parent, child provider.Record) (provider.Record, error) {
	out, err := t.syntheticTMDB.FetchChild(ctx, parent, child)
	for i := range out.Children {
		out.Children[i].ProviderID = "900" + strconv.Itoa(i+1)
	}
	return out, err
}

func TestNativePlaybackAndSnapshotHTTP(t *testing.T) {
	h := application(t)
	ctx := t.Context()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	h.server.secrets = box
	h.server.catalog = &catalog.Service{Pool: h.Pool, Providers: map[string]provider.Provider{"tmdb": numericTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s := bloem.Store{Pool: h.Pool, Box: box}
	i, raw, err := s.Register(ctx, h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	p := bloem.ProvisionInput{Version: 1, InstanceID: i.ID, AccountID: "house", AccountName: "House", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, Active: true, AccountActive: true}
	m, err := s.Provision(ctx, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.IssueCredential(ctx, raw, m.ProfileID, "http")
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, token string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		return w
	}
	inspect := "/api/v1/integrations/bloem/credentials:inspect"
	requireCode(t, call(inspect, c.Credential, map[string]any{}), 200)
	requireCode(t, call(inspect, raw, map[string]any{}), 401)
	event := map[string]any{"version": 1, "instance_id": i.ID, "account_id": "house", "profile_id": "main", "entity_id": "movie-42", "event_id": "e1", "revision": 1, "operation": "stop", "occurred_at": time.Now().Add(-time.Minute), "completed": true, "consumption_id": "view1", "position_seconds": 90, "duration_seconds": 100, "media": map[string]any{"kind": "movie", "external_ids": map[string]string{"tmdb": "42"}}}
	events := "/api/v1/integrations/bloem/events:apply"
	requireCode(t, call(events, raw, event), 401)
	requireCode(t, call(events, c.Credential, event), 200)
	requireCode(t, call(events, c.Credential, event), 200)
	var plays int
	if err = h.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_consumption_receipts WHERE profile_id=$1`, m.ProfileID).Scan(&plays); err != nil || plays != 1 {
		t.Fatal("replay duplicated viewing", plays, err)
	}
	event["event_id"] = "forged"
	event["profile_id"] = "another"
	requireCode(t, call(events, c.Credential, event), 403)
	event["profile_id"] = "main"
	event["media"] = map[string]any{"kind": "movie", "external_ids": map[string]string{}, "title": "Synthetic Film"}
	requireCode(t, call(events, c.Credential, event), 422)
	// An episode TMDB id at other numbers than the source's is settled by the episode's
	// title or air date; without them it is refused.
	episode := map[string]any{"kind": "episode", "external_ids": map[string]string{"tmdb": "9003"}, "series_external_ids": map[string]string{"tmdb": "100"}, "season": 1, "episode": 2}
	event["entity_id"], event["event_id"], event["consumption_id"], event["media"] = "episode-2", "ep1", "view-ep", episode
	requireCode(t, call(events, c.Credential, event), 404)
	episode["air_date"] = "2026-8-27"
	if invalid := call(events, c.Credential, event); invalid.Code != 422 || !bytes.Contains(invalid.Body.Bytes(), []byte("air date")) {
		t.Fatal("a malformed air date was not refused as such", invalid.Code, invalid.Body.String())
	}
	delete(episode, "air_date")
	episode["title"] = "Episode 2"
	requireCode(t, call(events, c.Credential, event), 200)
	var target string
	if err = h.Pool.QueryRow(ctx, `SELECT ci.provider_id FROM bloem_entity_states s JOIN catalog_items ci ON ci.id=s.target_catalog_id WHERE s.profile_id=$1 AND s.entity_id='episode-2'`, m.ProfileID).Scan(&target); err != nil || target != "9002" {
		t.Fatal("the episode title did not settle the match", target, err)
	}
	page := bloem.SnapshotPage{SnapshotID: "http", Complete: true, Profiles: []bloem.ProvisionInput{p}}
	w := call("/api/v1/integrations/bloem/snapshots:page", raw, page)
	requireCode(t, w, 200)
	var receipt bloem.PageReceipt
	if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	completed := call("/api/v1/integrations/bloem/snapshots:complete", raw, map[string]string{"snapshot_id": "http", "receipt": receipt.Receipt})
	requireCode(t, completed, 200)
	var completion struct {
		Completed bool `json:"completed"`
	}
	if json.Unmarshal(completed.Body.Bytes(), &completion) != nil || !completion.Completed {
		t.Fatal("completion response lacks confirmed result")
	}
}
