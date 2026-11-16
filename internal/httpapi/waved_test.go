package httpapi

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestLinkedServiceContracts(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	// Without SECRET_KEY every service reports itself unavailable.
	kinds := h.request("GET", "/api/v1/me/service-kinds", nil, h.OwnerRaw, h.Owner.CSRF, origin, "")
	requireCode(t, kinds, 200)
	if strings.Contains(kinds.Body.String(), `"available":true`) {
		t.Fatal("services offered without a key")
	}
	requireCode(t, h.request("POST", "/api/v1/me/services", map[string]any{"kind": "kitsu", "schedule": "daily", "config": map[string]string{"username": "sam"}}, h.OwnerRaw, h.Owner.CSRF, origin, ""), 503)

	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	keyed, err := New(ready, nil, "test", Options{Pool: h.Pool, PublicOrigin: origin, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Secrets: box})
	if err != nil {
		t.Fatal(err)
	}
	hk := &harness{h.Fixture, keyed, t}
	call := func(method, path string, body any) (int, string) {
		t.Helper()
		key := ""
		if strings.HasSuffix(path, "/sync") {
			key = testkit.Key(t, h.Fixture)
		}
		rec := hk.request(method, path, body, h.OwnerRaw, h.Owner.CSRF, origin, key)
		return rec.Code, rec.Body.String()
	}
	if code, body := call("GET", "/api/v1/me/service-kinds", nil); code != 200 || !strings.Contains(body, `"kind":"kitsu","name":"Kitsu","fields":["username"],"auth":"","available":true`) {
		t.Fatal(code, body)
	}
	// Trakt needs the server's application keys.
	if code, _ := call("POST", "/api/v1/me/service-authorizations", map[string]string{"kind": "trakt"}); code != 503 {
		t.Fatal("trakt offered without keys", code)
	}
	if code, _ := call("POST", "/api/v1/me/services", map[string]any{"kind": "trakt", "schedule": "daily", "config": map[string]string{}}); code != 422 {
		t.Fatal("device-flow service linked with credentials", code)
	}
	code, body := call("POST", "/api/v1/me/services", map[string]any{"kind": "kitsu", "schedule": "daily", "config": map[string]string{"username": "sam", "token": "never-shown"}})
	if code != 201 || strings.Contains(body, "never-shown") {
		t.Fatal(code, body)
	}
	var link struct{ ID string }
	_ = json.Unmarshal([]byte(body), &link)
	if code, body = call("GET", "/api/v1/me/services", nil); code != 200 || !strings.Contains(body, `"description":"sam"`) {
		t.Fatal(code, body)
	}
	if code, body = call("PUT", "/api/v1/me/services/"+link.ID+"/schedule", map[string]string{"schedule": "manual"}); code != 200 || !strings.Contains(body, `"next_run_at":null`) {
		t.Fatal(code, body)
	}
	if code, _ = call("POST", "/api/v1/me/services/"+link.ID+"/sync", nil); code != 202 {
		t.Fatal("sync", code)
	}
	if code, _ = call("POST", "/api/v1/me/services/"+link.ID+"/sync", nil); code != 409 {
		t.Fatal("second sync", code)
	}
	if code, _ = call("POST", "/api/v1/me/service-authorizations/00000000-0000-4000-8000-000000000000/poll", nil); code != 404 {
		t.Fatal("unknown authorization", code)
	}
	if code, _ = call("DELETE", "/api/v1/me/services/"+link.ID, nil); code != 204 {
		t.Fatal("unlink", code)
	}
	if code, _ = call("DELETE", "/api/v1/me/services/"+link.ID, nil); code != 404 {
		t.Fatal("unlink twice", code)
	}
}

