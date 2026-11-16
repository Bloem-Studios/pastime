package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestBloemCalendarHTTPRequiresMachineAuthority(t *testing.T) {
	h := application(t)
	s := bloem.Store{Pool: h.Pool}
	instance, raw, err := s.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://media.example.test", Scope: "scope"})
	if err != nil {
		t.Fatal(err)
	}
	profile := bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "source", AccountName: "Household", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true}
	if _, err = s.Provision(t.Context(), raw, profile); err != nil {
		t.Fatal(err)
	}
	page := bloem.CalendarInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", SnapshotID: testkit.Key(t, h.Fixture), Start: "2026-10-12", End: "2026-10-12", VisibilityDigest: "scope", Revision: 1, Complete: true, ExpiresAt: time.Now().Add(time.Minute)}
	body, _ := json.Marshal(page)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/bloem/calendar/pages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+raw)
	out := httptest.NewRecorder()
	h.server.Router.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatalf("machine page: %d %s", out.Code, out.Body.String())
	}
	for _, credential := range []string{"", h.OwnerRaw} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/bloem/calendar/pages", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if credential != "" {
			req.AddCookie(&http.Cookie{Name: "pastime_session", Value: credential})
		}
		out := httptest.NewRecorder()
		h.server.Router.ServeHTTP(out, req)
		if out.Code != 401 {
			t.Fatalf("cookie/anonymous machine write accepted: %d", out.Code)
		}
	}
}
