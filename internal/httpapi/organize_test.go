package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestOrganizationResponseContracts(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		key := ""
		if method != "GET" {
			key = testkit.Key(t, h.Fixture)
		}
		return h.request(method, path, body, h.OwnerRaw, h.Owner.CSRF, origin, key)
	}
	idOf := func(r *httptest.ResponseRecorder) string {
		var v struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &v)
		return v.ID
	}
	entry := call("POST", "/api/v1/library", map[string]any{"title": "Contract Shelf", "media_type": "movie", "unit": "minutes", "total": 90, "rating": 8})
	requireCode(t, entry, 201)
	e := idOf(entry)
	tag := call("POST", "/api/v1/tags", map[string]any{"name": "Rewatch", "color": "#169e88"})
	requireCode(t, tag, 201)
	tg := idOf(tag)
	requireCode(t, call("PUT", "/api/v1/tags/"+tg, map[string]any{"name": "Rewatch soon"}), 200)
	requireCode(t, call("PUT", "/api/v1/library/"+e+"/tags", map[string]any{"tag_ids": []string{tg}}), 200)
	requireCode(t, call("POST", "/api/v1/library/bulk", map[string]any{"entry_ids": []string{e}, "action": "set_status", "status": "rated"}), 200)
	requireCode(t, call("POST", "/api/v1/library/bulk", map[string]any{"entry_ids": []string{e}, "action": "explode"}), 422)
	field := call("POST", "/api/v1/collection-fields", map[string]any{"name": "Region", "kind": "choice", "choices": []string{"A", "B", "C"}})
	requireCode(t, field, 201)
	copyResp := call("POST", "/api/v1/library/"+e+"/copies", map[string]any{"format": "uhd_bluray", "resolution": "2160p", "hdr": "dolby_vision", "custom": map[string]any{idOf(field): "B"}})
	requireCode(t, copyResp, 201)
	requireCode(t, call("PUT", "/api/v1/library/"+e+"/copies/"+idOf(copyResp), map[string]any{"format": "uhd_bluray", "edition": "Steelbook", "version": 1}), 200)
	requireCode(t, call("POST", "/api/v1/collection-fields", map[string]any{"name": "Shelf", "kind": "text"}), 201)
	for _, p := range []string{"/api/v1/tags", "/api/v1/library/" + e + "/tags", "/api/v1/library/" + e + "/copies", "/api/v1/collection", "/api/v1/collection?format=dvd",
		"/api/v1/collection-fields", "/api/v1/library/" + e + "/skipped", "/api/v1/me/preferences",
		"/api/v1/library?sort=title&status=rated,completed&tag_id=" + tg + "&owned=true&min_rating=5"} {
		requireCode(t, call("GET", p, nil), 200)
	}
	prefs := call("GET", "/api/v1/me/preferences", nil)
	var p map[string]any
	_ = json.Unmarshal(prefs.Body.Bytes(), &p)
	p["library_view"], p["public_profile"] = "list", true
	requireCode(t, call("PUT", "/api/v1/me/preferences", p), 200)

	list := call("POST", "/api/v1/lists", map[string]any{"name": "Shared shelf", "visibility": "public", "slug": "shared-shelf"})
	requireCode(t, list, 201)
	l := idOf(list)
	requireCode(t, call("PUT", "/api/v1/lists/"+l+"/entries/"+e, map[string]any{"version": 1, "present": true}), 200)
	requireCode(t, call("PUT", "/api/v1/lists/"+l+"/order", map[string]any{"version": 2, "entry_ids": []string{e}}), 200)
	requireCode(t, call("PUT", "/api/v1/lists/"+l+"/entries/"+e+"/note", map[string]any{"version": 3, "note": "=HYPERLINK(\"x\")"}), 200)
	requireCode(t, call("POST", "/api/v1/lists", map[string]any{"name": "Films", "kind": "smart", "rules": map[string]any{"media_types": []string{"movie"}}}), 201)
	requireCode(t, call("GET", "/api/v1/lists", nil), 200)

	// Shared lists are readable without credentials; private notes and ratings are not.
	anon := func(path string) *httptest.ResponseRecorder { return h.request("GET", path, nil, "", "", "", "") }
	public := anon("/api/v1/public/" + h.Owner.User.Username + "/lists/shared-shelf")
	requireCode(t, public, 200)
	if strings.Contains(public.Body.String(), "rating") {
		t.Fatal("public list exposed a rating")
	}
	rss := anon("/lists/" + h.Owner.User.Username + "/shared-shelf.rss")
	requireCode(t, rss, 200)
	if !strings.Contains(rss.Body.String(), "<title>Contract Shelf</title>") {
		t.Fatalf("rss: %s", rss.Body.String())
	}
	csv := anon("/lists/" + h.Owner.User.Username + "/shared-shelf.csv")
	requireCode(t, csv, 200)
	if !strings.Contains(csv.Body.String(), `'=HYPERLINK`) {
		t.Fatalf("csv formula not neutralized: %s", csv.Body.String())
	}
	requireCode(t, anon("/api/v1/public/"+h.Owner.User.Username+"/lists"), 200) // opted in through preferences above
	requireCode(t, anon("/api/v1/public/"+h.Owner.User.Username+"/lists/missing"), 404)

	requireCode(t, call("DELETE", "/api/v1/library/"+e+"/copies/"+idOf(copyResp), nil), 200)
	requireCode(t, call("DELETE", "/api/v1/tags/"+tg, nil), 200)
}
