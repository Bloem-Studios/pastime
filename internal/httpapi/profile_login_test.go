package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

const testOrigin = "http://127.0.0.1:8080"

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err, string(body))
	}
	return out
}

// TestProfileLoginsOverHTTP shares one application across its cases: each
// harness migrates a schema, which dominates this package's run time.
func TestProfileLoginsOverHTTP(t *testing.T) {
	h := application(t)
	for name, check := range map[string]func(*testing.T, *harness){
		"cannot reach a sibling profile":    profileSessionCannotReachSiblingProfile,
		"needs CSRF":                        profileSessionNeedsCSRF,
		"long email signs in":               profileLoginLongEmailSignsIn,
		"primary manages logins":            primaryManagesLogins,
		"session reports the profile login": sessionReportsProfileLogin,
		"profile token hides the household": profileTokenMeHidesHousehold,
	} {
		t.Run(name, func(t *testing.T) { check(t, &harness{h.Fixture, h.server, t}) })
	}
}

func profileSessionCannotReachSiblingProfile(t *testing.T, h *harness) {
	_, sam, raw := h.ProfileLogin(t, "Sam", "sam_sibling")
	owner := h.Owner.TrackingID()
	req := func(path string, header string) int {
		r, _ := http.NewRequest("GET", path, nil)
		r.RemoteAddr = "127.0.0.1:51234"
		r.AddCookie(&http.Cookie{Name: "pastime_session", Value: raw})
		if header != "" {
			r.Header.Set("X-Pastime-Profile-ID", header)
		}
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		return w.Code
	}
	if code := req("/api/v1/library", itoa(owner)); code != 403 {
		t.Fatalf("library with the default profile's header: %d, want 403", code)
	}
	if code := req("/api/v1/library", itoa(sam.TrackingID())); code != 200 {
		t.Fatalf("library with its own profile's header: %d, want 200", code)
	}
	for _, path := range []string{"/api/v1/export-files/00000000-0000-4000-8000-000000000001?profile_id=" + itoa(owner), "/api/v1/export/library.csv?profile_id=" + itoa(owner)} {
		if code := req(path, ""); code != 403 && code != 404 {
			t.Fatalf("%s: %d, want 403 or 404", path, code)
		}
	}
	res := h.request("GET", "/api/v1/profiles", nil, raw, "", "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	items := decodeBody(t, res.Body.Bytes())["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != itoa(sam.TrackingID()) {
		t.Fatalf("profiles visible to the profile session: %v", items)
	}
}

func profileSessionNeedsCSRF(t *testing.T, h *harness) {
	_, sam, raw := h.ProfileLogin(t, "Sam", "sam_csrf")
	if res := h.request("POST", "/api/v1/logout", nil, raw, "", testOrigin, ""); res.Code != 403 {
		t.Fatalf("logout without CSRF: %d, want 403", res.Code)
	}
	if res := h.request("POST", "/api/v1/logout", nil, raw, sam.CSRF, "https://elsewhere.example.test", ""); res.Code != 403 {
		t.Fatalf("logout from another origin: %d, want 403", res.Code)
	}
	if res := h.request("POST", "/api/v1/logout", nil, raw, sam.CSRF, testOrigin, ""); res.Code != 204 {
		t.Fatalf("logout with CSRF: %d, want 204", res.Code)
	}
}

func profileLoginLongEmailSignsIn(t *testing.T, h *harness) {
	email := strings.Repeat("s", 47) + "@example.test"
	if len(email) != 60 {
		t.Fatal(len(email))
	}
	sam, err := h.Accounts.CreateProfile(t.Context(), h.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Accounts.SetProfileLogin(t.Context(), h.Owner, sam.ID, identity.ProfileLoginInput{LoginName: email, Password: testkit.Password}); err != nil {
		t.Fatal(err)
	}
	res := h.request("POST", "/api/v1/login", map[string]string{"username": strings.ToUpper(email), "password": testkit.Password}, "", "", testOrigin, "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	login, _ := decodeBody(t, res.Body.Bytes())["profile_login"].(map[string]any)
	if login == nil || login["login_name"] != email || login["profile_id"] != itoa(sam.ID) {
		t.Fatalf("profile_login: %v", login)
	}
}

func primaryManagesLogins(t *testing.T, h *harness) {
	_, sam, samRaw := h.ProfileLogin(t, "Sam", "sam_manages")
	// Profiles come from Bloem; Pastime has no route that adds one.
	kit, err := h.Accounts.CreateProfile(t.Context(), h.Owner, "Kit")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/profiles/" + itoa(kit.ID) + "/login"
	if res := h.request("PUT", path, map[string]string{"login_name": "kit", "password": testkit.Password}, h.OwnerRaw, h.Owner.CSRF, testOrigin, ""); res.Code != 200 {
		t.Fatal("PUT", res.Code, res.Body.String())
	}
	res := h.request("GET", "/api/v1/profile-logins", nil, h.OwnerRaw, "", "", "")
	if res.Code != 200 {
		t.Fatal("GET", res.Code, res.Body.String())
	}
	listed := false
	for _, item := range decodeBody(t, res.Body.Bytes())["items"].([]any) {
		listed = listed || item.(map[string]any)["profile_id"] == itoa(kit.ID) && item.(map[string]any)["login_name"] == "kit"
	}
	if !listed {
		t.Fatalf("logins: %s", res.Body.String())
	}
	if res = h.request("DELETE", path, nil, h.OwnerRaw, h.Owner.CSRF, testOrigin, ""); res.Code != 204 {
		t.Fatal("DELETE", res.Code, res.Body.String())
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/profile-logins"},
		{"PUT", "/api/v1/profiles/" + itoa(sam.TrackingID()) + "/login"},
		{"DELETE", "/api/v1/profiles/" + itoa(sam.TrackingID()) + "/login"},
	} {
		var body any
		if c.method == "PUT" {
			body = map[string]string{"login_name": "sam_manages", "password": testkit.Password}
		}
		if res := h.request(c.method, c.path, body, samRaw, sam.CSRF, testOrigin, ""); res.Code != 403 {
			t.Errorf("%s %s from a profile session: %d, want 403", c.method, c.path, res.Code)
		}
	}
}

func sessionReportsProfileLogin(t *testing.T, h *harness) {
	sam, _, raw := h.ProfileLogin(t, "Sam", "sam_session")
	res := h.request("GET", "/api/v1/session", nil, raw, "", "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	body := decodeBody(t, res.Body.Bytes())
	user := body["user"].(map[string]any)
	login, _ := body["profile_login"].(map[string]any)
	if user["role"] != "member" || user["email"] != "" || user["username"] != "sam_session" || login == nil || login["profile_id"] != itoa(sam.ID) {
		t.Fatalf("session view: %v", body)
	}
	if res = h.request("GET", "/api/v1/me", nil, raw, "", "", ""); res.Code != 200 || decodeBody(t, res.Body.Bytes())["email"] != "" {
		t.Fatalf("me: %d %s", res.Code, res.Body.String())
	}
	owner := decodeBody(t, h.request("GET", "/api/v1/session", nil, h.OwnerRaw, "", "", "").Body.Bytes())
	if owner["profile_login"] != nil {
		t.Fatalf("account session reports a profile login: %v", owner)
	}
}

// A token a profile person mints acts for their profile only; GET /me through
// it must not show the household account's email or role either.
func profileTokenMeHidesHousehold(t *testing.T, h *harness) {
	if _, err := h.Pool.Exec(t.Context(), `UPDATE users SET email='household@example.test' WHERE id=$1`, h.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	_, sam, _ := h.ProfileLogin(t, "Sam", "sam_token")
	me := func(a identity.Actor) map[string]any {
		_, key, err := h.Accounts.MintToken(t.Context(), a, identity.TokenInput{Name: "Reader", Scopes: []string{"profile:read"}, ExpiresInDays: 1, Password: testkit.Password})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/api/v1/me", nil)
		r.RemoteAddr = "127.0.0.1:51234"
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return decodeBody(t, w.Body.Bytes())
	}
	if got := me(sam); got["email"] != "" || got["role"] != "member" || got["last_login_at"] != nil {
		t.Fatalf("profile token sees the household account: %v", got)
	}
	if got := me(h.Owner); got["email"] != "household@example.test" || got["role"] != "owner" {
		t.Fatalf("household token lost its own account view: %v", got)
	}
}
