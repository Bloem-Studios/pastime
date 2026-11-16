package identity_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestAliasOIDCSubjectKeepsTheAccountAcrossSubjectModes(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	const issuer = "https://sso.example.test/application/o/pastime/"
	hashed := strings.Repeat("ab", 32)
	uuid := "0b6f2a2e-5c1d-4f8e-9a3b-7d2c1e0f4a51"
	if _, err := f.Pool.Exec(ctx, `INSERT INTO oidc_identities(issuer,subject,user_id,email) VALUES($1,$2,$3,'owner@example.test')`, issuer, hashed, f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	member := f.User(t, "alias_member", "member")
	if _, err := f.Accounts.AliasOIDCSubject(ctx, member, issuer, hashed, uuid, nil); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member added a sign-in alias", err)
	}
	for _, bad := range [][3]string{{"", hashed, uuid}, {issuer, "", uuid}, {issuer, hashed, ""}, {issuer, hashed, hashed}, {issuer, hashed, "bad\nsubject"}, {issuer, hashed, strings.Repeat("a", 256)}, {strings.Repeat("a", 501), hashed, uuid}} {
		if _, err := f.Accounts.AliasOIDCSubject(ctx, f.Owner, bad[0], bad[1], bad[2], nil); fault.Public(err).Status != 422 {
			t.Fatalf("invalid alias %q accepted: %v", bad, err)
		}
	}
	if _, err := f.Accounts.AliasOIDCSubject(ctx, f.Owner, issuer, "missing-subject", uuid, nil); !errors.Is(err, fault.NotFound) {
		t.Fatal("alias of a missing sign-in", err)
	}
	if _, err := f.Pool.Exec(ctx, `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,'taken-subject',$2)`, issuer, member.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Accounts.AliasOIDCSubject(ctx, f.Owner, issuer, hashed, "taken-subject", nil); fault.Public(err).Status != 409 {
		t.Fatal("alias took another account's sign-in", err)
	}
	u, err := f.Accounts.AliasOIDCSubject(ctx, f.Owner, issuer, hashed, uuid, map[string]any{"entry_point": "test"})
	if err != nil || u.ID != f.Owner.User.ID {
		t.Fatal(err)
	}
	var owner int64
	var email string
	if err = f.Pool.QueryRow(ctx, `SELECT user_id,email FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, uuid).Scan(&owner, &email); err != nil || owner != f.Owner.User.ID || email != "owner@example.test" {
		t.Fatal("alias row", owner, email, err)
	}
	var raw []byte
	if err = f.Pool.QueryRow(ctx, `SELECT metadata FROM audit_events WHERE action='oidc.subject_alias' AND actor_id=$1 AND target_id=$1::text`, f.Owner.User.ID).Scan(&raw); err != nil {
		t.Fatal("alias audit", err)
	}
	var meta map[string]any
	_ = json.Unmarshal(raw, &meta)
	if meta["issuer"] != issuer || meta["entry_point"] != "test" || strings.Contains(string(raw), hashed) || strings.Contains(string(raw), uuid) {
		t.Fatal("alias audit metadata", string(raw))
	}
	// Both subjects sign in to the same account.
	for _, subject := range []string{hashed, uuid} {
		got, _, err := f.Accounts.OIDCSignIn(ctx, identity.OIDCClaims{Issuer: issuer, Subject: subject}, nil, false)
		if err != nil || got.ID != f.Owner.User.ID {
			t.Fatal("aliased sign-in", subject == uuid, err)
		}
	}
}
