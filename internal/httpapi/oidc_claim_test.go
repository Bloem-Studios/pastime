package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// TestSingleSignOnClaimsManagedAccount drives the browser callback: a verified
// provider identity equal to a managed account's identity lands in that account,
// and an unclaimable match is refused with a logged reason, never auto-created.
func TestSingleSignOnClaimsManagedAccount(t *testing.T) {
	issuer := newFakeIssuer(t)
	defer issuer.Close()
	f := testkit.New(t)
	ctx := t.Context()
	logs := &lockedBuffer{}
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		OIDC: OIDC{Issuer: issuer.URL, ClientID: "pastime", ClientSecret: "s", Name: "Authentik", AutoCreate: true}})
	if err != nil {
		t.Fatal(err)
	}
	const instanceIssuer = "https://sso.example.test/application/o/bloem/"
	store := bloem.Store{Pool: f.Pool}
	instance, raw, err := store.Register(ctx, f.Owner, bloem.InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot", IdentityIssuer: instanceIssuer})
	if err != nil {
		t.Fatal(err)
	}
	// The fake provider runs on loopback HTTP, which the operator command refuses,
	// so the equivalence is recorded directly.
	if _, err = f.Pool.Exec(ctx, `INSERT INTO bloem_login_issuers(instance_id,login_issuer,subject_mode) VALUES($1,$2,'user_uuid')`, instance.ID, issuer.URL); err != nil {
		t.Fatal(err)
	}
	provision := func(source, subject string) bloem.ProvisionResult {
		t.Helper()
		r, err := store.Provision(ctx, raw, bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: source, AccountName: "Bloem " + source, ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: instanceIssuer, IdentitySubject: subject})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	var binding *http.Cookie
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		if binding != nil {
			req.AddCookie(binding)
		}
		out := httptest.NewRecorder()
		s.Router.ServeHTTP(out, req)
		for _, c := range out.Result().Cookies() {
			if c.Name == "pastime_session_oidc" {
				binding = c
			}
		}
		return out
	}
	signIn := func(subject string) *httptest.ResponseRecorder {
		issuer.subject = subject
		start := get("/auth/oidc/start?return_to=" + url.QueryEscape("/library"))
		if start.Code != 302 {
			t.Fatalf("start %d", start.Code)
		}
		code, state := issuer.authorize(t, start.Header().Get("Location"))
		return get("/auth/oidc/callback?code=" + code + "&state=" + url.QueryEscape(state))
	}
	const claimable = "0b6f2a2e-5c1d-4f8e-9a3b-7d2c1e0f4a51"
	managed := provision("claimable", claimable)
	out := signIn(claimable)
	if out.Header().Get("Location") != "/library" {
		t.Fatalf("claim sign-in: %v", out.Header())
	}
	var session string
	for _, c := range out.Result().Cookies() {
		if c.Name == "pastime_session" && c.Value != "" {
			session = c.Value
		}
	}
	if a, err := f.Accounts.Authenticate(ctx, session, "session"); err != nil || a.User.ID != managed.AccountID {
		t.Fatal("claim signed in to the wrong account", err)
	}
	var users int
	if err = f.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	const refused = "4c3d2e1f-0a9b-4c8d-8e7f-6a5b4c3d2e1f"
	blocked := provision("refused", refused)
	if _, err = f.Pool.Exec(ctx, `UPDATE users SET password_hash='synthetic-hash' WHERE id=$1`, blocked.AccountID); err != nil {
		t.Fatal(err)
	}
	if out = signIn(refused); !strings.Contains(out.Header().Get("Location"), "error=identity_claim_conflict") {
		t.Fatalf("unclaimable account: %v", out.Header())
	}
	var after int
	if err = f.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&after); err != nil || after != users+1 {
		t.Fatal("refused claim auto-created an account", users, after, err)
	}
	text := logs.String()
	if !strings.Contains(text, "managed account claim refused") || !strings.Contains(text, "has_password") || !strings.Contains(text, instance.ID) {
		t.Fatalf("refusal not logged: %s", text)
	}
	if strings.Contains(text, refused) || strings.Contains(text, "@example.test") || strings.Contains(text, instanceIssuer) {
		t.Fatal("refusal log carries identity details")
	}
}