func TestKOReaderSyncServer(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	session := func(method, path string, body any) (int, string) {
		t.Helper()
		key := ""
		if method == "POST" && path == "/api/v1/library" {
			key = testkit.Key(t, h.Fixture)
		}
		rec := h.request(method, path, body, h.OwnerRaw, h.Owner.CSRF, origin, key)
		return rec.Code, rec.Body.String()
	}
	device := func(method, path, user, key, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:51234"
		req.Header.Set("Content-Type", "application/vnd.koreader.v1+json")
		req.Header.Set("x-auth-user", user)
		req.Header.Set("x-auth-key", key)
		out := httptest.NewRecorder()
		h.server.Router.ServeHTTP(out, req)
		return out.Code, out.Body.String()
	}
	// md5("koreader-secret")
	sum := md5.Sum([]byte("koreader-secret"))
	key := hex.EncodeToString(sum[:])
	if code, _ := device("GET", "/kosync/users/auth", "fixture_owner", key, ""); code != 401 {
		t.Fatal("signed in before a password was set", code)
	}
	if code, _ := session("PUT", "/api/v1/me/koreader", map[string]string{"password": "short"}); code != 422 {
		t.Fatal("short password accepted", code)
	}
	if code, body := session("PUT", "/api/v1/me/koreader", map[string]string{"password": "koreader-secret"}); code != 200 || !strings.Contains(body, `"configured":true`) {
		t.Fatal(code, body)
	}
	if code, _ := device("GET", "/kosync/users/auth", "FIXTURE_OWNER", strings.ToUpper(key), ""); code != 200 {
		t.Fatal("valid credentials refused", code)
	}
	if code, _ := device("GET", "/kosync/users/auth", "fixture_owner", "00000000000000000000000000000000", ""); code != 401 {
		t.Fatal("wrong key accepted", code)
	}
	if code, _ := device("POST", "/kosync/users/create", "", "", `{"username":"x","password":"y"}`); code != 402 {
		t.Fatal("registration allowed", code)
	}
	doc := "0123456789abcdef0123456789abcdef"
	if code, body := device("PUT", "/kosync/syncs/progress", "fixture_owner", key, `{"document":"`+doc+`","progress":"/body/DocFragment[5]","percentage":0.25,"device":"Kobo","device_id":"K1"}`); code != 200 || !strings.Contains(body, doc) {
		t.Fatal(code, body)
	}
	if code, body := device("GET", "/kosync/syncs/progress/"+doc, "fixture_owner", key, ""); code != 200 || !strings.Contains(body, `"percentage":0.25`) || !strings.Contains(body, "DocFragment") {
		t.Fatal(code, body)
	}
	if code, body := device("GET", "/kosync/syncs/progress/unknown", "fixture_owner", key, ""); code != 200 || strings.TrimSpace(body) != "{}" {
		t.Fatal(code, body)
	}
	// Linking the document to a book applies the position, then moves it forward.
	code, body := session("POST", "/api/v1/library", map[string]any{"title": "Synced Novel", "media_type": "book", "unit": "pages", "total": 400})
	if code != 201 {
		t.Fatal(code, body)
	}
	var entry struct{ ID string }
	_ = json.Unmarshal([]byte(body), &entry)
	if code, body = session("PUT", "/api/v1/me/koreader/documents/"+doc, map[string]any{"entry_id": entry.ID}); code != 200 || !strings.Contains(body, "Synced Novel") {
		t.Fatal(code, body)
	}
	progress := func() (int, string) {
		var p int
		var status string
		_ = h.Pool.QueryRow(context.Background(), "SELECT progress,status FROM library_entries WHERE id=$1", entry.ID).Scan(&p, &status)
		return p, status
	}
	if p, status := progress(); p != 100 || status != "in_progress" {
		t.Fatal("linked progress", p, status)
	}
	device("PUT", "/kosync/syncs/progress", "fixture_owner", key, `{"document":"`+doc+`","progress":"x","percentage":0.1}`)
	if p, _ := progress(); p != 100 {
		t.Fatal("progress moved back", p)
	}
	device("PUT", "/kosync/syncs/progress", "fixture_owner", key, `{"document":"`+doc+`","progress":"end","percentage":1}`)
	if p, status := progress(); p != 400 || status != "completed" {
		t.Fatal("finished book", p, status)
	}
	if code, _ := session("DELETE", "/api/v1/me/koreader", nil); code != 204 {
		t.Fatal(code)
	}
	if code, _ := device("GET", "/kosync/users/auth", "fixture_owner", key, ""); code != 401 {
		t.Fatal("disabled sync still signs in")
	}
}

func TestListenBrainzSubmission(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	rec := h.request("POST", "/api/v1/me/tokens", map[string]any{"name": "Scrobbler", "scopes": []string{"library:read", "library:write"}, "expires_in_days": 30, "password": testkit.Password}, h.OwnerRaw, h.Owner.CSRF, origin, "")
	requireCode(t, rec, 201)
	var token struct{ Secret string }
	_ = json.Unmarshal(rec.Body.Bytes(), &token)
	submit := func(body string) int {
		req := httptest.NewRequest("POST", "/apis/listenbrainz/1/submit-listens", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:51234"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Token "+token.Secret)
		out := httptest.NewRecorder()
		h.server.Router.ServeHTTP(out, req)
		return out.Code
	}
	if code := submit(`{"listen_type":"playing_now","payload":[{"track_metadata":{"artist_name":"Band","track_name":"Song"}}]}`); code != 200 {
		t.Fatal("playing_now", code)
	}
	if code := submit(`{"listen_type":"single","payload":[{"listened_at":1767384000,"track_metadata":{"artist_name":"Band","track_name":"Song","release_name":"First Album"}}]}`); code != 200 {
		t.Fatal("single", code)
	}
	if code := submit(`{"listen_type":"single","payload":[{"track_metadata":{"artist_name":"Band"}}]}`); code != 422 {
		t.Fatal("invalid listen", code)
	}
	var queued int
	_ = h.Pool.QueryRow(context.Background(), "SELECT count(*) FROM jobs WHERE kind='listens'").Scan(&queued)
	if queued != 1 {
		t.Fatal("listens queued", queued)
	}
}

func TestStorytellerDeviceSignIn(t *testing.T) {
	h := application(t)
	approved := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/device/start":
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"ABCD-1234","verification_uri":"http://internal:8001/device","interval":1,"expires_in":600}`))
		case "/api/v2/device/token":
			if !approved {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"st-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	origin := "http://127.0.0.1:8080"
	keyed, err := New(ready, nil, "test", Options{Pool: h.Pool, PublicOrigin: origin, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Secrets: box})
	if err != nil {
		t.Fatal(err)
	}
	hk := &harness{h.Fixture, keyed, t}
	start := hk.request("POST", "/api/v1/me/service-authorizations", map[string]string{"kind": "storyteller", "url": srv.URL}, h.OwnerRaw, h.Owner.CSRF, origin, "")
	requireCode(t, start, 201)
	var auth struct {
		ID              string `json:"id"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_url"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &auth)
	if auth.UserCode != "ABCD-1234" || !strings.HasPrefix(auth.VerificationURL, srv.URL+"/device") {
		t.Fatalf("start %+v", auth)
	}
	poll := func() string {
		rec := hk.request("POST", "/api/v1/me/service-authorizations/"+auth.ID+"/poll", nil, h.OwnerRaw, h.Owner.CSRF, origin, "")
		requireCode(t, rec, 200)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out["status"].(string)
	}
	if status := poll(); status != "pending" {
		t.Fatal("linked before approval", status)
	}
	approved = true
	if status := poll(); status != "linked" {
		t.Fatal("not linked after approval", status)
	}
}
