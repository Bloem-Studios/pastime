package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	Pool           *pgxpool.Pool
	PublicOrigin   string
	TrustedProxies string
	Logger         *slog.Logger
	Catalog        *catalog.Service
	// Secrets encrypts stored secrets; nil disables the features that need it.
	Secrets *sealed.Box
	// NotifyPrivate lets notification destinations use private network addresses.
	NotifyPrivate bool
	// Services holds application keys for linked services; ServicesPrivate lets members
	// link services on private networks.
	Services           imports.Credentials
	ServicesPrivate    bool
	OIDC               OIDC
	PublicRegistration bool
	// SiloSyncRequestsPerMinute limits each Silo sync credential; zero means 600.
	SiloSyncRequestsPerMinute int
}
type actorKey struct{}

func actor(r *http.Request) identity.Actor {
	a, _ := r.Context().Value(actorKey{}).(identity.Actor)
	return a
}
func (s *Server) problem(w http.ResponseWriter, r *http.Request, err error) {
	// A client that navigated away cancels its request; that is not a server failure.
	if errors.Is(r.Context().Err(), context.Canceled) {
		s.logger.Debug("request cancelled by client", "method", r.Method, "route", chi.RouteContext(r.Context()).RoutePattern())
		return
	}
	p := fault.Public(err)
	if p.Status >= 400 && p.Status < 500 {
		s.countSyncFault(chi.RouteContext(r.Context()), p.Code)
	}
	if (p.Status == 500 || p.Status == 503) && !expectedRetry(p) {
		attrs := []any{"method", r.Method, "route", chi.RouteContext(r.Context()).RoutePattern(), "code", p.Code, "request_id", w.Header().Get("X-Request-ID")}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			attrs = append(attrs, "sqlstate", pgErr.Code)
		}
		s.logger.Error("request failed", attrs...)
	} else if expectedRetry(p) {
		s.logger.Debug("request deferred", "method", r.Method, "route", chi.RouteContext(r.Context()).RoutePattern(), "code", p.Code)
	}
	if retry := retryAfter(p); retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	respond(w, p.Status, map[string]any{"code": p.Code, "message": p.Message})
}

// expectedRetry reports a 503 that is part of normal operation rather than a failure:
// a show whose episodes are still loading, or an episode the provider does not list
// yet. Silo retries these routinely, so they are not logged as errors.
func expectedRetry(p *fault.Error) bool {
	return p.Status == 503 && (p.Code == "catalog_loading" || p.Code == "catalog_pending")
}

