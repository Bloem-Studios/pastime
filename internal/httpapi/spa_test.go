package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebApplicationRoutes(t *testing.T) {
	s := newTestServer(t, ready, nil)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	for _, path := range []string{"/", "/login", "/join", "/library/42"} {
		w := get(path)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `id="root"`) {
			t.Fatalf("%s: status %d type %q", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := get("/api/v1/does-not-exist"); w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("unknown API path must stay a JSON 404, got %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	// Its own code: the Silo plugin reads 404 not_found on an event as a definite
	// refusal, and a missing route must not end a history export.
	for _, path := range []string{"/api/v1/does-not-exist", "/api/v1/integrations/bloem/events:moved"} {
		w := httptest.NewRecorder()
		s.Router.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader("{}")))
		if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"unknown_route"`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := get("/assets/missing-0000.js"); w.Code != 404 {
		t.Fatalf("missing asset must not fall back to index.html, got %d", w.Code)
	}
	if w := get("/theme.js"); w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "must-revalidate") {
		t.Fatalf("theme.js: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
}
