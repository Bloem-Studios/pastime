package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/exports"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestAccountsAndOperationsContracts(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		key := ""
		if method != "GET" && method != "DELETE" {
			key = testkit.Key(t, h.Fixture)
		}
		return h.request(method, path, body, h.OwnerRaw, h.Owner.CSRF, origin, key)
	}
	entry := call("POST", "/api/v1/library", map[string]any{"title": "Snapshot Atlas", "media_type": "book", "unit": "pages", "total": 10, "notes": "=cmd|' /C calc'!A0"})
	requireCode(t, entry, 201)
	var ack struct{ ID string }
	_ = json.Unmarshal(entry.Body.Bytes(), &ack)

	// Snapshots and schedules.
	snap := call("POST", "/api/v1/export-files", map[string]string{"format": "csv"})
	requireCode(t, snap, 201)
	var file struct{ ID string }
	_ = json.Unmarshal(snap.Body.Bytes(), &file)
	dl := call("GET", "/api/v1/export-files/"+file.ID, nil)
	requireCode(t, dl, 200)
	if !strings.Contains(dl.Body.String(), "Snapshot Atlas") || !strings.Contains(dl.Body.String(), "'=cmd") {
		t.Fatalf("snapshot content: %s", dl.Body.String())
	}
	requireCode(t, call("GET", "/api/v1/export-files", nil), 200)
	requireCode(t, call("GET", "/api/v1/me/export-schedule", nil), 200)
	requireCode(t, call("PUT", "/api/v1/me/export-schedule", map[string]any{"frequency": "weekly", "format": "jsonl", "keep": 2}), 200)
	store := exports.Store{Pool: h.Pool, Library: library.Store{Pool: h.Pool}}
	var due []string
	n, err := store.EnqueueDue(context.Background(), func(ctx context.Context, key string, payload []byte) error {
		due = append(due, key)
		return store.Handler(ctx, payload)
	})
	if err != nil || n != 1 {
		t.Fatalf("due schedules %d %v", n, err)
	}
	files, _ := store.Files(context.Background(), h.Owner)
	if len(files) != 2 || files[0].Trigger != "schedule" {
		t.Fatalf("scheduled snapshot: %+v", files)
	}
	var nextRun time.Time
	_ = h.Pool.QueryRow(context.Background(), "SELECT next_run_at FROM export_schedules WHERE user_id=$1", h.Owner.User.ID).Scan(&nextRun)
	if time.Until(nextRun) < 6*24*time.Hour {
		t.Fatalf("schedule not advanced: %v", nextRun)
	}
	requireCode(t, call("DELETE", "/api/v1/export-files/"+file.ID, nil), 204)
	requireCode(t, call("DELETE", "/api/v1/me/export-schedule", nil), 204)

	// Without SECRET_KEY, features that store secrets say so instead of failing oddly.
	requireCode(t, call("GET", "/api/v1/me/notification-targets", nil), 200)
	missing := call("POST", "/api/v1/me/notification-targets", map[string]any{"kind": "ntfy", "name": "Phone", "config": map[string]string{"topic": "floppy"}})
	requireCode(t, missing, 503)
	if !strings.Contains(missing.Body.String(), "secret_key_missing") {
		t.Fatal(missing.Body.String())
	}
	requireCode(t, call("POST", "/api/v1/me/totp", map[string]string{"password": testkit.Password}), 503)
	requireCode(t, call("GET", "/api/v1/me/notification-settings", nil), 200)
	requireCode(t, call("PUT", "/api/v1/me/notification-settings", map[string]any{"releases": true, "days_ahead": 1, "media_types": []string{}, "statuses": []string{"planned"}, "digest": true, "version": 0}), 200)
	requireCode(t, call("PUT", "/api/v1/library/"+ack.ID+"/notifications", map[string]bool{"excluded": true}), 200)
	requireCode(t, call("GET", "/api/v1/library/"+ack.ID+"/notifications", nil), 200)
	requireCode(t, call("GET", "/api/v1/auth/methods", nil), 200)
	requireCode(t, call("GET", "/api/v1/me/totp", nil), 200)
	requireCode(t, call("GET", "/api/v1/me/oidc", nil), 200)
	requireCode(t, call("GET", "/api/v1/import-unmatched", nil), 200)
	requireCode(t, call("PUT", "/api/v1/library/"+ack.ID+"/artwork", map[string]any{"version": 1, "image_url": "https://images.example/a.jpg"}), 200)
	requireCode(t, call("POST", "/api/v1/imports/00000000-0000-4000-8000-000000000000/rollback", nil), 404)

	// With a key, a destination can be saved; its secret is never returned.
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	keyed, err := New(ready, nil, "test", Options{Pool: h.Pool, PublicOrigin: origin, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Secrets: box})
	if err != nil {
		t.Fatal(err)
	}
	hk := &harness{h.Fixture, keyed, t}
	saved := hk.request("POST", "/api/v1/me/notification-targets", map[string]any{"kind": "ntfy", "name": "Phone", "config": map[string]string{"topic": "floppy", "token": "tk_secret"}}, h.OwnerRaw, h.Owner.CSRF, origin, "")
	requireCode(t, saved, 201)
	if strings.Contains(saved.Body.String(), "tk_secret") {
		t.Fatal("token returned")
	}
	requireCode(t, hk.request("POST", "/api/v1/me/totp", map[string]string{"password": testkit.Password}, h.OwnerRaw, h.Owner.CSRF, origin, ""), 201)
	requireCode(t, hk.request("POST", "/api/v1/password-recovery", map[string]string{"username": "nobody", "code": "123456", "password": "a-long-synthetic-password"}, "", "", origin, ""), 401)
}