// retryAfter is the Retry-After value, in seconds, of a retryable problem: a rate limit,
// a show whose episodes are still loading, or an episode the provider may not list yet
// (the daily catalog refresh usually lists it within hours).
func retryAfter(p *fault.Error) string {
	switch {
	case p.Status == 429:
		return "60"
	case p.Status == 503 && p.Code == "catalog_loading":
		return "60"
	case p.Status == 503 && p.Code == "catalog_pending":
		return "21600"
	}
	return ""
}
func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var out T
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return out, &fault.Error{Status: 415, Code: "unsupported_media_type", Message: "Send application/json."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err = d.Decode(&out); err != nil {
		return out, fault.Invalid("Send a valid JSON object with supported fields.")
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return out, fault.Invalid("Send one JSON object only.")
	}
	return out, nil
}
func (s *Server) cookieName() string {
	if strings.HasPrefix(s.origin, "https://") {
		return "__Host-pastime_session"
	}
	return "pastime_session"
}
func (s *Server) setCookie(w http.ResponseWriter, raw string) {
	maxAge := 604800
	expires := time.Now().Add(7 * 24 * time.Hour)
	if raw == "" {
		maxAge = -1
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: raw, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.origin, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: maxAge, Expires: expires})
}
func (s *Server) originAllowed(r *http.Request) bool {
	// Origin is configured, never inferred from forwarded headers or untrusted Host.
	return r.Header.Get("Origin") == s.origin && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

// access is who besides a scoped credential may use a route group.
type access int

const (
	// anyCredential: any session or token holding the group's scope.
	anyCredential access = iota
	// browserSession: a browser session of either kind, the account's own or a
	// profile session acting for its one profile.
	browserSession
	// householdCredential: the account's own session or a scoped token, never a
	// profile session. For things shared between accounts that tokens also
	// reach; the store also refuses tokens bound to another profile
	// (identity.ForHousehold).
	householdCredential
	// householdSession: only the account's own session.
	householdSession
	// adminSession: the account's own session of an owner or administrator.
	adminSession
)

// authenticated is the older form of authenticatedAs. sessionOnly means the
// household's own session, so a group nobody has decided for profile sessions
// stays closed to them.
func (s *Server) authenticated(scope string, sessionOnly, admin bool) func(http.Handler) http.Handler {
	need := anyCredential
	if sessionOnly {
		need = householdSession
	}
	if admin {
		need = adminSession
	}
	return s.authenticatedAs(scope, need)
}

func (s *Server) authenticatedAs(scope string, need access) func(http.Handler) http.Handler {
	admin := need == adminSession
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.accounts == nil {
				s.problem(w, r, fault.Unauthenticated)
				return
			}
			var raw, kind string
			cookieCount := 0
			for _, c := range r.Cookies() {
				if c.Name == s.cookieName() {
					raw = c.Value
					kind = identity.KindSession
					cookieCount++
				}
			}
			auth := r.Header.Values("Authorization")
			if cookieCount > 1 || len(auth) > 1 || (len(auth) > 0 && cookieCount > 0) {
				s.problem(w, r, fault.Unauthenticated)
				return
			}
			if len(auth) == 1 {
				var ok bool
				raw, ok = strings.CutPrefix(auth[0], "Bearer ")
				if !ok {
					s.problem(w, r, fault.Unauthenticated)
					return
				}
				kind = identity.KindToken
			}
			a, err := s.accounts.Authenticate(r.Context(), raw, kind)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			values := r.Header.Values("X-Pastime-Profile-ID")
			if strings.HasPrefix(r.URL.Path, "/api/v1/export/") || strings.HasPrefix(r.URL.Path, "/api/v1/export-files/") {
				if query, ok := r.URL.Query()["profile_id"]; ok {
					if len(query) != 1 || (len(values) > 0 && (len(values) != 1 || values[0] != query[0])) {
						s.problem(w, r, fault.NotFound)
						return
					}
					values = query
				}
			}
			if len(values) > 0 && !admin {
				if len(values) != 1 {
					s.problem(w, r, fault.NotFound)
					return
				}
				id, parseErr := identity.ParseID(values[0])
				if parseErr != nil {
					s.problem(w, r, parseErr)
					return
				}
				a, err = s.accounts.SelectProfile(r.Context(), a, id)
				if err != nil {
					s.problem(w, r, err)
					return
				}
			}
			refused := scope != "" && !a.Allows(scope)
			switch need {
			case browserSession:
				refused = refused || !a.Browser()
			case householdCredential:
				refused = refused || (a.Browser() && !a.Household())
			case householdSession:
				refused = refused || !a.Household()
			case adminSession:
				refused = refused || !a.IsAdmin()
			}
			if refused {
				s.problem(w, r, fault.Forbidden)
				return
			}
			if r.Method != "GET" && r.Method != "HEAD" && a.Browser() {
				if !s.originAllowed(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(a.CSRF)) != 1 {
					s.problem(w, r, fault.Forbidden)
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, a)))
		})
	}
}
func remoteBucket(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	ip = ip.Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String()
	}
	return ip.String()
}
func (s *Server) publicPost(label string, limit int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.originAllowed(r) {
			s.problem(w, r, fault.Forbidden)
			return
		}
		if s.accounts == nil {
			s.problem(w, r, fault.Unavailable)
			return
		}
		if err := s.accounts.Limit(r.Context(), label+":"+remoteBucket(r), limit, time.Minute); err != nil {
			s.problem(w, r, err)
			return
		}
		next(w, r)
	}
}
func (s *Server) accountLimit(w http.ResponseWriter, r *http.Request, label string, max int) bool {
	// A profile session counts against its own login name, which never equals an
	// account username, so it can't use up the primary user's allowance.
	if err := s.accounts.Limit(r.Context(), label+":"+actor(r).PublicUser().Username, max, time.Minute); err != nil {
		s.problem(w, r, err)
		return false
	}
	return true
}

