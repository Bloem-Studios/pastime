package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/testkit"
	"github.com/go-jose/go-jose/v4"
)

// fakeIssuer is a minimal OpenID provider: discovery, keys and a token endpoint that
// checks PKCE and returns a signed ID token for whichever subject the test chooses.
type fakeIssuer struct {
	*httptest.Server
	key     *rsa.PrivateKey
	subject string
	nonces  map[string]string // code -> nonce
	pkce    map[string]string // code -> challenge
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIssuer{key: key, subject: "sub-1", nonces: map[string]string{}, pkce: map[string]string{}}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token",
			"jwks_uri": f.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := r.Form.Get("code")
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if f.pkce[code] == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.pkce[code] {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		claims, _ := json.Marshal(map[string]any{"iss": f.URL, "sub": f.subject, "aud": "pastime", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"nonce": f.nonces[code], "email": f.subject + "@example.test", "email_verified": true, "preferred_username": "Sso.Person"})
		jws, _ := signer.Sign(claims)
		token, _ := jws.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": token, "expires_in": 3600})
	})
	return f
}

// authorize plays the provider's login page: it accepts the redirect and returns a code.
func (f *fakeIssuer) authorize(t *testing.T, location string) (code, state string) {
	u, err := url.Parse(location)
	if err != nil || !strings.HasPrefix(location, f.URL+"/authorize") {
		t.Fatalf("not sent to the provider: %s", location)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
		t.Fatalf("missing PKCE or nonce: %s", location)
	}
	code = "code-" + q.Get("state")[:8]
	f.nonces[code], f.pkce[code] = q.Get("nonce"), q.Get("code_challenge")
	return code, q.Get("state")
}

func TestSingleSignOnLinksCreatesAndRefuses(t *testing.T) {
	issuer := newFakeIssuer(t)
	defer issuer.Close()
	f := testkit.New(t)
	build := func(auto bool) *Server {
		s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
			OIDC: OIDC{Issuer: issuer.URL, ClientID: "pastime", ClientSecret: "s", Name: "Authentik", AutoCreate: auto}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	var binding *http.Cookie
	get := func(s *Server, path, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "pastime_session", Value: cookie})
		}
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
	signIn := func(s *Server, returnTo string) *httptest.ResponseRecorder {
		start := get(s, "/auth/oidc/start?return_to="+url.QueryEscape(returnTo), "")
		if start.Code != 302 {
			t.Fatalf("start %d %s", start.Code, start.Body.String())
		}
		code, state := issuer.authorize(t, start.Header().Get("Location"))
		return get(s, "/auth/oidc/callback?code="+code+"&state="+url.QueryEscape(state), "")
	}
	strict := build(false)
	// An unlinked identity is refused when accounts are not created automatically.
	if out := signIn(strict, "/library"); !strings.Contains(out.Header().Get("Location"), "error=identity_unlinked") {
		t.Fatalf("unlinked identity signed in: %v", out.Header())
	}
	// Linking from a signed-in account, then signing in through the provider.
	ctx := context.Background()
	h := &harness{f, strict, t}
	link := h.request("POST", "/api/v1/me/oidc/link", nil, h.OwnerRaw, h.Owner.CSRF, "http://127.0.0.1:8080", "")
	requireCode(t, link, 200)
	for _, c := range link.Result().Cookies() {
		if c.Name == "pastime_session_oidc" {
			binding = c
		}
	}
	var target struct{ URL string }
	_ = json.Unmarshal(link.Body.Bytes(), &target)
	code, state := issuer.authorize(t, target.URL)
	if out := get(strict, "/auth/oidc/callback?code="+code+"&state="+url.QueryEscape(state), h.OwnerRaw); out.Header().Get("Location") != "/account?tab=security" {
		t.Fatalf("link: %v", out.Header())
	}
	out := signIn(strict, "https://evil.example/")
	if out.Code != 302 || out.Header().Get("Location") != "/" || len(out.Result().Cookies()) != 2 {
		t.Fatalf("linked sign-in: %d %v", out.Code, out.Header())
	}
	if a, err := f.Accounts.Authenticate(ctx, out.Result().Cookies()[1].Value, "session"); err != nil || a.User.ID != h.Owner.User.ID {
		t.Fatalf("session belongs to the wrong account: %v", err)
	}
	// A used state cannot be replayed.
	if replay := get(strict, "/auth/oidc/callback?code="+code+"&state="+url.QueryEscape(state), ""); !strings.Contains(replay.Header().Get("Location"), "sign_in_expired") {
		t.Fatalf("state replayed: %v", replay.Header())
	}
	// With automatic accounts, a new identity becomes a member.
	issuer.subject = "sub-2"
	auto := build(true)
	out = signIn(auto, "/library")
	if out.Header().Get("Location") != "/library" {
		t.Fatalf("auto create: %v", out.Header())
	}
	var role, username string
	if err := f.Pool.QueryRow(ctx, "SELECT u.role,u.username FROM users u JOIN oidc_identities o ON o.user_id=u.id WHERE o.subject='sub-2'").Scan(&role, &username); err != nil || role != "member" || username != "sso.person" {
		t.Fatalf("created account: %s %s %v", role, username, err)
	}
}
