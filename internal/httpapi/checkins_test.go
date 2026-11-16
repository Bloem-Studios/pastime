package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestCheckinRoutes(t *testing.T) {
	f := testkit.New(t)
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{f, s, t}
	call := func(method, path string, body any, idempotent bool) (int, string) {
		t.Helper()
		key := ""
		if idempotent {
			key = testkit.Key(t, f)
		}
		rec := h.request(method, path, body, f.OwnerRaw, f.Owner.CSRF, "http://127.0.0.1:8080", key)
		return rec.Code, rec.Body.String()
	}
	var board int64
	if err = f.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'boardgame','bgg','13','Catan','items') RETURNING id`).Scan(&board); err != nil {
		t.Fatal(err)
	}
	code, body := call("POST", "/api/v1/checkins", map[string]any{"catalog_id": strconv.FormatInt(board, 10)}, true)
	if code != 201 {
		t.Fatal(code, body)
	}
	var started library.Checkin
	if err = json.Unmarshal([]byte(body), &started); err != nil || started.Status != "active" || started.Kind != "play" || started.Title != "Catan" || started.MediaKind != "boardgame" {
		t.Fatalf("started %s %v", body, err)
	}
	code, body = call("GET", "/api/v1/checkins", nil, false)
	var page checkinPage
	if code != 200 || json.Unmarshal([]byte(body), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != started.ID {
		t.Fatal(code, body)
	}
	if code, body = call("POST", "/api/v1/checkins", map[string]any{"catalog_id": strconv.FormatInt(board, 10)}, true); code != 409 {
		t.Fatal("second check-in", code, body)
	}
	path := "/api/v1/checkins/" + strconv.FormatInt(started.ID, 10)
	code, body = call("POST", path+"/finish", map[string]any{"minutes": 75, "players": []map[string]any{{"name": "Ann", "score": 10, "winner": true}, {"name": "Ben", "score": 7}}}, true)
	var finished library.Checkin
	if code != 200 || json.Unmarshal([]byte(body), &finished) != nil || finished.Status != "finished" || *finished.Minutes != 75 || finished.Details["players"] == nil {
		t.Fatal(code, body)
	}
	if code, body = call("POST", path+"/finish", map[string]any{}, true); code != 404 {
		t.Fatal("finished twice", code, body)
	}
	if code, body = call("POST", path+"/cancel", nil, false); code == 200 {
		t.Fatal("cancel without an idempotency key", code, body)
	}
	code, body = call("GET", path, nil, false)
	if code != 200 || json.Unmarshal([]byte(body), &finished) != nil || finished.Status != "finished" {
		t.Fatal(code, body)
	}
	if code, body = call("GET", "/api/v1/checkins", nil, false); code != 200 || json.Unmarshal([]byte(body), &page) != nil || len(page.Items) != 0 {
		t.Fatal("finished check-in still listed", code, body)
	}
}