// passwordLimit charges the per-account budget that every check of the signed-in
// person's current password shares, so the routes' budgets never add up.
func (s *Server) passwordLimit(w http.ResponseWriter, r *http.Request) bool {
	return s.accountLimit(w, r, "password", 6)
}
func (s *Server) applicationRoutes() {
	r := s.Router
	r.Post("/api/v1/login", s.publicPost("login", 12, s.login))
	r.Post("/api/v1/register", s.publicPost("register", 5, s.register))
	r.Post("/api/v1/invitations/preview", s.publicPost("invitation-preview", 30, s.previewInvite))
	r.Post("/api/v1/invitations/claim", s.publicPost("invitation-claim", 12, s.claimInvite))
	r.Group(func(r chi.Router) {
		r.Use(s.authenticated("profile:read", false, false))
		r.Get("/api/v1/me", s.me)
		r.Get("/api/v1/profiles", s.trackingProfiles)
	})
	r.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/session", s.session)
		r.Post("/api/v1/logout", s.logout)
		r.Post("/api/v1/me/password", s.changePassword)
		r.Get("/api/v1/me/sessions", s.sessions)
		r.Delete("/api/v1/me/sessions/{credentialID}", s.revokeSession)
		r.Get("/api/v1/me/tokens", s.tokens)
		r.Post("/api/v1/me/tokens", s.mintToken)
		r.Delete("/api/v1/me/tokens/{credentialID}", s.revokeToken)
	})
	r.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", householdSession))
		r.Patch("/api/v1/me", s.updateProfile)
		r.Get("/api/v1/profile-logins", s.profileLogins)
		r.Put("/api/v1/profiles/{profileID}/login", s.setProfileLogin)
		r.Delete("/api/v1/profiles/{profileID}/login", s.removeProfileLogin)
	})
	r.Group(func(r chi.Router) {
		r.Use(s.authenticated("", true, true))
		r.Get("/api/admin/v1/users", s.users)
		r.Post("/api/admin/v1/users", s.createUser)
		r.Get("/api/admin/v1/users/{userID}", s.getUser)
		r.Patch("/api/admin/v1/users/{userID}", s.updateUser)
		r.Post("/api/admin/v1/users/{userID}/reset-link", s.resetLink)
		r.Post("/api/admin/v1/users/{userID}/revoke-access", s.revokeAccess)
		r.Get("/api/admin/v1/invitations", s.invitations)
		r.Post("/api/admin/v1/invitations", s.invite)
		r.Delete("/api/admin/v1/invitations/{invitationID}", s.revokeInvitation)
		r.Get("/api/admin/v1/audit", s.audit)
		r.Get("/api/admin/v1/overview", s.adminOverview)
		r.Get("/api/admin/v1/jobs", s.adminJobs)
		r.Get("/api/admin/v1/job-kinds", s.adminJobKinds)
		r.Post("/api/admin/v1/jobs/{jobID}/retry", s.adminRetryJob)
		r.Post("/api/admin/v1/jobs/{jobID}/cancel", s.adminCancelJob)
		r.Get("/api/admin/v1/catalog", s.adminCatalog)
		r.Post("/api/admin/v1/catalog/refresh-stale", s.adminRefreshStale)
		r.Get("/api/admin/v1/imports", s.adminImports)
		r.Get("/api/admin/v1/checks", s.adminChecks)
	})
	s.libraryRoutes()
	s.organizeRoutes()
	s.notifyRoutes()
	s.savedViewRoutes()
	s.ratingRoutes()
	s.linkRoutes()
	s.kosyncRoutes()
	s.discoveryRoutes()
	s.editionRoutes()
	s.collabRoutes()
	s.listSourceRoutes()
	s.changeRoutes()
	s.mcpRoutes()
	s.oidcRoutes()
	s.recoveryRoutes()
	s.deviceRoutes()
	s.exportRoutes()
	s.catalogRoutes()
	s.checkinRoutes()
	s.insightRoutes()
	s.connectionRoutes()
	s.compatRoutes()
	s.bloemRoutes()
	s.transferRoutes()
}
