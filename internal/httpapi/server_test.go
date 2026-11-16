package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func newTestServer(t testing.TB, db, cache Probe) *Server {
	t.Helper()
	s, err := New(db, cache, "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func ready(context.Context) error { return nil }

func TestContractCoversEveryOperation(t *testing.T) {
	s := newTestServer(t, ready, nil)
	exceptions := map[string]bool{
		"GET /docs": true, "GET /docs/": true, "GET /docs/licenses.txt": true, "GET /api/openapi.json": true, "GET /api/openapi.yaml": true,
		"GET /docs/assets/swagger-ui-bundle.js": true, "GET /docs/assets/swagger-ui.css": true,
		"GET /docs/assets/swagger-bootstrap.js": true, "GET /docs/assets/swagger-ui-bundle.js.LICENSE.txt": true,
		"GET /docs/assets/docs-theme.css": true, "GET /docs/assets/docs-theme.js": true,
	}
	actual := map[string]bool{}
	err := chi.Walk(s.Router, func(method, path string, h http.Handler, m ...func(http.Handler) http.Handler) error {
		key := method + " " + path
		if exceptions[key] {
			delete(exceptions, key)
			return nil
		}
		actual[key] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exceptions) != 0 {
		t.Fatalf("stale infrastructure exception: %v", exceptions)
	}
	for path, item := range s.Spec.Paths.Map() {
		for method := range item.Operations() {
			key := method + " " + path
			if !actual[key] {
				t.Errorf("documented but not implemented: %s", key)
			}
			delete(actual, key)
		}
	}
	if len(actual) != 0 {
		t.Fatalf("undocumented operations: %v", actual)
	}
}

func validateResponse(t *testing.T, s *Server, path string, rec *httptest.ResponseRecorder) {
	t.Helper()
	response := s.Spec.Paths.Value(path).Get.Responses.Value(strconv.Itoa(rec.Code))
	if response == nil {
		t.Fatalf("undocumented status %d", rec.Code)
	}
	schema := response.Value.Content["application/json"].Schema.Value
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if err := schema.VisitJSON(v); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessAndContracts(t *testing.T) {
	broken := func(context.Context) error { return errors.New("private-test-secret") }
	for _, tc := range []struct {
		name      string
		db, cache Probe
		code      int
		status    string
	}{
		{"no_cache", ready, nil, 200, "ready"},
		{"healthy", ready, ready, 200, "ready"},
		{"cache_down", ready, broken, 200, "degraded"},
		{"database_down", broken, ready, 503, "unavailable"},
		{"all_down", broken, broken, 503, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t, tc.db, tc.cache)
			for _, path := range []string{"/healthz", "/readyz", "/api/v1/info"} {
				rec := httptest.NewRecorder()
				s.Router.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
				validateResponse(t, s, path, rec)
				if strings.Contains(rec.Body.String(), "private-test-secret") {
					t.Fatal("dependency details leaked")
				}
				if path == "/readyz" {
					if rec.Code != tc.code {
						t.Fatalf("readiness %d", rec.Code)
					}
					var got Readiness
					_ = json.Unmarshal(rec.Body.Bytes(), &got)
					if string(got.Status) != tc.status {
						t.Fatalf("readiness %s", got.Status)
					}
				} else if rec.Code != 200 {
					t.Fatalf("unavailable dependency blocked %s", path)
				}
			}
		})
	}
}

func TestReadinessDeadline(t *testing.T) {
	s := newTestServer(t, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, nil)
	start := time.Now()
	rec := httptest.NewRecorder()
	s.Router.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 || time.Since(start) > 1200*time.Millisecond {
		t.Fatal("readiness did not respect its budget")
	}
}

func TestSwaggerAssetsAndCache(t *testing.T) {
	s := newTestServer(t, ready, nil)
	rec := httptest.NewRecorder()
	s.Router.ServeHTTP(rec, httptest.NewRequest("GET", "/docs/", nil))
	if rec.Code != 401 {
		t.Fatal("administration contract must require authentication")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatal("external connections not restricted")
	}
	boot := httptest.NewRecorder()
	s.Router.ServeHTTP(boot, httptest.NewRequest("GET", "/docs/assets/swagger-bootstrap.js", nil))
	for _, required := range []string{"validatorUrl: null", "persistAuthorization: false", "queryConfigEnabled: false"} {
		if !strings.Contains(boot.Body.String(), required) {
			t.Errorf("missing %s", required)
		}
	}
	for _, path := range []string{"/docs/assets/swagger-ui-bundle.js", "/docs/assets/swagger-ui.css"} {
		plain := httptest.NewRecorder()
		s.Router.ServeHTTP(plain, httptest.NewRequest("GET", path, nil))
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		zipped := httptest.NewRecorder()
		s.Router.ServeHTTP(zipped, req)
		reader, err := gzip.NewReader(bytes.NewReader(zipped.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, plain.Body.Bytes()) {
			t.Fatal("gzip changed asset")
		}
		if zipped.Body.Len() >= plain.Body.Len() {
			t.Fatal("asset not compressed")
		}
		if zipped.Header().Get("ETag") == plain.Header().Get("ETag") {
			t.Fatal("representation ETags must differ")
		}
		req.Header.Set("If-None-Match", zipped.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		s.Router.ServeHTTP(cached, req)
		if cached.Code != 304 || cached.Body.Len() != 0 {
			t.Fatal("conditional asset response failed")
		}
	}
	legacy := httptest.NewRecorder()
	s.Router.ServeHTTP(legacy, httptest.NewRequest("GET", "/api/v1/user/preferences/", nil))
	if legacy.Code != 404 {
		t.Fatal("unimplemented legacy endpoint should not succeed")
	}
}

func TestGzipNegotiation(t *testing.T) {
	for _, tc := range []struct {
		h    string
		want bool
	}{{"gzip", true}, {"br, gzip;q=0.8", true}, {"gzip;q=0", false}, {"gzip;q=nonsense", false}, {"gzip;q=NaN", false}, {"gzip;q=2", false}, {"br", false}, {"", false}} {
		if got := acceptsGzip(tc.h); got != tc.want {
			t.Errorf("%q: %v", tc.h, got)
		}
	}
}

func BenchmarkNativeInfo(b *testing.B) {
	s := newTestServer(b, ready, nil)
	req := httptest.NewRequest("GET", "/api/v1/info", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		s.Router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			b.Fatal(rec.Code)
		}
	}
}
