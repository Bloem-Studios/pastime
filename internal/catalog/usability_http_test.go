package catalog_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/discover"
	"github.com/Bloem-Studios/pastime/internal/httpapi"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestUsabilityHTTPMembershipAndDismissalContracts(t *testing.T) {
	f, cat, tv := setup(t)
	ctx := context.Background()
	cat.Providers["faketv"] = &cachedUsabilityProvider{fakeTV: tv}
	item, err := cat.Resolve(ctx, "faketv", "movie", "100")
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := (library.Store{Pool: f.Pool}).Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.User(t, "http_member", "member")
	_, memberRaw := f.Login(t, "http_member")
	server, err := httpapi.New(func(context.Context) error { return nil }, nil, "synthetic", httpapi.Options{Pool: f.Pool, Catalog: cat, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, route, raw string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:51234"
		if raw != "" {
			req.AddCookie(&http.Cookie{Name: "pastime_session", Value: raw})
		}
		rec := httptest.NewRecorder()
		server.Router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		response := server.Spec.Paths.Value(route).Get.Responses.Value(strconv.Itoa(want))
		if response == nil {
			t.Fatal("undocumented status")
		}
		if err := response.Value.Content["application/json"].Schema.Value.VisitJSON(out); err != nil {
			t.Fatalf("GET %s contract: %v", path, err)
		}
		return out
	}
	route := "/api/v1/catalog/search"
	get(route+"?media_type=movie&q=Synthetic", route, "", 401)
	mine := get(route+"?media_type=movie&q=Synthetic", route, f.OwnerRaw, 200)
	hit := mine["items"].([]any)[0].(map[string]any)
	if hit["library_entry_id"] != strconv.FormatInt(tracked.ID, 10) || hit["library_status"] != "planned" {
		t.Fatalf("wrong membership %v", hit)
	}
	other := get(route+"?media_type=movie&q=Synthetic&user_id="+strconv.FormatInt(f.Owner.User.ID, 10), route, memberRaw, 200)
	hit = other["items"].([]any)[0].(map[string]any)
	if hit["library_entry_id"] != nil || hit["library_status"] != nil || hit["catalog_id"] == nil {
		t.Fatalf("private membership disclosed %v", hit)
	}
	get("/api/v1/catalog/providers", "/api/v1/catalog/providers", f.OwnerRaw, 200)
	browse := get("/api/v1/catalog/browse?media_type=movie", "/api/v1/catalog/browse", f.OwnerRaw, 200)
	if browse["availability"] != "search_only" {
		t.Fatalf("fake browse success %v", browse)
	}
	ds := discover.Store{Pool: f.Pool}
	if err := ds.Dismiss(ctx, f.Owner, "faketv", "movie", "200", false); err != nil {
		t.Fatal(err)
	}
	hidden := get("/api/v1/discover/dismissals?media_type=movie", "/api/v1/discover/dismissals", f.OwnerRaw, 200)
	if len(hidden["items"].([]any)) != 1 {
		t.Fatal("dismissal missing")
	}
	hidden = get("/api/v1/discover/dismissals?media_type=movie", "/api/v1/discover/dismissals", memberRaw, 200)
	if len(hidden["items"].([]any)) != 0 {
		t.Fatal("another caller's dismissals disclosed")
	}
}
