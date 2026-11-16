package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"github.com/go-chi/chi/v5"
)

type harness struct {
	*testkit.Fixture
	server *Server
	t      *testing.T
}

func application(t *testing.T) *harness {
	t.Helper()
	f := testkit.New(t)
	s, e := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if e != nil {
		t.Fatal(e)
	}
	return &harness{f, s, t}
}
func (h *harness) request(method, path string, body any, raw, csrf, origin, key string) *httptest.ResponseRecorder {
	h.t.Helper()
	var data []byte
	if body != nil {
		var e error
		data, e = json.Marshal(body)
		if e != nil {
			h.t.Fatal(e)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.RemoteAddr = "127.0.0.1:51234"
	req.Header.Set("User-Agent", "pastime-test/1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if raw != "" {
		req.AddCookie(&http.Cookie{Name: "pastime_session", Value: raw})
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	out := httptest.NewRecorder()
	h.server.Router.ServeHTTP(out, req)
	rctx := chi.NewRouteContext()
	u, _ := url.Parse(path)
	if h.server.Router.Match(rctx, method, u.Path) {
		if p := h.server.Spec.Paths.Value(rctx.RoutePattern()); p != nil {
			op := p.GetOperation(method)
			response := op.Responses.Value(strconv.Itoa(out.Code))
			if response == nil {
				h.t.Fatalf("undocumented %s %s %d: %s", method, path, out.Code, out.Body.String())
			}
			if content, ok := response.Value.Content["application/json"]; ok {
				var value any
				if e := json.Unmarshal(out.Body.Bytes(), &value); e != nil {
					h.t.Fatal(e)
				}
				if e := content.Schema.Value.VisitJSON(value); e != nil {
					h.t.Fatalf("response contract %s %s: %v", method, path, e)
				}
			}
		}
	}
	return out
}
func requireCode(t *testing.T, r *httptest.ResponseRecorder, want int) {
	t.Helper()
	if r.Code != want {
		t.Fatalf("status %d, want %d: %s", r.Code, want, r.Body.String())
	}
}
func TestBrowserSessionCSRFOriginAndProtectedDocumentation(t *testing.T) {
	h := application(t)
	for _, path := range []string{"/api/v1/session", "/api/v1/library", "/api/admin/v1/users", "/api/openapi.json", "/api/openapi.yaml", "/docs/"} {
		requireCode(t, h.request("GET", path, nil, "", "", "", ""), 401)
	}
	for _, origin := range []string{"", "http://evil.example", "http://127.0.0.1:8080.evil.example"} {
		requireCode(t, h.request("POST", "/api/v1/login", map[string]string{"username": "fixture_owner", "password": testkit.Password}, "", "", origin, ""), 403)
	}
	login := h.request("POST", "/api/v1/login", map[string]string{"username": "fixture_owner", "password": testkit.Password}, "", "", "http://127.0.0.1:8080", "")
	requireCode(t, login, 200)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" || cookies[0].Domain != "" {
		t.Fatal("unsafe session cookie")
	}
	var session struct {
		User identity.User `json:"user"`
		CSRF string        `json:"csrf_token"`
	}
	if e := json.Unmarshal(login.Body.Bytes(), &session); e != nil {
		t.Fatal(e)
	}
	if len(session.CSRF) != 64 || strings.Contains(login.Body.String(), cookies[0].Value) {
		t.Fatal("session secret disclosed")
	}
	for _, v := range []struct{ csrf, origin string }{{"", "http://127.0.0.1:8080"}, {session.CSRF, ""}, {session.CSRF, "http://evil.example"}} {
		requireCode(t, h.request("POST", "/api/v1/logout", nil, cookies[0].Value, v.csrf, v.origin, ""), 403)
	}
	requireCode(t, h.request("GET", "/api/openapi.json", nil, cookies[0].Value, "", "", ""), 200)
	sessions := h.request("GET", "/api/v1/me/sessions", nil, cookies[0].Value, "", "", "")
	requireCode(t, sessions, 200)
	if !strings.Contains(sessions.Body.String(), `"user_agent":"pastime-test/1"`) || !strings.Contains(sessions.Body.String(), `"last_used_at":"`) {
		t.Fatalf("the session does not name its client: %s", sessions.Body.String())
	}
	requireCode(t, h.request("POST", "/api/v1/logout", nil, cookies[0].Value, session.CSRF, "http://127.0.0.1:8080", ""), 204)
	requireCode(t, h.request("GET", "/api/v1/session", nil, cookies[0].Value, "", "", ""), 401)
	member := h.User(t, "http_member", "member")
	_, memberRaw := h.Login(t, member.User.Username)
	requireCode(t, h.request("GET", "/api/openapi.json", nil, memberRaw, "", "", ""), 403)
	requireCode(t, h.request("GET", "/api/admin/v1/users", nil, memberRaw, "", "", ""), 403)
}
func TestHTTPSCookieHostPrefixAndUntrustedOriginConfiguration(t *testing.T) {
	s, e := New(ready, nil, "test", Options{PublicOrigin: "https://tracker.example"})
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	s.setCookie(w, "synthetic-cookie-value")
	c := w.Result().Cookies()[0]
	if c.Name != "__Host-pastime_session" || !c.Secure || !c.HttpOnly || c.Domain != "" || c.Path != "/" {
		t.Fatal("unsafe HTTPS cookie")
	}
	for _, origin := range []string{"http://tracker.example", "https://user:password@tracker.example", "https://tracker.example/path", "https://tracker.example?query=1"} {
		if _, e = New(ready, nil, "test", Options{PublicOrigin: origin}); e == nil {
			t.Fatal("unsafe public origin", origin)
		}
	}
}
func TestLibraryAndAdministrationResponseContracts(t *testing.T) {
	h := application(t)
	csrf := h.Owner.CSRF
	raw := h.OwnerRaw
	origin := "http://127.0.0.1:8080"
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		return h.request(method, path, body, raw, csrf, origin, testkit.Key(t, h.Fixture))
	}
	entry := map[string]any{"title": "Contract Atlas", "media_type": "book", "unit": "pages", "total": 100, "notes": "private contract note"}
	created := call("POST", "/api/v1/library", entry)
	requireCode(t, created, 201)
	var ack struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &ack)
	path := "/api/v1/library/" + ack.ID
	requireCode(t, call("GET", "/api/v1/history?entry_id=atlas", nil), 422)
	for _, p := range []string{path, "/api/v1/library", "/api/v1/library?q=Atlas", "/api/v1/library-stats", "/api/v1/activity", "/api/v1/history?entry_id=" + ack.ID, "/api/v1/me", "/api/v1/me/sessions", "/api/v1/me/tokens", "/api/admin/v1/users", "/api/admin/v1/users/" + strconv.FormatInt(h.Owner.User.ID, 10), "/api/admin/v1/invitations", "/api/admin/v1/audit"} {
		requireCode(t, call("GET", p, nil), 200)
	}
	requireCode(t, call("POST", path+"/progress", map[string]any{"version": 1, "progress": 10, "status": "in_progress"}), 200)
	requireCode(t, call("POST", path+"/progress", map[string]any{"version": 1, "progress": 11, "status": "in_progress"}), 409)
	entry["version"] = 2
	entry["status"] = "completed"
	requireCode(t, call("PUT", path, entry), 200)
	requireCode(t, call("POST", path+"/restart", map[string]int{"version": 3}), 200)
	list := call("POST", "/api/v1/lists", map[string]string{"name": "Contract shelf"})
	requireCode(t, list, 201)
	var listAck struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listAck)
	listPath := "/api/v1/lists/" + listAck.ID
	requireCode(t, call("PUT", listPath+"/entries/"+ack.ID, map[string]any{"version": 1, "present": true}), 200)
	for _, p := range []string{"/api/v1/lists", listPath, "/api/v1/library?list_id=" + listAck.ID} {
		requireCode(t, call("GET", p, nil), 200)
	}
	onLists := call("GET", path+"/lists", nil)
	requireCode(t, onLists, 200)
	if !strings.Contains(onLists.Body.String(), `"id":"`+listAck.ID+`"`) {
		t.Fatalf("entry lists %s", onLists.Body.String())
	}
	requireCode(t, call("PUT", listPath, map[string]any{"version": 2, "name": "Renamed shelf"}), 200)
	requireCode(t, call("DELETE", listPath, map[string]int{"version": 3}), 200)
	requireCode(t, call("DELETE", path, map[string]int{"version": 4}), 200)
	requireCode(t, call("GET", path, nil), 404)
	invited := call("POST", "/api/admin/v1/invitations", map[string]string{"role": "member"})
	requireCode(t, invited, 201)
	var invite struct {
		Invitation identity.Invitation `json:"invitation"`
		URL        string              `json:"claim_url"`
	}
	_ = json.Unmarshal(invited.Body.Bytes(), &invite)
	if !strings.HasPrefix(invite.URL, origin+"/join#fn_i_") {
		t.Fatal("invitation secret not fragment-only")
	}
	parsed, _ := url.Parse(invite.URL)
	requireCode(t, h.request("POST", "/api/v1/invitations/preview", map[string]string{"token": parsed.Fragment}, "", "", origin, ""), 200)
	requireCode(t, h.request("POST", "/api/v1/invitations/claim", map[string]string{"token": parsed.Fragment, "username": "contract_joiner", "password": testkit.Password}, "", "", origin, ""), 201)
	requireCode(t, call("DELETE", "/api/admin/v1/invitations/"+invite.Invitation.ID, nil), 409)
	newUser := call("POST", "/api/admin/v1/users", map[string]string{"username": "contract_pending", "role": "member"})
	requireCode(t, newUser, 201)
	var user struct {
		User identity.User `json:"user"`
	}
	_ = json.Unmarshal(newUser.Body.Bytes(), &user)
	userPath := "/api/admin/v1/users/" + strconv.FormatInt(user.User.ID, 10)
	requireCode(t, call("POST", userPath+"/reset-link", nil), 201)
	requireCode(t, call("POST", userPath+"/revoke-access", nil), 204)
	requireCode(t, call("PATCH", userPath, map[string]any{"version": 2, "active": false}), 200)
}
func TestMalformedInputTokenScopeAndAmbiguousCredentials(t *testing.T) {
	h := application(t)
	ctx := context.Background()
	_, key, e := h.Accounts.MintToken(ctx, h.Owner, identity.TokenInput{Name: "HTTP token", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path   string
		cookie bool
		want   int
	}{{"/api/v1/library", false, 200}, {"/api/admin/v1/users", false, 403}, {"/api/v1/me", false, 403}, {"/api/v1/library", true, 401}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		if tc.cookie {
			r.AddCookie(&http.Cookie{Name: "pastime_session", Value: h.OwnerRaw})
		}
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		requireCode(t, w, tc.want)
	}
	for _, p := range []string{"/api/v1/library?limit=101", "/api/v1/library?limit=0", "/api/v1/library?cursor=not-a-cursor", "/api/admin/v1/users?role=superuser"} {
		requireCode(t, h.request("GET", p, nil, h.OwnerRaw, "", "", ""), 422)
	}
	for _, body := range []string{`{"title":"Atlas","media_type":"book","unit":"pages","user_id":"999"}`, `{"title":"Atlas"} {"second":"object"}`, `null`, strings.Repeat(" ", 66000) + `{}`} {
		r := httptest.NewRequest("POST", "/api/v1/library", strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "pastime_session", Value: h.OwnerRaw})
		r.Header.Set("Origin", "http://127.0.0.1:8080")
		r.Header.Set("X-CSRF-Token", h.Owner.CSRF)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", testkit.Key(t, h.Fixture))
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		requireCode(t, w, 422)
	}
	for _, ip := range []struct{ remote, want string }{{"127.0.0.1:1234", "127.0.0.1"}, {"[2001:db8:1234:5678::1]:1234", "2001:db8:1234:5678::/64"}, {"[::ffff:192.0.2.5]:1234", "192.0.2.5"}} {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = ip.remote
		r.Header.Set("X-Forwarded-For", "evil")
		if got := remoteBucket(r); got != ip.want {
			t.Fatal(fmt.Sprintf("IP bucket %q != %q", got, ip.want))
		}
	}
}
