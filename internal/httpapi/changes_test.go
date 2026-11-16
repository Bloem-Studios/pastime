package httpapi

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestChangeFeed(t *testing.T) {
	h := application(t)
	call := func(method, path string, body any) (int, string) {
		t.Helper()
		key := ""
		if method != "GET" {
			key = testkit.Key(t, h.Fixture)
		}
		rec := h.request(method, path, body, h.OwnerRaw, h.Owner.CSRF, "http://127.0.0.1:8080", key)
		return rec.Code, rec.Body.String()
	}
	type page struct {
		Items []struct {
			Entity   string
			EntityID string `json:"entity_id"`
			Op       string
			Entry    *struct{ Title string }
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	read := func(cursor string) page {
		t.Helper()
		code, body := call("GET", "/api/v1/changes?limit=2&cursor="+cursor, nil)
		if code != 200 {
			t.Fatal(code, body)
		}
		var p page
		_ = json.Unmarshal([]byte(body), &p)
		return p
	}
	start := read("")
	if len(start.Items) != 0 || start.NextCursor == "" {
		t.Fatalf("start %+v", start)
	}
	_, body := call("POST", "/api/v1/library", map[string]any{"title": "Feed Novel", "media_type": "book", "unit": "pages", "total": 100})
	var made struct {
		ID      string
		Version int
	}
	_ = json.Unmarshal([]byte(body), &made)
	if code, body := call("POST", "/api/v1/library/"+made.ID+"/progress", map[string]any{"version": made.Version, "progress": 10, "status": "in_progress"}); code != 200 {
		t.Fatal(code, body)
	}
	first := read(start.NextCursor)
	if len(first.Items) != 2 || !first.HasMore || first.Items[0].Entry == nil || first.Items[0].Entry.Title != "Feed Novel" {
		t.Fatalf("first %+v", first)
	}
	var drained page
	for cursor := first.NextCursor; ; {
		drained = read(cursor)
		cursor = drained.NextCursor
		if !drained.HasMore {
			break
		}
	}
	_, body = call("GET", "/api/v1/library/"+made.ID, nil)
	_ = json.Unmarshal([]byte(body), &made)
	if code, body := call("DELETE", "/api/v1/library/"+made.ID, map[string]any{"version": made.Version}); code != 200 {
		t.Fatal(code, body)
	}
	gone := read(drained.NextCursor)
	if len(gone.Items) == 0 || gone.Items[len(gone.Items)-1].Op != "delete" || gone.Items[len(gone.Items)-1].EntityID != made.ID {
		t.Fatalf("delete %+v", gone)
	}
	stale := strconv.FormatInt(1, 36) + "." + strconv.FormatInt(time.Now().Add(-100*24*time.Hour).Unix(), 36)
	if code, body := call("GET", "/api/v1/changes?cursor="+stale, nil); code != 410 || !strings.Contains(body, "cursor_expired") {
		t.Fatal(code, body)
	}
}
