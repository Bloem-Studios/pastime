package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOIDCBrowserBinding(t *testing.T) {
	s := &Server{origin: "https://pastime.example"}
	for _, value := range []string{"", "different", "expected"} {
		r := httptest.NewRequest("GET", "https://pastime.example/auth/oidc/callback?state=expected", nil)
		if value != "" {
			r.AddCookie(&http.Cookie{Name: s.cookieName() + "_oidc", Value: value})
		}
		if got := s.oidcBrowserMatches(r); got != (value == "expected") {
			t.Fatalf("cookie %q matched=%v", value, got)
		}
	}
}
