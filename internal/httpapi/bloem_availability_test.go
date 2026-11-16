package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAvailabilityHTTPKeepsMachineAndViewerCredentialsSeparate(t *testing.T) {
	h := application(t)
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	h.server.secrets = box
	s := bloem.Store{Pool: h.Pool, Box: box}
	_, raw, err := s.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://example.invalid", Scope: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {h.OwnerRaw, 401}, {raw, 200}} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/bloem/availability:pending", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("machine credential boundary: %d wanted%d", w.Code, tc.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/1/bloem-availability", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	h.server.Router.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("provision credential read viewer data", w.Code)
	}
	w = h.request(http.MethodGet, "/api/v1/catalog/1/bloem-availability", nil, h.OwnerRaw, "", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("unlinked owner: %d %s", w.Code, w.Body.String())
	}
}
