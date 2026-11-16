package bloem

import (
	"context"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// A Connect Silo key is the person's own credential, so it ends with the account's
// sessions: administrator revoke-access, deactivation (reactivation does not revive
// it), a role change, a password change and claim withdrawal. Keys Silo provisioning
// issues stay under the provisioning-key holder's control.
func TestConnectSiloKeyEndsWithAccountRevocation(t *testing.T) {
	c := newSyncCase(t)
	ctx := t.Context()
	accounts := c.f.Accounts
	member := c.f.User(t, "silo_member", "member")
	_, machine := c.credential(t, "acct", "main", "machine")
	issue := func(t *testing.T) string {
		t.Helper()
		a, _ := c.f.Login(t, "silo_member")
		k, err := c.s.IssueSelfServiceCredential(ctx, a, a.TrackingID(), c.instance)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.s.AuthenticateSync(ctx, k.Credential); err != nil {
			t.Fatal("a new Connect Silo key was refused", err)
		}
		return k.Credential
	}
	refused := func(t *testing.T, key, after string) {
		t.Helper()
		_, err := c.s.AuthenticateSync(ctx, key)
		wantFault(t, err, 401, "unauthenticated")
		var open int
		if err = c.f.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_sync_credentials c JOIN tracking_profiles t ON t.id=c.profile_id
			WHERE t.account_id=$1 AND (c.revoked_at IS NULL OR c.revoked_at>clock_timestamp())`, member.User.ID).Scan(&open); err != nil {
			t.Fatal(err)
		}
		if open != 0 {
			t.Fatalf("%d Connect Silo keys still current after %s", open, after)
		}
	}
	version := func() int {
		t.Helper()
		var v int
		if err := c.f.Pool.QueryRow(ctx, `SELECT version FROM users WHERE id=$1`, member.User.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	key := issue(t)
	if err := accounts.RevokeUserAccess(ctx, c.f.Owner, member.User.ID); err != nil {
		t.Fatal(err)
	}
	refused(t, key, "revoke-access")

	key = issue(t)
	off, on := false, true
	if _, err := accounts.UpdateUser(ctx, c.f.Owner, member.User.ID, identity.UserPatch{Version: version(), Active: &off}, false); err != nil {
		t.Fatal(err)
	}
	refused(t, key, "deactivation")
	if _, err := accounts.UpdateUser(ctx, c.f.Owner, member.User.ID, identity.UserPatch{Version: version(), Active: &on}, false); err != nil {
		t.Fatal(err)
	}
	refused(t, key, "reactivation")

	key = issue(t)
	admin := "admin"
	if _, err := accounts.UpdateUser(ctx, c.f.Owner, member.User.ID, identity.UserPatch{Version: version(), Role: &admin}, false); err != nil {
		t.Fatal(err)
	}
	refused(t, key, "a role change")
	plain := "member"
	if _, err := accounts.UpdateUser(ctx, c.f.Owner, member.User.ID, identity.UserPatch{Version: version(), Role: &plain}, false); err != nil {
		t.Fatal(err)
	}

	key = issue(t)
	session, _ := c.f.Login(t, "silo_member")
	if err := accounts.ChangePassword(ctx, session, testkit.Password, "synthetic-changed-password-2026"); err != nil {
		t.Fatal(err)
	}
	refused(t, key, "a password change")

	if _, err := c.s.AuthenticateSync(ctx, machine); err != nil {
		t.Fatal("a provisioning-issued key followed another account's revocation", err)
	}
}

// Claim withdrawal (pastime-bloem-register -revoke-claim) goes through WithdrawAccess.
func TestConnectSiloKeyEndsWithWithdrawAccess(t *testing.T) {
	c := newSyncCase(t)
	ctx := t.Context()
	member := c.f.User(t, "silo_claimer", "member")
	k, err := c.s.IssueSelfServiceCredential(ctx, member, member.TrackingID(), c.instance)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := c.f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = identity.LockAccounts(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = identity.WithdrawAccess(ctx, tx, member.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = c.s.AuthenticateSync(ctx, k.Credential)
	wantFault(t, err, 401, "unauthenticated")
}
