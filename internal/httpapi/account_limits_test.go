package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// from sends a public JSON request as if from a distinct client address.
func (h *harness) from(addr, method, path string, body any, header map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.RemoteAddr = addr + ":40000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	out := httptest.NewRecorder()
	h.server.Router.ServeHTTP(out, req)
	return out
}

func TestPasswordRecoveryBudgetIsPerAccountAcrossAddresses(t *testing.T) {
	h := application(t)
	body := map[string]string{"username": "Fixture_Owner", "code": "123456", "password": "a-long-synthetic-password"}
	for i := 0; i < 5; i++ {
		if got := h.from(fmt.Sprintf("203.0.113.%d", i+1), "POST", "/api/v1/password-recovery", body, nil); got.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d limited early", i+1)
		}
	}
	requireCode(t, h.from("203.0.113.99", "POST", "/api/v1/password-recovery", body, nil), http.StatusTooManyRequests)
	// Another account keeps its own budget.
	other := map[string]string{"username": "someone_else", "code": "123456", "password": "a-long-synthetic-password"}
	if got := h.from("203.0.113.99", "POST", "/api/v1/password-recovery", other, nil); got.Code == http.StatusTooManyRequests {
		t.Fatal("unrelated account limited")
	}
}

func TestOneClientCannotLockAnAccountOutOfSignIn(t *testing.T) {
	h := application(t)
	wrong := map[string]string{"username": "fixture_owner", "password": "not-the-synthetic-password"}
	for i := 0; i < 12; i++ {
		requireCode(t, h.from("198.51.100.7", "POST", "/api/v1/login", wrong, nil), http.StatusUnauthorized)
	}
	requireCode(t, h.from("198.51.100.7", "POST", "/api/v1/login", wrong, nil), http.StatusTooManyRequests)
	right := map[string]string{"username": "fixture_owner", "password": testkit.Password}
	requireCode(t, h.from("198.51.100.8", "POST", "/api/v1/login", right, nil), http.StatusOK)
}

func TestLoginAccountCeilingCoversManyAddresses(t *testing.T) {
	h := application(t)
	wrong := map[string]string{"username": "fixture_owner", "password": "not-the-synthetic-password"}
	limited := false
	for i := 0; i < 130 && !limited; i++ {
		limited = h.from(fmt.Sprintf("2001:db8:%x::1", i), "POST", "/api/v1/login", wrong, nil).Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("account-wide ceiling never applied")
	}
}

func TestKOReaderBudgetIsPerAccount(t *testing.T) {
	h := application(t)
	header := map[string]string{"x-auth-user": "fixture_owner", "x-auth-key": "0123456789abcdef0123456789abcdef"}
	limited := false
	for i := 0; i < 130 && !limited; i++ {
		limited = h.from(fmt.Sprintf("2001:db8:%x::1", i), "GET", "/kosync/users/auth", nil, header).Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("KOReader account ceiling never applied")
	}
}

// Turning off the authenticator verifies the current password, so it draws on the one
// per-account budget every current-password check shares: a session cannot guess the
// password there, nor add the budgets of several routes together.
func TestCurrentPasswordChecksShareOneBudget(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	wrong := map[string]string{"password": "not-the-synthetic-password"}
	for i := 0; i < 6; i++ {
		requireCode(t, h.request("DELETE", "/api/v1/me/totp", wrong, h.OwnerRaw, h.Owner.CSRF, origin, ""), http.StatusUnprocessableEntity)
	}
	requireCode(t, h.request("DELETE", "/api/v1/me/totp", wrong, h.OwnerRaw, h.Owner.CSRF, origin, ""), http.StatusTooManyRequests)
	requireCode(t, h.request("POST", "/api/v1/me/password", map[string]string{"current_password": "not-the-synthetic-password", "password": "another-synthetic-password"}, h.OwnerRaw, h.Owner.CSRF, origin, ""), http.StatusTooManyRequests)
	requireCode(t, h.request("POST", "/api/v1/me/tokens", map[string]any{"name": "Guess", "scopes": []string{"library:read"}, "expires_in_days": 30, "password": "not-the-synthetic-password"}, h.OwnerRaw, h.Owner.CSRF, origin, ""), http.StatusTooManyRequests)
	requireCode(t, h.request("POST", "/api/v1/me/totp", wrong, h.OwnerRaw, h.Owner.CSRF, origin, ""), http.StatusTooManyRequests)
	// Another account keeps its own budget.
	h.User(t, "budget_member", "member")
	session, raw := h.Login(t, "budget_member")
	requireCode(t, h.request("DELETE", "/api/v1/me/totp", wrong, raw, session.CSRF, origin, ""), http.StatusUnprocessableEntity)
}
