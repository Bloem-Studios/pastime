package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestPersonalMembershipAPIAddsRemovesAndIsolatesProfiles(t *testing.T) {
	h := application(t)
	var title int64
	if err := h.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(kind,provider,provider_id,title,unit) VALUES('movie','tmdb','42','Synthetic film','minutes') RETURNING id`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/catalog/" + strconv.FormatInt(title, 10) + "/personal-state"
	session := h.request("GET", "/api/v1/session", nil, h.OwnerRaw, "", "", "")
	var auth struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	key := testkit.Key(t, h.Fixture)
	in := map[string]any{"kind": "favorite", "present": true}
	for i := 0; i < 2; i++ {
		requireCode(t, h.request("PUT", path, in, h.OwnerRaw, auth.CSRF, "http://127.0.0.1:8080", key), http.StatusNoContent)
	}
	requireCode(t, h.request("PUT", path, map[string]any{"kind": "favorite", "present": false}, h.OwnerRaw, auth.CSRF, "http://127.0.0.1:8080", key), 409)
	var entries int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM library_entries WHERE profile_id=$1`, h.Owner.TrackingID()).Scan(&entries); err != nil || entries != 0 {
		t.Fatal("favorite enrolled unrelated tracking", entries, err)
	}
	requireCode(t, h.request("PUT", path, map[string]any{"kind": "watchlist", "present": true}, h.OwnerRaw, auth.CSRF, "http://127.0.0.1:8080", testkit.Key(t, h.Fixture)), 204)
	requireCode(t, h.request("PUT", path, map[string]any{"kind": "favorite", "present": false}, h.OwnerRaw, auth.CSRF, "http://127.0.0.1:8080", testkit.Key(t, h.Fixture)), 204)
	read := h.request("GET", path, nil, h.OwnerRaw, "", "", "")
	requireCode(t, read, 200)
	var state struct{ Favourite, Watchlist bool }
	if err := json.Unmarshal(read.Body.Bytes(), &state); err != nil || state.Favourite || !state.Watchlist {
		t.Fatal("membership change missing", state, err)
	}
	h.User(t, "other_personal_viewer", "member")
	_, other := h.Login(t, "other_personal_viewer")
	read = h.request("GET", path, nil, other, "", "", "")
	requireCode(t, read, 200)
	if err := json.Unmarshal(read.Body.Bytes(), &state); err != nil || state.Favourite || state.Watchlist {
		t.Fatal("membership crossed profile", state, err)
	}
	requireCode(t, h.request("PUT", path, map[string]any{"kind": "favorite", "present": true}, h.OwnerRaw, "", "http://127.0.0.1:8080", testkit.Key(t, h.Fixture)), 403)
}
