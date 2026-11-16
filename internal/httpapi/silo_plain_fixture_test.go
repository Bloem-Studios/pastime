package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"github.com/go-chi/chi/v5"
)

// syncFixture is a registered Silo instance with a source account whose profiles get
// plugin sync credentials (ptbs_), as Silo plugin 0.1.14 provisioned them.
type syncFixture struct {
	*harness
	store     bloem.Store
	instance  bloem.Instance
	provision string
	account   string
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	h := application(t)
	box, err := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	h.server.secrets = box
	h.server.catalog = &catalog.Service{Pool: h.Pool, Providers: map[string]provider.Provider{"tmdb": syntheticTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s := bloem.Store{Pool: h.Pool, Box: box}
	instance, raw, err := s.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://silo.example", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	return &syncFixture{harness: h, store: s, instance: instance, provision: raw, account: "7"}
}

// profile provisions a source profile of the fixture account and returns its Pastime
// tracking profile and a fresh sync credential.
func (f *syncFixture) profile(t *testing.T, source, name string) (int64, string) {
	t.Helper()
	ctx := t.Context()
	p := bloem.ProvisionInput{Version: 1, InstanceID: f.instance.ID, AccountID: f.account, AccountName: "Household", ProfileID: source, DefaultProfileID: "main", Name: name, AccountRevision: 1, Revision: 1, Active: true, AccountActive: true}
	m, err := f.store.Provision(ctx, f.provision, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.store.IssueCredential(ctx, f.provision, m.ProfileID, "issue-"+source)
	if err != nil {
		t.Fatal(err)
	}
	return m.ProfileID, c.Credential
}

// call sends a bearer request and checks a documented answer against the contract.
// call sends a request the way plugin 0.1.14 does: no User-Agent of its own.
func (f *syncFixture) call(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.callAs(t, "", method, path, token, body)
}

// plainCall sends a request the way plugin 0.2.0 does.
func (f *syncFixture) plainCall(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.callAs(t, "bloem-pastime/0.2.0", method, path, token, body)
}

// callFrom sends a 0.1.14-style request from another client address.
func (f *syncFixture) callFrom(t *testing.T, remoteAddr, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.send(t, remoteAddr, "", method, path, token, body)
}

func (f *syncFixture) callAs(t *testing.T, userAgent, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.send(t, "127.0.0.1:51234", userAgent, method, path, token, body)
}

func (f *syncFixture) send(t *testing.T, remoteAddr, userAgent, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.RemoteAddr = remoteAddr
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if userAgent != "" {
		r.Header.Set("User-Agent", userAgent)
	}
	w := httptest.NewRecorder()
	f.server.Router.ServeHTTP(w, r)
	f.checkContract(t, method, path, w)
	return w
}

func (f *syncFixture) checkContract(t *testing.T, method, path string, w *httptest.ResponseRecorder) {
	t.Helper()
	rctx := chi.NewRouteContext()
	u, _ := url.Parse(path)
	if !f.server.Router.Match(rctx, method, u.Path) {
		return
	}
	p := f.server.Spec.Paths.Value(rctx.RoutePattern())
	if p == nil || p.GetOperation(method) == nil {
		t.Fatalf("undocumented route %s %s", method, path)
	}
	response := p.GetOperation(method).Responses.Value(strconv.Itoa(w.Code))
	if response == nil {
		t.Fatalf("undocumented %s %s %d: %s", method, path, w.Code, w.Body.String())
	}
	if content, ok := response.Value.Content["application/json"]; ok {
		var value any
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if err := content.Schema.Value.VisitJSON(value); err != nil {
			t.Fatalf("response contract %s %s: %v", method, path, err)
		}
	}
}

// strict decodes a 200 body the way plugin 0.1.14 does: unknown fields are an error.
func strict[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	d := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		t.Fatalf("0.1.14 cannot decode %s: %v", w.Body.String(), err)
	}
	return out
}

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem body: %s", w.Body.String())
	}
	return p.Code
}

func execSQL(t *testing.T, f *syncFixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
