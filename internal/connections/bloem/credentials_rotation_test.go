package bloem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

type reviewFixture struct {
	f        *testkit.Fixture
	s        Store
	raw      string
	instance Instance
	in       ProvisionInput
	profile  int64
}

func newReviewFixture(t *testing.T) reviewFixture {
	t.Helper()
	f := testkit.New(t)
	box, err := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(t.Context(), f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	in := ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "household", AccountName: "Household", ProfileID: "child", DefaultProfileID: "main", Name: "Child", Revision: 1, AccountRevision: 1, Active: true, AccountActive: true}
	p, err := s.Provision(t.Context(), raw, in)
	if err != nil {
		t.Fatal(err)
	}
	return reviewFixture{f: f, s: s, raw: raw, instance: instance, in: in, profile: p.ProfileID}
}

func (r reviewFixture) authenticates(credential string) bool {
	_, err := r.s.AuthenticateSync(context.Background(), credential)
	return err == nil
}

// A rotation must not strand the host: the old credential keeps working until
// the host proves it committed the successor by using it, or a bounded grace
// period ends. Lifecycle revocation stays immediate.
func TestCredentialRotationOverlapsUntilSuccessorIsUsed(t *testing.T) {
	r := newReviewFixture(t)
	ctx := t.Context()
	old, err := r.s.IssueCredential(ctx, r.raw, r.profile, "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	next, err := r.s.IssueCredential(ctx, r.raw, r.profile, "revision-2")
	if err != nil || next.Credential == old.Credential {
		t.Fatal("rotation", err)
	}
	if !r.authenticates(old.Credential) {
		t.Fatal("rotation stranded the host's committed credential before the successor was used")
	}
	var deadline time.Time
	if err = r.f.Pool.QueryRow(ctx, `SELECT revoked_at FROM bloem_sync_credentials WHERE profile_id=$1 AND issuance_id='revision-1'`, r.profile).Scan(&deadline); err != nil {
		t.Fatal("superseded credential has no bounded deadline", err)
	}
	if until := time.Until(deadline); until <= 0 || until > credentialRotationGrace+time.Minute {
		t.Fatal("superseded credential grace is unbounded", until)
	}
	if _, err = r.s.IssueCredential(ctx, r.raw, r.profile, "revision-1"); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("superseded issuance re-issued", err)
	}
	replay, err := r.s.IssueCredential(ctx, r.raw, r.profile, "revision-2")
	if err != nil || replay.Credential != next.Credential {
		t.Fatal("successor issuance not retry-stable", err)
	}
	// A second rotation before the host commits must not extend the old deadline.
	third, err := r.s.IssueCredential(ctx, r.raw, r.profile, "revision-3")
	if err != nil {
		t.Fatal(err)
	}
	var again time.Time
	if err = r.f.Pool.QueryRow(ctx, `SELECT revoked_at FROM bloem_sync_credentials WHERE profile_id=$1 AND issuance_id='revision-1'`, r.profile).Scan(&again); err != nil || !again.Equal(deadline) {
		t.Fatal("later rotation extended a superseded credential", err)
	}
	if !r.authenticates(old.Credential) || !r.authenticates(next.Credential) {
		t.Fatal("superseded credentials ended before the host used a successor")
	}
	if !r.authenticates(third.Credential) {
		t.Fatal("current credential rejected")
	}
	if r.authenticates(old.Credential) || r.authenticates(next.Credential) {
		t.Fatal("superseded credentials survived use of the successor")
	}
	if !r.authenticates(third.Credential) {
		t.Fatal("current credential stopped after confirming itself")
	}
}

func TestSupersededCredentialEndsAtGraceDeadline(t *testing.T) {
	r := newReviewFixture(t)
	ctx := t.Context()
	old, err := r.s.IssueCredential(ctx, r.raw, r.profile, "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.IssueCredential(ctx, r.raw, r.profile, "revision-2"); err != nil {
		t.Fatal(err)
	}
	// Simulate the grace period elapsing without the host using its successor.
	if _, err = r.f.Pool.Exec(ctx, `UPDATE bloem_sync_credentials SET revoked_at=clock_timestamp()-interval '1 second' WHERE profile_id=$1 AND issuance_id='revision-1'`, r.profile); err != nil {
		t.Fatal(err)
	}
	if r.authenticates(old.Credential) {
		t.Fatal("superseded credential outlived its grace deadline")
	}
}

func TestLifecycleRevocationEndsSupersededCredentialsImmediately(t *testing.T) {
	for _, variant := range []string{"profile_disable", "account_disable", "snapshot_missing"} {
		t.Run(variant, func(t *testing.T) {
			r := newReviewFixture(t)
			ctx := t.Context()
			old, err := r.s.IssueCredential(ctx, r.raw, r.profile, "revision-1")
			if err != nil {
				t.Fatal(err)
			}
			current, err := r.s.IssueCredential(ctx, r.raw, r.profile, "revision-2")
			if err != nil {
				t.Fatal(err)
			}
			in := r.in
			switch variant {
			case "profile_disable":
				in.Revision, in.Active = 2, false
				_, err = r.s.Provision(ctx, r.raw, in)
			case "account_disable":
				in.AccountRevision, in.AccountActive = 2, false
				_, err = r.s.Provision(ctx, r.raw, in)
			case "snapshot_missing":
				main := in
				main.ProfileID, main.Name, main.Revision = "main", "Main", 2
				var page PageReceipt
				if _, err = r.s.Provision(ctx, r.raw, main); err == nil {
					page, err = r.s.AcceptPage(ctx, r.raw, SnapshotPage{SnapshotID: "snapshot", Complete: true, Profiles: []ProvisionInput{main}})
				}
				if err == nil {
					err = r.s.CompleteSnapshot(ctx, r.raw, "snapshot", page.Receipt)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.authenticates(old.Credential) || r.authenticates(current.Credential) {
				t.Fatal("lifecycle revocation left a credential usable")
			}
		})
	}
}
