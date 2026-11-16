package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceSignInIssuesOneScopedTokenAfterApproval(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	start := h.request("POST", "/api/v1/device/authorize", map[string]any{"client_name": "Living room TV", "scopes": []string{"library:read"}}, "", "", "", "")
	requireCode(t, start, 200)
	var s struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri_complete"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &s); err != nil || !strings.HasPrefix(s.DeviceCode, "pd_") || len(s.UserCode) != 9 || !strings.HasSuffix(s.URI, "/device?code="+s.UserCode) {
		t.Fatal(start.Body.String(), err)
	}
	poll := func() (string, string) {
		r := h.request("POST", "/api/v1/device/token", map[string]string{"device_code": s.DeviceCode}, "", "", "", "")
		requireCode(t, r, 200)
		var out struct {
			Status string `json:"status"`
			Token  string `json:"access_token"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		return out.Status, out.Token
	}
	if status, token := poll(); status != "pending" || token != "" {
		t.Fatal(status, token)
	}
	if _, err := h.Pool.Exec(t.Context(), "UPDATE device_authorizations SET last_poll_at=clock_timestamp()-interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	// The code is entered lower case without its dash; a session is required.
	code := strings.ToLower(strings.ReplaceAll(s.UserCode, "-", ""))
	requireCode(t, h.request("GET", "/api/v1/device?code="+code, nil, "", "", "", ""), 401)
	lookup := h.request("GET", "/api/v1/device?code="+code, nil, h.OwnerRaw, "", "", "")
	requireCode(t, lookup, 200)
	if !strings.Contains(lookup.Body.String(), "Living room TV") {
		t.Fatal(lookup.Body.String())
	}
	requireCode(t, h.request("POST", "/api/v1/device/decision", map[string]any{"code": code, "approve": true}, h.OwnerRaw, "wrong", origin, ""), 403)
	requireCode(t, h.request("POST", "/api/v1/device/decision", map[string]any{"code": code, "approve": true}, h.OwnerRaw, h.Owner.CSRF, origin, ""), 204)
	status, token := poll()
	if status != "approved" || !strings.HasPrefix(token, "fn_k_") {
		t.Fatal(status, token)
	}
	// The token reads the library but cannot write, and is handed out only once.
	requireCode(t, bearer(h, "GET", "/api/v1/library", token), 200)
	if write := bearer(h, "POST", "/api/v1/library", token); write.Code != http.StatusForbidden {
		t.Fatal("token wrote without library:write", write.Code)
	}
	if _, err := h.Pool.Exec(t.Context(), "UPDATE device_authorizations SET last_poll_at=clock_timestamp()-interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	if status, token = poll(); status != "expired" || token != "" {
		t.Fatal("token handed out twice", status)
	}
	requireCode(t, h.request("POST", "/api/v1/device/decision", map[string]any{"code": code, "approve": true}, h.OwnerRaw, h.Owner.CSRF, origin, ""), 404)
}

func TestDeviceSignInDenialSlowDownAndRevocation(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	start := func() (string, string) {
		r := h.request("POST", "/api/v1/device/authorize", map[string]any{"client_name": "Phone"}, "", "", "", "")
		requireCode(t, r, 200)
		var s struct {
			DeviceCode string `json:"device_code"`
			UserCode   string `json:"user_code"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &s)
		return s.DeviceCode, s.UserCode
	}
	status := func(device string) string {
		r := h.request("POST", "/api/v1/device/token", map[string]string{"device_code": device}, "", "", "", "")
		requireCode(t, r, 200)
		var out struct{ Status string }
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		return out.Status
	}
	device, user := start()
	if status(device) != "pending" || status(device) != "slow_down" {
		t.Fatal("polling too fast was not slowed down")
	}
	requireCode(t, h.request("POST", "/api/v1/device/decision", map[string]any{"code": user, "approve": false}, h.OwnerRaw, h.Owner.CSRF, origin, ""), 204)
	if _, err := h.Pool.Exec(t.Context(), "UPDATE device_authorizations SET last_poll_at=NULL"); err != nil {
		t.Fatal(err)
	}
	if status(device) != "denied" {
		t.Fatal("denial not reported")
	}
	// Approval followed by "sign out everywhere" issues nothing.
	device, user = start()
	requireCode(t, h.request("POST", "/api/v1/device/decision", map[string]any{"code": user, "approve": true}, h.OwnerRaw, h.Owner.CSRF, origin, ""), 204)
	if _, err := h.Pool.Exec(t.Context(), "UPDATE users SET auth_epoch=auth_epoch+1 WHERE username='fixture_owner'"); err != nil {
		t.Fatal(err)
	}
	if status(device) != "denied" {
		t.Fatal("token issued after revocation")
	}
	requireCode(t, h.request("POST", "/api/v1/device/token", map[string]string{"device_code": "pd_unknown"}, "", "", "", ""), 404)
}

// bearer sends a request with an API token and no browser context.
func bearer(h *harness, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:51234"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	h.server.Router.ServeHTTP(out, req)
	return out
}
