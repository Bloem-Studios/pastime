package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

// OIDC configures single sign-on with one OpenID Connect provider, such as Authentik.
type OIDC struct {
	Issuer, ClientID, ClientSecret, Name string
	AutoCreate                           bool
}

func (o OIDC) enabled() bool { return o.Issuer != "" && o.ClientID != "" && o.ClientSecret != "" }

type oidcClient struct {
	mu       sync.Mutex
	provider *oidc.Provider
}

// provider discovers the issuer once; a failed discovery is retried on the next request.
func (s *Server) oidcProvider(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	if s.sso.provider == nil {
		p, err := oidc.NewProvider(ctx, s.oidc.Issuer)
		if err != nil {
			return nil, nil, fault.Unavailable
		}
		s.sso.provider = p
	}
	cfg := &oauth2.Config{ClientID: s.oidc.ClientID, ClientSecret: s.oidc.ClientSecret, Endpoint: s.sso.provider.Endpoint(),
		RedirectURL: s.origin + "/auth/oidc/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	return s.sso.provider, cfg, nil
}

func (s *Server) oidcRoutes() {
	s.Router.Get("/api/v1/auth/methods", func(w http.ResponseWriter, r *http.Request) {
		name := s.oidc.Name
		if name == "" {
			name = "single sign-on"
		}
		respond(w, 200, map[string]any{"password": true, "registration": s.publicRegistration, "oidc": map[string]any{"enabled": s.oidc.enabled(), "name": name, "start_url": "/auth/oidc/start"},
			"recovery": s.secrets.Available()})
	})
	s.Router.Get("/auth/oidc/start", func(w http.ResponseWriter, r *http.Request) {
		if !s.oidc.enabled() || s.accounts == nil {
			http.Redirect(w, r, "/login?error=sso_unavailable", http.StatusFound)
			return
		}
		if err := s.accounts.Limit(r.Context(), "oidc-start:"+remoteBucket(r), 30, time.Minute); err != nil {
			s.problem(w, r, err)
			return
		}
		target, err := s.oidcAuthURL(w, r, r.URL.Query().Get("return_to"), nil)
		if err != nil {
			http.Redirect(w, r, "/login?error=sso_unavailable", http.StatusFound)
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
	s.Router.Get("/auth/oidc/callback", s.oidcCallback)
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", householdSession))
		r.Get("/api/v1/me/oidc", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.accounts.OIDCIdentities(r.Context(), actor(r))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Post("/api/v1/me/oidc/link", func(w http.ResponseWriter, r *http.Request) {
			if !s.oidc.enabled() {
				s.problem(w, r, &fault.Error{Status: 503, Code: "sso_unavailable", Message: "Single sign-on is not configured on this server."})
				return
			}
			a := actor(r)
			target, err := s.oidcAuthURL(w, r, "/account?tab=security", &a)
			s.reply(w, r, map[string]string{"url": target}, err)
		})
		r.Delete("/api/v1/me/oidc", func(w http.ResponseWriter, r *http.Request) {
			if err := s.accounts.UnlinkOIDC(r.Context(), actor(r), r.URL.Query().Get("issuer")); err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
}

func (s *Server) oidcAuthURL(w http.ResponseWriter, r *http.Request, returnTo string, link *identity.Actor) (string, error) {
	ctx := r.Context()
	_, cfg, err := s.oidcProvider(ctx)
	if err != nil {
		return "", err
	}
	st, err := s.accounts.BeginOIDC(ctx, returnTo, link)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName() + "_oidc", Value: st.State, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.origin, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	challenge := sha256.Sum256([]byte(st.Verifier))
	return cfg.AuthCodeURL(st.State, oidc.Nonce(st.Nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256")), nil
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(code string) { http.Redirect(w, r, "/login?error="+url.QueryEscape(code), http.StatusFound) }
	if !s.oidc.enabled() || s.accounts == nil {
		fail("sso_unavailable")
		return
	}
	if !s.oidcBrowserMatches(r) {
		fail("sign_in_expired")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName() + "_oidc", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.origin, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	q := r.URL.Query()
	if q.Get("error") != "" {
		fail("sso_denied")
		return
	}
	st, err := s.accounts.ConsumeOIDC(r.Context(), q.Get("state"))
	if err != nil {
		fail("sign_in_expired")
		return
	}
	var link *identity.Actor
	if st.LinkUserID != nil {
		cookie, err := r.Cookie(s.cookieName())
		if err != nil {
			fail("sign_in_expired")
			return
		}
		a, err := s.accounts.Authenticate(r.Context(), cookie.Value, "session")
		if err != nil || a.User.ID != *st.LinkUserID || !bytes.Equal(a.Hash, st.LinkSessionHash) {
			fail("sign_in_expired")
			return
		}
		link = &a
	}
	provider, cfg, err := s.oidcProvider(r.Context())
	if err != nil {
		fail("sso_unavailable")
		return
	}
	token, err := cfg.Exchange(r.Context(), q.Get("code"), oauth2.SetAuthURLParam("code_verifier", st.Verifier))
	if err != nil {
		fail("sso_failed")
		return
	}
	rawID, _ := token.Extra("id_token").(string)
	idToken, err := provider.Verifier(&oidc.Config{ClientID: s.oidc.ClientID}).Verify(r.Context(), rawID)
	if err != nil || idToken.Nonce != st.Nonce {
		fail("sso_failed")
		return
	}
	var claims struct {
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	_ = idToken.Claims(&claims)
	_, raw, err := s.accounts.OIDCSignIn(identity.WithClient(r.Context(), r.UserAgent()), identity.OIDCClaims{Issuer: idToken.Issuer, Subject: idToken.Subject, Email: claims.Email,
		EmailVerified: claims.EmailVerified, PreferredUsername: claims.PreferredUsername, Name: claims.Name}, link, s.oidc.AutoCreate)
	if err != nil {
		var refused *identity.ClaimConflict
		if errors.As(err, &refused) {
			// Identifiers and the reason only; never the issuer, subject or email.
			s.logger.Warn("managed account claim refused", "reason", refused.Reason, "instance_id", refused.InstanceID, "source_account_id", refused.SourceAccountID, "user_id", refused.UserID)
		}
		fail(fault.Public(err).Code)
		return
	}
	s.setCookie(w, raw)
	http.Redirect(w, r, st.ReturnTo, http.StatusFound)
}

func (s *Server) oidcBrowserMatches(r *http.Request) bool {
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(s.cookieName() + "_oidc")
	return err == nil && state != "" && len(state) <= 128 && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) == 1
}
