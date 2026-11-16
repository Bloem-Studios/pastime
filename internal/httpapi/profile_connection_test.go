package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestProfileConnectionStatusUsesVerifiedAccountAndCurrentEnrollment(t *testing.T) {
	h := application(t)
	h.User(t, "connection_viewer", "member")
	member, session := h.Login(t, "connection_viewer")
	read := func(raw string, linked, active bool) {
		t.Helper()
		r := h.request("GET", "/api/v1/profiles", nil, raw, "", "", "")
		requireCode(t, r, 200)
		var body struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || len(body.Items) != 1 {
			t.Fatalf("profile inventory: %s %v", r.Body.String(), err)
		}
		if body.Items[0]["bloem_linked"] != linked || body.Items[0]["bloem_active"] != active {
			t.Fatalf("incorrect connection status: %s", r.Body.String())
		}
	}
	read(session, false, false)
	const issuer = "https://sso.example.test/bloem"
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,'verified-member',$2)`, issuer, member.User.ID); err != nil {
		t.Fatal(err)
	}
	store := bloem.Store{Pool: h.Pool}
	i, raw, err := store.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://media.example.test", Scope: "connection", IdentityIssuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Provision(t.Context(), raw, bloem.ProvisionInput{Version: 1, InstanceID: i.ID, AccountID: "source", ProfileID: "main", DefaultProfileID: "main", AccountName: "Household", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: issuer, IdentitySubject: "verified-member"})
	if err != nil {
		t.Fatal(err)
	}
	read(session, true, true)
	read(h.OwnerRaw, false, false)
	if _, err = h.Pool.Exec(t.Context(), `UPDATE bloem_instances SET active=false WHERE id=$1`, i.ID); err != nil {
		t.Fatal(err)
	}
	read(session, true, false)
}
