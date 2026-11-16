package httpapi

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
	"time"
)

func TestPublicRegistrationMemberSessionAndBoundaries(t *testing.T) {
	h := application(t)
	input := map[string]string{"username": "New_Member", "display_name": "New Member", "email": "member@example.test", "password": testkit.Password}
	requireCode(t, h.request("POST", "/api/v1/register", input, "", "", "http://127.0.0.1:8080", ""), 403)
	h.server.publicRegistration = true
	for _, origin := range []string{"", "https://evil.example"} {
		requireCode(t, h.request("POST", "/api/v1/register", input, "", "", origin, ""), 403)
	}
	elevated := map[string]string{"username": "elevated", "email": "other@example.test", "password": testkit.Password, "role": "owner"}
	requireCode(t, h.request("POST", "/api/v1/register", elevated, "", "", "http://127.0.0.1:8080", ""), 422)
	out := h.request("POST", "/api/v1/register", input, "", "", "http://127.0.0.1:8080", "")
	requireCode(t, out, 201)
	var session struct {
		User identity.User `json:"user"`
		CSRF string        `json:"csrf_token"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.User.Role != "member" || session.User.Username != "new_member" || session.User.DisplayName != "New Member" || len(session.CSRF) != 64 {
		t.Fatal("wrong member session", session.User.Role)
	}
	cookies := out.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("missing protected session")
	}
	requireCode(t, h.request("GET", "/api/admin/v1/users", nil, cookies[0].Value, "", "", ""), 403)
	requireCode(t, h.request("POST", "/api/v1/register", input, "", "", "http://127.0.0.1:8080", ""), 409)
	var count int
	if err := h.Pool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE username='new_member'").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate account", count, err)
	}
	requireCode(t, h.request("POST", "/api/v1/login", map[string]string{"username": "new_member", "password": testkit.Password}, "", "", "http://127.0.0.1:8080", ""), 200)
}

func TestPublicRegistrationValidationAndRateLimit(t *testing.T) {
	h := application(t)
	h.server.publicRegistration = true
	for _, input := range []map[string]string{
		{"username": "ab", "email": "valid@example.test", "password": testkit.Password},
		{"username": "valid", "email": "bad-email", "password": testkit.Password},
		{"username": "valid", "email": "valid@example.test", "password": "short"},
		{"username": "valid", "email": "", "password": testkit.Password},
	} {
		requireCode(t, h.request("POST", "/api/v1/register", input, "", "", "http://127.0.0.1:8080", ""), 422)
	}
	for i := 0; i < 5; i++ {
		_ = h.server.accounts.Limit(context.Background(), "register:127.0.0.1", 5, time.Minute)
	}
	requireCode(t, h.request("POST", "/api/v1/register", map[string]string{"username": "limited", "email": "limited@example.test", "password": testkit.Password}, "", "", "http://127.0.0.1:8080", ""), 429)
	var count int
	if err := h.Pool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE role='member'").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid request persisted", count, err)
	}
}
