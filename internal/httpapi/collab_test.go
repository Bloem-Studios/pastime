package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestCollaborativeLists(t *testing.T) {
	f := testkit.New(t)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": syntheticTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{f, s, t}
	call := func(method, path string, body any) (int, string) {
		t.Helper()
		rec := h.request(method, path, body, f.OwnerRaw, f.Owner.CSRF, "http://127.0.0.1:8080", "")
		return rec.Code, rec.Body.String()
	}
	ctx := context.Background()
	film, _ := cat.Resolve(ctx, "tmdb", "movie", "42")
	show, _ := cat.Resolve(ctx, "tmdb", "series", "100")
	editor := f.User(t, "collab_editor", "member")
	viewer := f.User(t, "collab_viewer", "member")
	outsider := f.User(t, "collab_outsider", "member")
	code, body := call("POST", "/api/v1/collab-lists", map[string]string{"name": "Movie night", "description": "Picks for Fridays"})
	if code != 201 {
		t.Fatal(code, body)
	}
	var list struct{ ID string }
	_ = json.Unmarshal([]byte(body), &list)
	base := "/api/v1/collab-lists/" + list.ID
	for _, m := range []map[string]string{{"username": "collab_editor", "role": "editor"}, {"username": "collab_viewer", "role": "viewer"}} {
		if code, body = call("PUT", base+"/members", m); code != 200 {
			t.Fatal(code, body)
		}
	}
	if code, _ = call("PUT", base+"/members", map[string]string{"username": "nobody_here", "role": "editor"}); code != 422 {
		t.Fatal("unknown member added", code)
	}
	if code, body = call("POST", base+"/items", map[string]string{"catalog_id": itoa(film.ID), "note": "A classic"}); code != 200 || !strings.Contains(body, "A classic") {
		t.Fatal(code, body)
	}
	lib := library.Store{Pool: f.Pool}
	id := mustID(t, list.ID)
	if err := lib.AddCollabItem(ctx, editor, id, show.ID, ""); err != nil {
		t.Fatal("editor could not add", err)
	}
	var fe *fault.Error
	if err := lib.AddCollabItem(ctx, viewer, id, show.ID, ""); !asFault(err, &fe) || fe.Status != 403 {
		t.Fatal("viewer added a title", err)
	}
	if _, err := lib.Collab(ctx, outsider, id); !asFault(err, &fe) || fe.Status != 404 {
		t.Fatal("outsider saw the list", err)
	}
	seen, err := lib.Collab(ctx, viewer, id)
	if err != nil || len(seen.ItemList) != 2 || len(seen.MemberList) != 3 || seen.Role != "viewer" || seen.ItemList[1].AddedBy.Username != "collab_editor" {
		t.Fatalf("viewer view %+v %v", seen, err)
	}
	if code, body = call("PUT", base+"/order", map[string]any{"catalog_ids": []string{itoa(show.ID)}}); code != 200 || strings.Index(body, "Synthetic Harbour") > strings.Index(body, "Synthetic Film") {
		t.Fatal("order", code, body)
	}
	if err := lib.RemoveCollabMember(ctx, editor, id, "collab_viewer"); !asFault(err, &fe) || fe.Status != 403 {
		t.Fatal("editor removed someone", err)
	}
	if err := lib.RemoveCollabMember(ctx, viewer, id, "collab_viewer"); err != nil {
		t.Fatal("viewer could not leave", err)
	}
	if err := lib.DeleteCollab(ctx, editor, id); !asFault(err, &fe) || fe.Status != 403 {
		t.Fatal("editor deleted the list", err)
	}
	if code, _ = call("DELETE", base+"/items/"+itoa(film.ID), nil); code != 204 {
		t.Fatal("remove item", code)
	}
	if code, body = call("GET", "/api/v1/collab-lists", nil); code != 200 || !strings.Contains(body, `"members":2`) || !strings.Contains(body, `"items":1`) {
		t.Fatal(code, body)
	}
	if code, _ = call("DELETE", base, nil); code != 204 {
		t.Fatal("delete", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func mustID(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func asFault(err error, target **fault.Error) bool { return errors.As(err, target) }