// TestSingleSignOnClaimsOnlyAMatchingIdentity drives the browser callback with
// sign-ins that do not match a managed identity: another subject, a sign-in
// issuer that is not declared, and a declared issuer whose subject mode the
// subject does not fit. Each lands in a separate new account and claims nothing;
// the matching sign-in still claims afterwards.
func TestSingleSignOnClaimsOnlyAMatchingIdentity(t *testing.T) {
	issuer := newFakeIssuer(t)
	defer issuer.Close()
	f := testkit.New(t)
	ctx := t.Context()
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		OIDC: OIDC{Issuer: issuer.URL, ClientID: "pastime", ClientSecret: "s", Name: "Authentik", AutoCreate: true}})
	if err != nil {
		t.Fatal(err)
	}
	const instanceIssuer = "https://sso.example.test/application/o/bloem/"
	store := bloem.Store{Pool: f.Pool}
	instance, raw, err := store.Register(ctx, f.Owner, bloem.InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot", IdentityIssuer: instanceIssuer})
	if err != nil {
		t.Fatal(err)
	}
	declare := func(mode string) {
		t.Helper()
		if _, err := f.Pool.Exec(ctx, `DELETE FROM bloem_login_issuers WHERE instance_id=$1`, instance.ID); err != nil {
			t.Fatal(err)
		}
		if mode == "" {
			return
		}
		if _, err := f.Pool.Exec(ctx, `INSERT INTO bloem_login_issuers(instance_id,login_issuer,subject_mode) VALUES($1,$2,$3)`, instance.ID, issuer.URL, mode); err != nil {
			t.Fatal(err)
		}
	}
	provision := func(source, subject string) int64 {
		t.Helper()
		r, err := store.Provision(ctx, raw, bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: source, AccountName: "Bloem " + source, ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: instanceIssuer, IdentitySubject: subject})
		if err != nil {
			t.Fatal(err)
		}
		return r.AccountID
	}
	var binding *http.Cookie
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		if binding != nil {
			req.AddCookie(binding)
		}
		out := httptest.NewRecorder()
		s.Router.ServeHTTP(out, req)
		for _, c := range out.Result().Cookies() {
			if c.Name == "pastime_session_oidc" {
				binding = c
			}
		}
		return out
	}
	// signIn returns the account the sign-in landed in.
	signIn := func(subject string) int64 {
		t.Helper()
		issuer.subject = subject
		start := get("/auth/oidc/start?return_to=" + url.QueryEscape("/library"))
		if start.Code != 302 {
			t.Fatalf("start %d", start.Code)
		}
		code, state := issuer.authorize(t, start.Header().Get("Location"))
		out := get("/auth/oidc/callback?code=" + code + "&state=" + url.QueryEscape(state))
		if out.Header().Get("Location") != "/library" {
			t.Fatalf("sign-in refused: %v", out.Header())
		}
		for _, c := range out.Result().Cookies() {
			if c.Name == "pastime_session" && c.Value != "" {
				a, err := f.Accounts.Authenticate(ctx, c.Value, "session")
				if err != nil {
					t.Fatal(err)
				}
				return a.User.ID
			}
		}
		t.Fatal("no session")
		return 0
	}
	unclaimed := func(user int64) {
		t.Helper()
		var status string
		var claimed bool
		if err := f.Pool.QueryRow(ctx, `SELECT link_status,claimed_at IS NOT NULL FROM bloem_accounts WHERE instance_id=$1 AND account_id=$2`, instance.ID, user).Scan(&status, &claimed); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || claimed {
			t.Fatalf("managed account %d is %s, claimed %v", user, status, claimed)
		}
	}
	claims := func() int {
		t.Helper()
		var n int
		if err := f.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='bloem.account.claim'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	const (
		matching    = "6f1e2d3c-4b5a-4987-8a6b-5c4d3e2f1a0b"
		undeclared  = "7a6b5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d"
		wrongShape  = "8b7c6d5e-4f3a-4b2c-8d9e-0f1a2b3c4d5e"
		unknownUser = "9c8d7e6f-5a4b-4c3d-9e2f-1a0b9c8d7e6f"
	)
	matched := provision("matching", matching)
	notDeclared := provision("undeclared", undeclared)
	shaped := provision("wrong-shape", wrongShape)
	managed := map[int64]bool{matched: true, notDeclared: true, shaped: true}

	declare(bloem.SubjectUserUUID)
	if user := signIn(unknownUser); managed[user] {
		t.Fatal("a sign-in matching no managed identity landed in a managed account")
	}
	declare("")
	if user := signIn(undeclared); managed[user] {
		t.Fatal("a sign-in from an undeclared issuer claimed a managed account")
	}
	declare(bloem.SubjectHashedUserID)
	if user := signIn(wrongShape); managed[user] {
		t.Fatal("a subject outside the declared mode claimed a managed account")
	}
	for user := range managed {
		unclaimed(user)
	}
	if n := claims(); n != 0 {
		t.Fatalf("%d claims recorded for non-matching sign-ins", n)
	}
	// The same callback still claims once the identity and declaration match.
	declare(bloem.SubjectUserUUID)
	if user := signIn(matching); user != matched {
		t.Fatalf("matching sign-in landed in %d, want %d", user, matched)
	}
	unclaimed(notDeclared)
	unclaimed(shaped)
	if n := claims(); n != 1 {
		t.Fatalf("claims = %d, want 1", n)
	}
}
