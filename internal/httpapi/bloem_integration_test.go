package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestBloemProvisioningHTTPBoundary(t *testing.T) {
	h := application(t)
	ctx := context.Background()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	h.server.secrets = box
	s := bloem.Store{Pool: h.Pool, Box: box}
	instance, raw, err := s.Register(ctx, h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	input := bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "synthetic-household", AccountName: "Household", ProfileID: "child", DefaultProfileID: "main", Name: "Child", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true}
	call := func(path, credential string, body any) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		h.server.Router.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/integrations/bloem/profiles:provision"
	requireCode(t, call(path, "", input), http.StatusUnauthorized)
	requireCode(t, call(path, h.OwnerRaw, input), http.StatusUnauthorized)
	w := call(path, raw, input)
	requireCode(t, w, 200)
	var result bloem.ProvisionResult
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	requireCode(t, call(path, raw, map[string]any{"version": 1, "role": "owner"}), 422)
	encoded, _ := json.Marshal(input)
	var missing map[string]any
	_ = json.Unmarshal(encoded, &missing)
	missing["account_id"] = "missing-state"
	delete(missing, "active")
	delete(missing, "account_active")
	requireCode(t, call(path, raw, missing), 422)
	in2 := input
	in2.Version = 2
	requireCode(t, call(path, raw, in2), 422)
	in2 = input
	in2.InstanceID = "forged"
	requireCode(t, call(path, raw, in2), 403)
	issuePath := "/api/v1/integrations/bloem/profiles/" + strconv.FormatInt(result.ProfileID, 10) + "/credentials:issue"
	requireCode(t, call(issuePath, h.OwnerRaw, map[string]string{"issuance_id": "one"}), 401)
	issued := call(issuePath, raw, map[string]string{"issuance_id": "one"})
	requireCode(t, issued, 200)
	var secret bloem.CredentialResult
	if err = json.Unmarshal(issued.Body.Bytes(), &secret); err != nil {
		t.Fatal(err)
	}
	requireCode(t, call(path, secret.Credential, input), 401)
	if issued.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential response may be cached")
	}
}
