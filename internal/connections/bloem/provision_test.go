package bloem

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestProvisionHouseholdAndRevisionBoundaries(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, err := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "household-pilot", IdentityIssuer: "https://synthetic.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	in := ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "source-account", AccountName: "Household", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true}
	first, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.LinkStatus != "pending" {
		t.Fatalf("unverified identity granted login: %+v", first)
	}
	var role, password string
	if err = f.Pool.QueryRow(ctx, "SELECT role,password_hash FROM users WHERE id=$1", first.AccountID).Scan(&role, &password); err != nil || role != "member" || password != "" {
		t.Fatalf("provision role/password: %s %v", role, err)
	}
	var wg sync.WaitGroup
	results := make(chan ProvisionResult, 4)
	errs := make(chan error, 4)
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.Provision(ctx, raw, in); results <- r; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for r := range results {
		if r.AccountID != first.AccountID || r.ProfileID != first.ProfileID {
			t.Fatal("duplicate mappings", r)
		}
	}
	in.ProfileID = "child"
	in.Name = "Child"
	child, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if child.AccountID != first.AccountID || child.ProfileID == first.ProfileID {
		t.Fatal("household mapping", child)
	}
	in.Revision = 2
	in.Name = "Renamed Child"
	renamed, err := s.Provision(ctx, raw, in)
	if err != nil || renamed.ProfileID != child.ProfileID {
		t.Fatal("rename mapping", renamed, err)
	}
	in.Revision = 1
	in.Name = "Stale Child"
	if _, err = s.Provision(ctx, raw, in); !errors.Is(err, fault.Conflict) {
		t.Fatal("stale revision accepted", err)
	}
	var name string
	if err = f.Pool.QueryRow(ctx, "SELECT name FROM tracking_profiles WHERE id=$1", child.ProfileID).Scan(&name); err != nil || name != "Renamed Child" {
		t.Fatal(name, err)
	}
	in.Revision = 2
	in.Name = "Conflicting Child"
	if _, err = s.Provision(ctx, raw, in); !errors.Is(err, fault.Conflict) {
		t.Fatal("same revision conflict accepted", err)
	}
	in.Name = "Renamed Child"
	in.InstanceID = "forged"
	if _, err = s.Provision(ctx, raw, in); !errors.Is(err, fault.Forbidden) {
		t.Fatal("forged instance accepted", err)
	}
}

func TestProvisionIdentityNeverMergesByEmail(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot", IdentityIssuer: "https://synthetic.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE users SET email='admin@example.invalid' WHERE id=$1", f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	in := ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "source-collision", AccountName: "Household", Email: "admin@example.invalid", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true}
	result, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountID == f.Owner.User.ID || result.LinkStatus != "pending" {
		t.Fatal("email collision linked administrator", result)
	}
	// Only a subject already verified by Pastime's OIDC flow may select a login, and
	// only a member's: an owner's or administrator's sign-in is never linked.
	if _, err = f.Pool.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id) VALUES('https://synthetic.invalid','verified-owner',$1)", f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	in.AccountID = "owner-source"
	in.IdentityIssuer = "https://synthetic.invalid"
	in.IdentitySubject = "verified-owner"
	refused, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if refused.AccountID == f.Owner.User.ID || refused.LinkStatus != "pending" {
		t.Fatal("verified owner linked", refused)
	}
	verified := f.User(t, "verified_member", "member")
	if _, err = f.Pool.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id) VALUES('https://synthetic.invalid','verified-member',$1)", verified.User.ID); err != nil {
		t.Fatal(err)
	}
	in.AccountID = "verified-source"
	in.IdentitySubject = "verified-member"
	linked, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if linked.AccountID != verified.User.ID || linked.LinkStatus != "linked" {
		t.Fatal("verified subject not linked", linked)
	}
	in.AccountRevision = 2
	in.AccountActive = false
	in.Revision = 2
	in.Active = false
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.Accounts.OIDCSignIn(ctx, identity.OIDCClaims{Issuer: "https://synthetic.invalid", Subject: "verified-member"}, nil, false); err != nil {
		t.Fatal("source disable removed an independently linked login", err)
	}
	in.AccountID = "forged-subject"
	in.IdentityIssuer = "https://evil.invalid"
	if _, err = s.Provision(ctx, raw, in); fault.Public(err).Code != "bloem_identity_mismatch" {
		t.Fatal("untrusted issuer accepted", err)
	}
	member := f.User(t, "ordinary_member", "member")
	if _, _, err = s.Register(ctx, member, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"}); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member enrolled instance", err)
	}
}

func TestIssuerWithOIDCPathCanBeExplicitlyGranted(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool}
	_, _, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot", IdentityIssuer: "https://auth.example.test/application/o/pastime/"})
	if err != nil {
		t.Fatal("valid OIDC issuer path rejected", err)
	}
}
