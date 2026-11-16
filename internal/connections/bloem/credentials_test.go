package bloem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestSyncAuthorityLocksLifecycleBeforeCredential(t *testing.T) {
	f := testkit.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	i, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Provision(ctx, raw, ProvisionInput{Version: 1, InstanceID: i.ID, AccountID: "locking", AccountName: "Locking", ProfileID: "child", DefaultProfileID: "main", Name: "Child", Revision: 1, AccountRevision: 1, Active: true, AccountActive: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.IssueCredential(ctx, raw, p.ProfileID, "lock-test")
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var blockerPID int
	if err = blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, "SELECT 1 FROM bloem_accounts WHERE instance_id=$1 FOR UPDATE", i.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.AuthenticateSync(ctx, c.Credential); done <- err }()
	defer func() {
		_ = blocker.Rollback(context.Background())
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for {
		var waiting bool
		if err = f.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))", blockerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	probe, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback(context.Background())
	// Lifecycle revocation takes the account first. A waiting authentication
	// must not already own a credential lock, which would create a lock cycle.
	if _, err = probe.Exec(ctx, "SELECT 1 FROM bloem_sync_credentials WHERE token_digest=$1 FOR UPDATE NOWAIT", identity.Digest(c.Credential)); err != nil {
		t.Fatal("authentication locked credential before account lifecycle", err)
	}
}

func TestCredentialIssuanceReplayRotationAndSourceDisable(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, err := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	in := ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "household", AccountName: "Household", ProfileID: "child", DefaultProfileID: "main", Name: "Child", Revision: 1, AccountRevision: 1, Active: true, AccountActive: true}
	child, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssueCredential(ctx, raw, child.ProfileID, "first-enrollment")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.IssueCredential(ctx, raw, child.ProfileID, "first-enrollment")
	if err != nil || issued.Credential != replay.Credential {
		t.Fatal("issuance not retry-stable", err)
	}
	auth, err := s.AuthenticateSync(ctx, issued.Credential)
	if err != nil || auth.ProfileID != child.ProfileID || auth.SourceProfileID != "child" || auth.InstanceID != instance.ID {
		t.Fatal("credential binding", auth, err)
	}
	var plain bool
	if err = f.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM bloem_sync_credentials WHERE credential_ciphertext=$1::bytea)", []byte(issued.Credential)).Scan(&plain); err != nil || plain {
		t.Fatal("plaintext persisted", err)
	}
	rotated, err := s.IssueCredential(ctx, raw, child.ProfileID, "second-enrollment")
	if err != nil || rotated.Credential == issued.Credential {
		t.Fatal("rotation", err)
	}
	// The superseded grant stays usable until the host uses its successor.
	if _, err = s.AuthenticateSync(ctx, issued.Credential); err != nil {
		t.Fatal("rotation stranded the committed grant", err)
	}
	if _, err = s.AuthenticateSync(ctx, rotated.Credential); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateSync(ctx, issued.Credential); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("old grant survived use of its successor", err)
	}
	if _, err = s.IssueCredential(ctx, raw, child.ProfileID, "first-enrollment"); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("old issuance resurrected grant", err)
	}
	in.AccountRevision = 2
	in.AccountActive = false
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateSync(ctx, rotated.Credential); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("disabled source credential worked", err)
	}
	if _, err = s.IssueCredential(ctx, raw, child.ProfileID, "third-enrollment"); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("disabled source issued credential", err)
	}
	// A profile-only update at a new revision cannot revive an inactive account.
	in.Revision = 2
	in.Name = "Renamed"
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	in.AccountRevision = 3
	in.AccountActive = true
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateSync(ctx, rotated.Credential); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("re-enable revived old credential", err)
	}
	current, err := s.IssueCredential(ctx, raw, child.ProfileID, "after-account-enable")
	if err != nil {
		t.Fatal(err)
	}
	in.Revision = 3
	in.Active = false
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	in.Revision = 4
	in.Active = true
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateSync(ctx, current.Credential); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("profile re-enable revived old credential", err)
	}
}
