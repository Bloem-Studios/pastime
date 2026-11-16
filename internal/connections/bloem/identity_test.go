package bloem

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Synthetic issuers and subjects. The issuer that verified the Bloem login and the
// one Pastime signs in with are different applications of one provider.
const (
	bloemIssuer  = "https://sso.example.test/application/o/bloem/"
	loginIssuer  = "https://sso.example.test/application/o/pastime/"
	hashedIssuer = "https://sso.example.test/application/o/hashed/"
	subjectA     = "0b6f2a2e-5c1d-4f8e-9a3b-7d2c1e0f4a51"
	subjectB     = "4c3d2e1f-0a9b-4c8d-8e7f-6a5b4c3d2e1f"
	subjectC     = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

func identityFixture(t *testing.T, issuer string) (*testkit.Fixture, Store, Instance, string) {
	t.Helper()
	f := testkit.New(t)
	box, err := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(t.Context(), f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot", IdentityIssuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	return f, s, instance, raw
}

func managed(instance, account string) ProvisionInput {
	return ProvisionInput{Version: 1, InstanceID: instance, AccountID: account, AccountName: "Managed " + account, ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true}
}

func withIdentity(in ProvisionInput, issuer, subject string) ProvisionInput {
	in.IdentityIssuer, in.IdentitySubject = issuer, subject
	return in
}

func code(err error) string {
	if err == nil {
		return ""
	}
	return fault.Public(err).Code
}

func auditCount(t *testing.T, f *testkit.Fixture, action string) int {
	t.Helper()
	var n int
	if err := f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action=$1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func lastAudit(t *testing.T, f *testkit.Fixture, action string) map[string]any {
	t.Helper()
	var raw []byte
	if err := f.Pool.QueryRow(context.Background(), `SELECT metadata FROM audit_events WHERE action=$1 ORDER BY id DESC LIMIT 1`, action).Scan(&raw); err != nil {
		t.Fatal(action, err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

type accountState struct {
	Issuer, Subject, Status   string
	BoundIssuer, BoundSubject string
	Owns, Claimed             bool
	UserID                    int64
}

func account(t *testing.T, f *testkit.Fixture, instance, source string) accountState {
	t.Helper()
	var a accountState
	if err := f.Pool.QueryRow(context.Background(), `SELECT identity_issuer,identity_subject,link_status,bound_issuer,bound_subject,owns_account,claimed_at IS NOT NULL,account_id FROM bloem_accounts WHERE instance_id=$1 AND source_account_id=$2`, instance, source).
		Scan(&a.Issuer, &a.Subject, &a.Status, &a.BoundIssuer, &a.BoundSubject, &a.Owns, &a.Claimed, &a.UserID); err != nil {
		t.Fatal(err)
	}
	return a
}

func signInIdentity(t *testing.T, f *testkit.Fixture, issuer, subject string, user int64) {
	t.Helper()
	if _, err := f.Pool.Exec(context.Background(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, subject, user); err != nil {
		t.Fatal(err)
	}
}

func TestSetIdentityIssuerTransitions(t *testing.T) {
	f, s, instance, raw := identityFixture(t, "")
	ctx := t.Context()
	member := f.User(t, "plain_member", "member")
	if _, _, err := s.SetIdentityIssuer(ctx, member, instance.ID, bloemIssuer, nil); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member changed the identity issuer", err)
	}
	for _, bad := range []string{"", "http://sso.example.test/", "https://", "https://user:pass@sso.example.test/", "https://sso.example.test/?q=1", "https://sso.example.test/#f", "https://sso.example.test/" + strings.Repeat("a", 500), "https://sso.example.test/\x01"} {
		if _, _, err := s.SetIdentityIssuer(ctx, f.Owner, instance.ID, bad, nil); fault.Public(err).Status != 422 {
			t.Fatalf("invalid issuer %q accepted: %v", bad, err)
		}
	}
	if _, _, err := s.SetIdentityIssuer(ctx, f.Owner, "missing-instance", bloemIssuer, nil); !errors.Is(err, fault.NotFound) {
		t.Fatal("unknown instance", err)
	}
	// An identity sent before the issuer is set is a mismatch, not a generic refusal.
	if _, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "early"), bloemIssuer, subjectA)); code(err) != "bloem_identity_mismatch" {
		t.Fatal("identity accepted without an instance issuer", err)
	}
	previous, changed, err := s.SetIdentityIssuer(ctx, f.Owner, instance.ID, bloemIssuer, map[string]any{"entry_point": "test"})
	if err != nil || previous != "" || !changed {
		t.Fatal("setting an empty issuer", previous, changed, err)
	}
	meta := lastAudit(t, f, "bloem.instance.identity_issuer")
	if meta["old_issuer"] != "" || meta["new_issuer"] != bloemIssuer || meta["entry_point"] != "test" {
		t.Fatal("issuer audit metadata", meta)
	}
	if _, changed, err = s.SetIdentityIssuer(ctx, f.Owner, instance.ID, bloemIssuer, nil); err != nil || changed || auditCount(t, f, "bloem.instance.identity_issuer") != 1 {
		t.Fatal("unchanged issuer was rewritten", changed, err)
	}
	// Nothing depends on the issuer yet, so it can still be corrected.
	if _, changed, err = s.SetIdentityIssuer(ctx, f.Owner, instance.ID, hashedIssuer, nil); err != nil || !changed {
		t.Fatal("correcting an unused issuer", err)
	}
	if _, _, err = s.SetIdentityIssuer(ctx, f.Owner, instance.ID, bloemIssuer, nil); err != nil {
		t.Fatal(err)
	}
	in := withIdentity(managed(instance.ID, "carrier"), bloemIssuer, subjectA)
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SetIdentityIssuer(ctx, f.Owner, instance.ID, hashedIssuer, nil); code(err) != "identity_issuer_in_use" {
		t.Fatal("issuer changed under a managed identity", err)
	}
	in.AccountRevision = 2
	in = withIdentity(in, "", "")
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectUserUUID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SetIdentityIssuer(ctx, f.Owner, instance.ID, hashedIssuer, nil); code(err) != "identity_issuer_in_use" {
		t.Fatal("issuer changed under a declared sign-in issuer", err)
	}
	var issuer string
	if err = f.Pool.QueryRow(ctx, `SELECT identity_issuer FROM bloem_instances WHERE id=$1`, instance.ID).Scan(&issuer); err != nil || issuer != bloemIssuer {
		t.Fatal("refused change altered the issuer", issuer, err)
	}
}

func TestDeclareAndRemoveLoginIssuer(t *testing.T) {
	f, s, instance, _ := identityFixture(t, "")
	ctx := t.Context()
	if _, err := s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectUserUUID, nil); code(err) != "identity_issuer_required" {
		t.Fatal("declared without an instance identity issuer", err)
	}
	if _, _, err := s.SetIdentityIssuer(ctx, f.Owner, instance.ID, bloemIssuer, nil); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ issuer, mode string }{{loginIssuer, "email"}, {loginIssuer, ""}, {"http://sso.example.test/", SubjectUserUUID}, {bloemIssuer, SubjectUserUUID}} {
		if _, err := s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, bad.issuer, bad.mode, nil); fault.Public(err).Status != 422 {
			t.Fatalf("invalid declaration %+v accepted: %v", bad, err)
		}
	}
	member := f.User(t, "plain_member", "member")
	if _, err := s.DeclareLoginIssuer(ctx, member, instance.ID, loginIssuer, SubjectUserUUID, nil); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member declared a sign-in issuer", err)
	}
	if _, err := s.DeclareLoginIssuer(ctx, f.Owner, "missing-instance", loginIssuer, SubjectUserUUID, nil); !errors.Is(err, fault.NotFound) {
		t.Fatal("unknown instance", err)
	}
	changed, err := s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectUserUUID, map[string]any{"authority": "test"})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	meta := lastAudit(t, f, "bloem.instance.login_issuer.declare")
	if meta["login_issuer"] != loginIssuer || meta["subject_mode"] != SubjectUserUUID || meta["authority"] != "test" {
		t.Fatal("declaration audit", meta)
	}
	if changed, err = s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectUserUUID, nil); err != nil || changed || auditCount(t, f, "bloem.instance.login_issuer.declare") != 1 {
		t.Fatal("repeated declaration was rewritten", changed, err)
	}
	if _, err = s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectHashedUserID, nil); code(err) != "login_issuer_declared" {
		t.Fatal("subject mode silently changed", err)
	}
	if err = s.RemoveLoginIssuer(ctx, member, instance.ID, loginIssuer, nil); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member removed a sign-in issuer", err)
	}
	if err = s.RemoveLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, map[string]any{"authority": "test"}); err != nil {
		t.Fatal(err)
	}
	if meta = lastAudit(t, f, "bloem.instance.login_issuer.remove"); meta["login_issuer"] != loginIssuer || meta["authority"] != "test" {
		t.Fatal("removal audit", meta)
	}
	if err = s.RemoveLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, nil); !errors.Is(err, fault.NotFound) {
		t.Fatal("removing an undeclared issuer", err)
	}
}

