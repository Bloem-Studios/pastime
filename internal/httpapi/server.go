package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/admin"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/config"
	"github.com/Bloem-Studios/pastime/internal/connections"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/notify"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	thirdparty "github.com/Bloem-Studios/pastime/third_party"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:generate go tool oapi-codegen -generate types -package httpapi -o types.gen.go openapi.yaml
//go:embed openapi.yaml assets/*
var files embed.FS

type Probe func(context.Context) error

type Server struct {
	Router             *chi.Mux
	Spec               *openapi3.T
	database           Probe
	valkey             Probe
	accounts           *identity.Store
	library            library.Store
	catalog            *catalog.Service
	connections        connections.Store
	imports            imports.Store
	admin              *admin.Store
	notify             notify.Store
	secrets            *sealed.Box
	oidc               OIDC
	publicRegistration bool
	sso                oidcClient
	origin             string
	logger             *slog.Logger
	// syncPerMinute limits each Silo sync credential on its own.
	syncPerMinute int
	syncFaults    *faultCounter
}

func New(database, valkey Probe, version string, options ...Options) (*Server, error) {
	if database == nil {
		return nil, fmt.Errorf("database readiness probe is required")
	}
	if version == "" {
		version = "dev"
	}
	yaml, err := files.ReadFile("openapi.yaml")
	if err != nil {
		return nil, err
	}
	spec, err := openapi3.NewLoader().LoadFromData(yaml)
	if err != nil {
		return nil, err
	}
	if err = spec.Validate(context.Background()); err != nil {
		return nil, err
	}
	jsonSpec, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	opt := Options{PublicOrigin: "http://127.0.0.1:8080", Logger: slog.Default()}
	if len(options) > 0 {
		opt = options[0]
	}
	if opt.PublicOrigin == "" {
		opt.PublicOrigin = "http://127.0.0.1:8080"
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	if opt.SiloSyncRequestsPerMinute <= 0 {
		opt.SiloSyncRequestsPerMinute = 600
	}
	origin, err := config.NormalizeOrigin(opt.PublicOrigin)
	if err != nil {
		return nil, err
	}
	s := &Server{Router: chi.NewRouter(), Spec: spec, database: database, valkey: valkey, origin: origin, publicRegistration: opt.PublicRegistration, logger: opt.Logger, library: library.Store{Pool: opt.Pool}, catalog: opt.Catalog, connections: connections.Store{Pool: opt.Pool}, imports: imports.Store{Pool: opt.Pool, Catalog: opt.Catalog, Library: library.Store{Pool: opt.Pool}, Box: opt.Secrets, Creds: opt.Services, AllowPrivate: opt.ServicesPrivate}, admin: &admin.Store{Pool: opt.Pool, Catalog: opt.Catalog, CacheProbe: valkey, Setup: admin.Setup{Origin: origin, SecretKey: opt.Secrets.Available(), Valkey: valkey != nil, OIDC: opt.OIDC.enabled(), NotifyPrivate: opt.NotifyPrivate}},
		notify: notify.Store{Pool: opt.Pool, Box: opt.Secrets, AllowPrivate: opt.NotifyPrivate}, secrets: opt.Secrets, oidc: opt.OIDC,
		syncPerMinute: opt.SiloSyncRequestsPerMinute, syncFaults: &faultCounter{logger: opt.Logger}}
	if opt.Catalog != nil && opt.Catalog.Cache != nil {
		s.library.Cache = opt.Catalog.Cache
	}
	if opt.Pool != nil {
		s.accounts = identity.New(opt.Pool)
		s.accounts.Secrets = opt.Secrets
		// An unlinked sign-in may claim the integration-managed account of its verified identity.
		s.accounts.ManagedClaim = bloem.Store{Pool: opt.Pool}.ClaimForLogin
	}
	var trusted []netip.Prefix
	for _, raw := range strings.Split(opt.TrustedProxies, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS must contain IP network prefixes")
		}
		trusted = append(trusted, prefix)
	}
	r := s.Router
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.RemoteAddr = proxyRemoteAddr(r, trusted)
			w.Header().Set("X-Request-ID", idempotencyKey())
			next.ServeHTTP(w, r)
		})
	})
	r.Use(deadlines)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; manifest-src 'self'; worker-src 'self'")
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, Liveness{Status: Alive}) })
	r.Get("/readyz", s.readiness)
	r.Get("/api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, ServerInfo{Name: Pastime, Stage: Preview, Version: version})
	})
	r.Group(func(r chi.Router) {
		r.Use(s.authenticated("", true, true))
		r.Get("/api/openapi.json", static(jsonSpec, "application/json", false))
		r.Get("/api/openapi.yaml", static(yaml, "application/yaml", false))
		r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/docs/", http.StatusTemporaryRedirect)
		})
		r.Get("/docs/", static([]byte(docsHTML), "text/html; charset=utf-8", false))
	})
	r.Get("/docs/licenses.txt", static(thirdparty.Notices, "text/plain; charset=utf-8", false))
	assets := map[string]string{
		"swagger-ui-bundle.js": "text/javascript; charset=utf-8", "swagger-ui.css": "text/css; charset=utf-8",
		"swagger-ui-bundle.js.LICENSE.txt": "text/plain; charset=utf-8", "swagger-bootstrap.js": "text/javascript; charset=utf-8",
		"docs-theme.css": "text/css; charset=utf-8", "docs-theme.js": "text/javascript; charset=utf-8",
	}
	for name, contentType := range assets {
		data, err := files.ReadFile("assets/" + name)
		if err != nil {
			return nil, err
		}
		r.Get("/docs/assets/"+name, static(data, contentType, true))
	}
	s.applicationRoutes()
	if err = s.webRoutes(); err != nil {
		return nil, err
	}
	return s, nil
}

