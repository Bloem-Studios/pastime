package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestBloemPersonalStateHTTPRequiresSyncCredential(t *testing.T) {
	h := application(t)
	ctx := t.Context()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	h.server.secrets = box
	s := bloem.Store{Pool: h.Pool, Box: box}
	instance, raw, err := s.Register(ctx, h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://example.invalid", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Provision(ctx, raw, bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "account", AccountName: "Account", ProfileID: "child", DefaultProfileID: "child", Name: "Child", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.IssueCredential(ctx, raw, child.ProfileID, "state-http")
	if err != nil {
		t.Fatal(err)
	}
	var title int64
	if err = h.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','tmdb','42','Film','minutes',90) RETURNING id`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"version": 1, "instance_id": instance.ID, "account_id": "account", "profile_id": "child", "entity_id": "favorite-film", "event_id": "favorite-add", "revision": 1, "operation": "favorite_add", "occurred_at": time.Now().UTC(), "media": map[string]any{"kind": "movie", "external_ids": map[string]string{"tmdb": "42"}}}
	call := func(token string, cookie bool) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/bloem/state/events:apply", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		if cookie {
			r.AddCookie(&http.Cookie{Name: "pastime_session", Value: token})
		} else if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		raw    string
		cookie bool
	}{{"", false}, {raw, false}, {h.OwnerRaw, true}} {
		w := call(tc.raw, tc.cookie)
		if w.Code != 401 {
			t.Fatalf("non-sync write: %d", w.Code)
		}
	}
	w := call(cred.Credential, false)
	if w.Code != 200 {
		t.Fatalf("sync write: %d %s", w.Code, w.Body.String())
	}
	var present bool
	if err = h.Pool.QueryRow(ctx, `SELECT present FROM profile_catalog_memberships WHERE profile_id=$1 AND catalog_item_id=$2 AND kind='favorite'`, child.ProfileID, title).Scan(&present); err != nil || !present {
		t.Fatal("effect absent", err)
	}
	readBody, _ := json.Marshal(bloem.PersonalRead{Kinds: []string{"favorite"}, PageSize: 1})
	for _, token := range []string{"", raw, cred.Credential} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/bloem/state:read", bytes.NewReader(readBody))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		h.server.Router.ServeHTTP(out, req)
		want := 401
		if token == cred.Credential {
			want = 200
		}
		if out.Code != want {
			t.Fatalf("remote state authority: %d %s", out.Code, out.Body.String())
		}
	}
	body["profile_id"] = "other"
	w = call(cred.Credential, false)
	if w.Code != 403 {
		t.Fatal("foreign profile", w.Code)
	}
}
