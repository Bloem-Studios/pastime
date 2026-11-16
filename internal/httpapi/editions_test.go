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

type fakeBooks struct{}

func (fakeBooks) Name() string    { return "openlibrary" }
func (fakeBooks) Kinds() []string { return []string{"book", "audiobook"} }
func (fakeBooks) Search(context.Context, string, string) ([]provider.Record, error) {
	return nil, nil
}
func (fakeBooks) Fetch(_ context.Context, kind, id string) (provider.Record, error) {
	return provider.Record{Provider: "openlibrary", Kind: kind, ProviderID: id, Title: "Synthetic Atlas", Total: iptr(320), ImageURL: "https://covers.example/work.jpg"}, nil
}
func (fakeBooks) Editions(context.Context, string, string) ([]provider.Edition, error) {
	return []provider.Edition{{ID: "OL1M", Title: "Synthetic Atlas", Publisher: "Paper House", Year: "2001", Pages: iptr(250), ISBN: "9780000000001", Format: "Paperback", ImageURL: "https://covers.example/1.jpg"},
		{ID: "OL2M", Title: "Synthetic Atlas (Large Print)", Pages: iptr(600)}}, nil
}

func TestEditions(t *testing.T) {
	f := testkit.New(t)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"openlibrary": fakeBooks{}}, Defaults: map[string]string{"book": "openlibrary"}}
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
	_, body := call("POST", "/api/v1/catalog/resolve", map[string]string{"provider": "openlibrary", "media_type": "book", "provider_id": "OL1W"})
	var item struct{ ID string }
	_ = json.Unmarshal([]byte(body), &item)
	if code, body := call("GET", "/api/v1/catalog/"+item.ID+"/editions", nil); code != 200 || !strings.Contains(body, "Paper House") {
		t.Fatal(code, body)
	}
	_, body = call("POST", "/api/v1/library/track", map[string]any{"catalog_id": item.ID, "status": "in_progress"})
	var tracked struct {
		ID      string
		Version int
	}
	_ = json.Unmarshal([]byte(body), &tracked)
	entry := func() (int, int, string, string) {
		var total, progress int
		var image, edition string
		_ = f.Pool.QueryRow(context.Background(), "SELECT total,progress,image_override,COALESCE(edition->>'id','') FROM library_entries WHERE id=$1", tracked.ID).Scan(&total, &progress, &image, &edition)
		return total, progress, image, edition
	}
	_, _ = f.Pool.Exec(context.Background(), "UPDATE library_entries SET progress=300 WHERE id=$1", tracked.ID)
	if code, body := call("PUT", "/api/v1/library/"+tracked.ID+"/edition", map[string]any{"version": tracked.Version, "edition_id": "OL9M"}); code != 422 {
		t.Fatal("unknown edition accepted", code, body)
	}
	code, body := call("PUT", "/api/v1/library/"+tracked.ID+"/edition", map[string]any{"version": tracked.Version, "edition_id": "OL1M"})
	if code != 200 {
		t.Fatal(code, body)
	}
	if total, progress, image, ed := entry(); total != 250 || progress != 250 || image != "https://covers.example/1.jpg" || ed != "OL1M" {
		t.Fatal("edition applied", total, progress, image, ed)
	}
	if code, body = call("GET", "/api/v1/library/"+tracked.ID, nil); code != 200 || !strings.Contains(body, `"publisher":"Paper House"`) {
		t.Fatal(code, body)
	}
	var m struct{ Version int }
	_ = json.Unmarshal([]byte(body), &m)
	if code, body = call("PUT", "/api/v1/library/"+tracked.ID+"/edition", map[string]any{"version": m.Version, "edition_id": nil}); code != 200 {
		t.Fatal(code, body)
	}
	if total, _, image, ed := entry(); total != 320 || image != "" || ed != "" {
		t.Fatal("edition cleared", total, image, ed)
	}
}
