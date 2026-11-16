package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryHTTPKeepsProvisionAndViewerAuthoritySeparate(t *testing.T) {
	h := application(t)
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	h.server.secrets = box
	store := bloem.Store{Pool: h.Pool, Box: box}
	_, raw, err := store.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://example.invalid", Scope: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {h.OwnerRaw, 401}, {raw, 200}} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/bloem/library:pending", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal("machine boundary", w.Code, tc.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/bloem/library/sources", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	h.server.Router.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("machine became viewer", w.Code)
	}
	w = h.request(http.MethodGet, "/api/v1/bloem/library/sources", nil, h.OwnerRaw, "", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("unlinked owner", w.Code, w.Body.String())
	}
}

func TestNativeCopyImportHTTPRequiresLibraryWrite(t *testing.T) {
	h := application(t)
	for _, tc := range []struct {
		scope string
		want  int
	}{{"library:write", 422}, {"library:read", 403}, {"lists:write", 403}} {
		t.Run(tc.scope, func(t *testing.T) {
			_, raw, err := h.Accounts.MintToken(t.Context(), h.Owner, identity.TokenInput{Name: "Synthetic import scope", Scopes: []string{tc.scope}, ExpiresInDays: 1, Password: testkit.Password})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/bloem/library/copies:import", strings.NewReader(`{"request_id":"page","copy_id":"copy","entry_id":"0"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+raw)
			w := httptest.NewRecorder()
			h.server.Router.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
