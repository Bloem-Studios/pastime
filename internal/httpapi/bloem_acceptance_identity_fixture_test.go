package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// serveIdentityFixture adds the controller routes of the identity acceptance: the
// managed accounts as provisioning left them, and a browser sign-in through the
// synthetic provider as a chosen subject. Handlers report failures to the
// controller instead of failing the fixture, which runs them off the test goroutine.
func serveIdentityFixture(s *Server, f *testkit.Fixture, sso *fakeIssuer, instance string) {
	s.Router.Get("/fixture/accounts", func(w http.ResponseWriter, r *http.Request) {
		rows, err := f.Pool.Query(r.Context(), `SELECT a.source_account_id,a.account_id,a.active,a.owns_account,a.link_status,a.claimed_at IS NOT NULL,a.identity_issuer,a.identity_subject,
				(SELECT count(*) FROM bloem_consumption_receipts c JOIN bloem_profiles p ON p.instance_id=c.instance_id AND p.profile_id=c.profile_id
					WHERE p.instance_id=a.instance_id AND p.source_account_id=a.source_account_id AND p.account_id=a.account_id)
			FROM bloem_accounts a WHERE a.instance_id=$1 ORDER BY a.source_account_id`, instance)
		if err != nil {
			http.Error(w, "fixture query failed", 500)
			return
		}
		defer rows.Close()
		type account struct {
			SourceAccountID string
			UserID          int64
			Active, Owns    bool
			LinkStatus      string
			Claimed         bool
			Issuer, Subject string
			Receipts        int
		}
		out := []account{}
		for rows.Next() {
			var a account
			if err := rows.Scan(&a.SourceAccountID, &a.UserID, &a.Active, &a.Owns, &a.LinkStatus, &a.Claimed, &a.Issuer, &a.Subject, &a.Receipts); err != nil {
				http.Error(w, "fixture scan failed", 500)
				return
			}
			out = append(out, a)
		}
		if rows.Err() != nil {
			http.Error(w, "fixture rows failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	var signIns sync.Mutex
	s.Router.Post("/fixture/sign-in", func(w http.ResponseWriter, r *http.Request) {
		signIns.Lock()
		defer signIns.Unlock()
		var in struct{ Subject string }
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Subject == "" {
			http.Error(w, "subject required", 400)
			return
		}
		var binding *http.Cookie
		browse := func(path string) *httptest.ResponseRecorder {
			// A fresh request context: the router would otherwise reuse this
			// handler's routing state.
			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "127.0.0.1:5000"
			if binding != nil {
				req.AddCookie(binding)
			}
			rec := httptest.NewRecorder()
			s.Router.ServeHTTP(rec, req)
			for _, c := range rec.Result().Cookies() {
				if c.Name == "pastime_session_oidc" {
					binding = c
				}
			}
			return rec
		}
		sso.subject = in.Subject
		start := browse("/auth/oidc/start?return_to=" + url.QueryEscape("/library"))
		location, err := url.Parse(start.Header().Get("Location"))
		if start.Code != 302 || err != nil || !strings.HasPrefix(location.String(), sso.URL+"/authorize") {
			http.Error(w, "sign-in did not reach the provider: "+strconv.Itoa(start.Code)+" "+start.Body.String(), 500)
			return
		}
		// The provider's login page: approve and hand back a code bound to the request.
		q := location.Query()
		code := "code-" + q.Get("state")[:8]
		sso.nonces[code], sso.pkce[code] = q.Get("nonce"), q.Get("code_challenge")
		callback := browse("/auth/oidc/callback?code=" + code + "&state=" + url.QueryEscape(q.Get("state")))
		var result struct {
			Location string
			UserID   int64
		}
		result.Location = callback.Header().Get("Location")
		for _, c := range callback.Result().Cookies() {
			if c.Name == "pastime_session" && c.Value != "" {
				actor, err := f.Accounts.Authenticate(r.Context(), c.Value, "session")
				if err != nil {
					http.Error(w, "session invalid", 500)
					return
				}
				result.UserID = actor.User.ID
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