func TestProvisionIdentityMismatchIsSpecific(t *testing.T) {
	_, s, instance, raw := identityFixture(t, bloemIssuer)
	ctx := t.Context()
	if _, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "other-issuer"), hashedIssuer, subjectA)); code(err) != "bloem_identity_mismatch" || fault.Public(err).Status != 403 {
		t.Fatal("foreign issuer", err)
	}
	if _, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "half"), bloemIssuer, "")); fault.Public(err).Status != 422 {
		t.Fatal("half-set identity", err)
	}
}

func TestProvisionIdentityTransitions(t *testing.T) {
	f, s, instance, raw := identityFixture(t, bloemIssuer)
	ctx := t.Context()
	in := managed(instance.ID, "household")
	if _, err := s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	// Equal revisions never change the account's identity.
	if _, err := s.Provision(ctx, raw, withIdentity(in, bloemIssuer, subjectA)); !errors.Is(err, fault.Conflict) {
		t.Fatal("identity changed without a newer revision", err)
	}
	step := func(subject string) error {
		in.AccountRevision++
		if subject == "" {
			in = withIdentity(in, "", "")
		} else {
			in = withIdentity(in, bloemIssuer, subject)
		}
		_, err := s.Provision(ctx, raw, in)
		return err
	}
	if err := step(subjectA); err != nil {
		t.Fatal("attach", err)
	}
	if a := account(t, f, instance.ID, "household"); a.Issuer != bloemIssuer || a.Subject != subjectA || a.Status != "pending" || !a.Owns {
		t.Fatal("attached identity", a)
	}
	if _, err := s.Provision(ctx, raw, in); err != nil {
		t.Fatal("replay after attach", err)
	}
	// An unclaimed account the integration owns may be re-targeted.
	if err := step(subjectB); err != nil {
		t.Fatal("re-target pending account", err)
	}
	if err := step(""); err != nil {
		t.Fatal("clear", err)
	}
	if a := account(t, f, instance.ID, "household"); a.Issuer != "" || a.Subject != "" {
		t.Fatal("cleared identity", a)
	}
	if err := step(subjectA); err != nil {
		t.Fatal("re-attach", err)
	}
	// Another source account cannot take an identity in use, on insert or update.
	other := withIdentity(managed(instance.ID, "duplicate"), bloemIssuer, subjectA)
	if _, err := s.Provision(ctx, raw, other); code(err) != "bloem_identity_conflict" {
		t.Fatal("duplicate identity inserted", err)
	}
	var exists bool
	if err := f.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_accounts WHERE instance_id=$1 AND source_account_id='duplicate')`, instance.ID).Scan(&exists); err != nil || exists {
		t.Fatal("refused duplicate left a mapping", exists, err)
	}
	other = managed(instance.ID, "duplicate")
	if _, err := s.Provision(ctx, raw, other); err != nil {
		t.Fatal(err)
	}
	other.AccountRevision = 2
	if _, err := s.Provision(ctx, raw, withIdentity(other, bloemIssuer, subjectA)); code(err) != "bloem_identity_conflict" {
		t.Fatal("duplicate identity attached", err)
	}
	// Once claimed, an identity cannot move to another subject; clearing keeps the claim.
	if _, err := f.Pool.Exec(ctx, `UPDATE bloem_accounts SET link_status='linked',claimed_at=clock_timestamp(),bound_issuer=identity_issuer,bound_subject=identity_subject WHERE instance_id=$1 AND source_account_id='household'`, instance.ID); err != nil {
		t.Fatal(err)
	}
	if err := step(subjectB); code(err) != "bloem_identity_conflict" {
		t.Fatal("claimed identity re-targeted", err)
	}
	if err := step(""); err != nil {
		t.Fatal("clearing a claimed identity", err)
	}
	if a := account(t, f, instance.ID, "household"); a.Subject != "" || a.Status != "linked" || !a.Claimed || a.BoundSubject != subjectA {
		t.Fatal("clear dropped the claim", a)
	}
	// A clear does not release the claim: another identity is still refused, so a
	// reassignment in Bloem cannot reach the claimer's account in two steps.
	if err := step(subjectB); code(err) != "bloem_identity_conflict" {
		t.Fatal("attaching another identity to a claimed account after a clear", err)
	}
	if a := account(t, f, instance.ID, "household"); a.Subject != "" || a.BoundSubject != subjectA {
		t.Fatal("refused attach changed the claimed account", a)
	}
	if err := step(subjectA); err != nil {
		t.Fatal("re-attaching the claimed identity", err)
	}
}

func TestLinkedAccountKeepsItsPerson(t *testing.T) {
	f, s, instance, raw := identityFixture(t, bloemIssuer)
	ctx := t.Context()
	member := f.User(t, "linked_member", "member")
	const subjectD = "1d2c3b4a-0000-4000-8000-0000000000d1"
	signInIdentity(t, f, bloemIssuer, subjectD, member.User.ID)
	in := withIdentity(managed(instance.ID, "setup-link"), bloemIssuer, subjectD)
	r, err := s.Provision(ctx, raw, in)
	if err != nil || r.LinkStatus != "linked" || r.AccountID != member.User.ID {
		t.Fatal("setup link", r, err)
	}
	if a := account(t, f, instance.ID, "setup-link"); a.Owns || a.BoundIssuer != bloemIssuer || a.BoundSubject != subjectD {
		t.Fatal("setup link did not record the identity it linked under", a)
	}
	step := func(subject string) error {
		in.AccountRevision++
		in = withIdentity(in, "", "")
		if subject != "" {
			in = withIdentity(in, bloemIssuer, subject)
		}
		_, err := s.Provision(ctx, raw, in)
		return err
	}
	if err = step(subjectB); code(err) != "bloem_identity_conflict" {
		t.Fatal("linked account re-targeted", err)
	}
	if err = step(""); err != nil {
		t.Fatal("clearing a linked identity", err)
	}
	// Clearing first does not let another person's tracking into the member's own account.
	if err = step(subjectB); code(err) != "bloem_identity_conflict" {
		t.Fatal("linked account took another identity after a clear", err)
	}
	if err = step(subjectD); err != nil {
		t.Fatal("re-attaching the linked identity", err)
	}
	if a := account(t, f, instance.ID, "setup-link"); a.Subject != subjectD || a.Status != "linked" || a.UserID != member.User.ID {
		t.Fatal("linked account state", a)
	}
}

func TestProvisionLinksOnlyVerifiedMembers(t *testing.T) {
	f, s, instance, raw := identityFixture(t, bloemIssuer)
	ctx := t.Context()
	if _, err := s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectUserUUID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, hashedIssuer, SubjectHashedUserID, nil); err != nil {
		t.Fatal(err)
	}
	admin := f.User(t, "second_admin", "admin")
	members := map[string]identity.Actor{}
	for _, name := range []string{"direct", "declared", "undeclared", "shape", "twin_a", "twin_b", "mapped", "inactive"} {
		members[name] = f.User(t, "member_"+name, "member")
	}
	signInIdentity(t, f, bloemIssuer, subjectA, f.Owner.User.ID)
	signInIdentity(t, f, loginIssuer, subjectB, admin.User.ID)
	cases := []struct {
		source, issuer, subject string
		want                    int64
	}{
		{"owner", bloemIssuer, subjectA, 0},
		{"admin", bloemIssuer, subjectB, 0},
	}
	add := func(member, issuer, subject string, linked bool) {
		signInIdentity(t, f, issuer, subject, members[member].User.ID)
		want := int64(0)
		if linked {
			want = members[member].User.ID
		}
		cases = append(cases, struct {
			source, issuer, subject string
			want                    int64
		}{member, issuer, subject, want})
	}
	add("direct", bloemIssuer, "1d2c3b4a-0000-4000-8000-000000000001", true)
	add("declared", loginIssuer, "1d2c3b4a-0000-4000-8000-000000000002", true)
	add("undeclared", "https://sso.example.test/application/o/other/", "1d2c3b4a-0000-4000-8000-000000000003", false)
	add("shape", hashedIssuer, "1d2c3b4a-0000-4000-8000-000000000004", false)
	add("twin_a", bloemIssuer, "1d2c3b4a-0000-4000-8000-000000000005", false)
	signInIdentity(t, f, loginIssuer, "1d2c3b4a-0000-4000-8000-000000000005", members["twin_b"].User.ID)
	add("inactive", loginIssuer, "1d2c3b4a-0000-4000-8000-000000000006", false)
	if _, err := f.Pool.Exec(ctx, `UPDATE users SET active=false WHERE id=$1`, members["inactive"].User.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		r, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, c.source), bloemIssuer, c.subject))
		if err != nil {
			t.Fatal(c.source, err)
		}
		a := account(t, f, instance.ID, c.source)
		if c.want != 0 && (r.AccountID != c.want || r.LinkStatus != "linked" || a.Owns) {
			t.Fatalf("%s: verified member not linked: %+v %+v", c.source, r, a)
		}
		if c.want == 0 && (r.LinkStatus != "pending" || !a.Owns || r.AccountID == f.Owner.User.ID || r.AccountID == admin.User.ID || r.AccountID == members[c.source].User.ID) {
			t.Fatalf("%s: linked without a unique verified member: %+v %+v", c.source, r, a)
		}
	}
	// A member already mapped in the instance is never mapped twice.
	signInIdentity(t, f, loginIssuer, "1d2c3b4a-0000-4000-8000-000000000007", members["direct"].User.ID)
	r, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "mapped"), bloemIssuer, "1d2c3b4a-0000-4000-8000-000000000007"))
	if err != nil || r.LinkStatus != "pending" || r.AccountID == members["direct"].User.ID {
		t.Fatal("already mapped member linked twice", r, err)
	}
	// Linking compares verified subjects only, never email.
	if _, err = f.Pool.Exec(ctx, `UPDATE users SET email='same@example.test' WHERE id=$1`, members["mapped"].User.ID); err != nil {
		t.Fatal(err)
	}
	in := withIdentity(managed(instance.ID, "email"), bloemIssuer, "1d2c3b4a-0000-4000-8000-000000000008")
	in.Email = "same@example.test"
	if r, err = s.Provision(ctx, raw, in); err != nil || r.LinkStatus != "pending" || r.AccountID == members["mapped"].User.ID {
		t.Fatal("email linked an account", r, err)
	}
}

func TestSnapshotAcceptsAttachedIdentity(t *testing.T) {
	_, s, instance, raw := identityFixture(t, bloemIssuer)
	ctx := t.Context()
	in := managed(instance.ID, "snapshot-household")
	if _, err := s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	in.AccountRevision = 2
	in = withIdentity(in, bloemIssuer, subjectA)
	if _, err := s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	stale := withIdentity(in, "", "")
	if _, err := s.AcceptPage(ctx, raw, SnapshotPage{SnapshotID: "stale", Complete: true, Profiles: []ProvisionInput{stale}}); !errors.Is(err, fault.Conflict) {
		t.Fatal("snapshot without the attached identity accepted", err)
	}
	receipt, err := s.AcceptPage(ctx, raw, SnapshotPage{SnapshotID: "current", Complete: true, Profiles: []ProvisionInput{in}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteSnapshot(ctx, raw, "current", receipt.Receipt); err != nil {
		t.Fatal(err)
	}
}

func TestIdentitySummaryCountsWithoutSubjects(t *testing.T) {
	f, s, instance, raw := identityFixture(t, bloemIssuer)
	ctx := t.Context()
	if _, err := s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectUserUUID, nil); err != nil {
		t.Fatal(err)
	}
	for i, subject := range []string{subjectA, subjectB, ""} {
		in := managed(instance.ID, "summary-"+string(rune('a'+i)))
		if subject != "" {
			in = withIdentity(in, bloemIssuer, subject)
		}
		if _, err := s.Provision(ctx, raw, in); err != nil {
			t.Fatal(err)
		}
	}
	signInIdentity(t, f, loginIssuer, subjectC, f.Owner.User.ID)
	signInIdentity(t, f, loginIssuer, strings.Repeat("ab", 32), f.Owner.User.ID)
	if _, err := s.IdentitySummary(ctx, f.User(t, "plain_member", "member"), instance.ID); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member read the identity summary", err)
	}
	sum, err := s.IdentitySummary(ctx, f.Owner, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Issuer != bloemIssuer || sum.Accounts != 3 || sum.WithIdentity != 2 || sum.Claimable != 2 || sum.Claimed != 0 || len(sum.LoginIssuers) != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if l := sum.LoginIssuers[0]; l.Issuer != loginIssuer || l.SubjectMode != SubjectUserUUID || l.SignIns != 2 || l.SignInsMatchingMode != 1 {
		t.Fatalf("login issuer summary: %+v", l)
	}
}