// longRequests are the transfers that legitimately outlast the interactive budget:
// uploading an export archive and streaming a full library export.
func longRequest(r *http.Request) bool {
	return (r.Method == http.MethodPost && r.URL.Path == "/api/v1/imports") ||
		(r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/export"))
}

// deadlines bounds every request. Long transfers get five minutes, extending the
// connection's read and write deadlines to match; everything else gets five seconds.
func deadlines(next http.Handler) http.Handler {
	short := middleware.Timeout(5 * time.Second)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !longRequest(r) {
			short.ServeHTTP(w, r)
			return
		}
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(5 * time.Minute))
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Minute))
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 750*time.Millisecond)
	defer cancel()
	type observation struct {
		dependency string
		ok         bool
	}
	results := make(chan observation, 2)
	go func() { results <- observation{"database", s.database(ctx) == nil} }()
	count := 1
	out := Readiness{Status: ReadinessStatusReady, Database: ReadinessDatabaseUnavailable, Valkey: ReadinessValkeyDisabled}
	if s.valkey != nil {
		count++
		out.Valkey = ReadinessValkeyUnavailable
		go func() { results <- observation{"valkey", s.valkey(ctx) == nil} }()
	}
observations:
	for range count {
		select {
		case got := <-results:
			if got.dependency == "database" && got.ok {
				out.Database = ReadinessDatabaseReady
			}
			if got.dependency == "valkey" && got.ok {
				out.Valkey = ReadinessValkeyReady
			}
		case <-ctx.Done():
			break observations
		}
	}
	status := http.StatusOK
	if out.Valkey == ReadinessValkeyUnavailable {
		out.Status = ReadinessStatusDegraded
	}
	if out.Database == ReadinessDatabaseUnavailable {
		out.Status = ReadinessStatusUnavailable
		status = http.StatusServiceUnavailable
	}
	respond(w, status, out)
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		pieces := strings.Split(strings.TrimSpace(part), ";")
		if strings.TrimSpace(pieces[0]) != "gzip" {
			continue
		}
		q := 1.0
		for _, option := range pieces[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(option), "=")
			if ok && strings.EqualFold(key, "q") {
				n, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return false
				}
				q = n
			}
		}
		return q > 0 && q <= 1
	}
	return false
}

// Static representations are compressed once at startup, not on every request.
func static(data []byte, contentType string, cacheable bool) http.HandlerFunc {
	policy := ""
	if cacheable {
		policy = "public, max-age=0, must-revalidate"
	}
	return staticWith(data, contentType, policy)
}

// staticWith serves a fixed representation; an empty policy keeps the global no-store.
func staticWith(data []byte, contentType, policy string) http.HandlerFunc {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	_, _ = z.Write(data)
	_ = z.Close()
	compressed := b.Bytes()
	plainTag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))
	gzipTag := fmt.Sprintf(`"%x"`, sha256.Sum256(compressed))
	return func(w http.ResponseWriter, r *http.Request) {
		body, tag := data, plainTag
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			body, tag = compressed, gzipTag
			w.Header().Set("Content-Encoding", "gzip")
		}
		if policy != "" {
			w.Header().Set("Cache-Control", policy)
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}
}

const docsHTML = `<!doctype html><html lang="en" data-theme="dark"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>API reference · Pastime</title>` +
	`<script src="/docs/assets/docs-theme.js"></script><link rel="stylesheet" href="/docs/assets/swagger-ui.css"><link rel="stylesheet" href="/docs/assets/docs-theme.css"></head><body>` +
	`<header class="docs-bar"><a class="docs-brand" href="/"><svg viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="9" fill="currentColor"/>` +
	`<rect x="8" y="9" width="4" height="15" rx="1.4" fill="var(--canvas)"/><rect x="14" y="6.5" width="4" height="17.5" rx="1.4" fill="var(--canvas)" opacity=".82"/>` +
	`<rect x="20.2" y="9.8" width="4" height="14.4" rx="1.4" fill="var(--canvas)" opacity=".64" transform="rotate(9 22.2 17)"/></svg>Pastime <span>API reference</span></a>` +
	`<nav aria-label="Documentation"><a href="/api/openapi.json">OpenAPI JSON</a><a href="/">Back to the app</a></nav></header>` +
	`<main id="swagger-ui"></main><footer class="docs-footer"><a href="/docs/licenses.txt">Third-party notices</a></footer>` +
	`<script src="/docs/assets/swagger-ui-bundle.js"></script><script src="/docs/assets/swagger-bootstrap.js"></script></body></html>`
