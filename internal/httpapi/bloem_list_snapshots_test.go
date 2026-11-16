package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListSnapshotHTTPRequiresMachineAuthority(t *testing.T) {
	h := application(t)
	for _, path := range []string{"/api/v1/integrations/bloem/lists:resolve", "/api/v1/integrations/bloem/lists:page", "/api/v1/integrations/bloem/lists:attest"} {
		for _, token := range []string{"", h.OwnerRaw} {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
			r.Header.Set("Content-Type", "application/json")
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			h.server.Router.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
}
