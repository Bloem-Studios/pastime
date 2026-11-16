package kosync

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// The KOReader key is a standing sign-in, so it ends with the account's sessions:
// administrator revoke-access, deactivation (reactivation does not revive it) and a
// password change. Without tracking access it is refused, as a Connect Silo key is,
// and works again when access returns. Push and Pull refuse it too, not only sign-in.
func TestKOReaderKeyEndsWithAccountRevocation(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool}
	member := f.User(t, "reader_member", "member")
	const password = "synthetic-reader-password"
	sum := md5.Sum([]byte(password))
	key := hex.EncodeToString(sum[:])
	set := func(t *testing.T) int64 {
		t.Helper()
		a, _ := f.Login(t, "reader_member")
		if err := s.SetPassword(ctx, a, password); err != nil {
			t.Fatal(err)
		}
		id, err := s.Authenticate(ctx, "reader_member", key)
		if err != nil {
			t.Fatal("a new KOReader key was refused", err)
		}
		if _, err = s.Push(ctx, id, Progress{Document: "0123456789abcdef0123456789abcdef", Percentage: 0.1}, key); err != nil {
			t.Fatal(err)
		}
		return id
	}
	refused := func(t *testing.T, profile int64, after string) {
		t.Helper()
		if _, err := s.Authenticate(ctx, "reader_member", key); err != ErrUnauthorized {
			t.Fatalf("KOReader sign-in survived %s: %v", after, err)
		}
		if _, err := s.Push(ctx, profile, Progress{Document: "0123456789abcdef0123456789abcdef", Percentage: 0.5}, key); err != ErrUnauthorized {
			t.Fatalf("KOReader push survived %s: %v", after, err)
		}
		if p, err := s.Pull(ctx, profile, "0123456789abcdef0123456789abcdef", key); p != nil || (err != nil && err != ErrUnauthorized) {
			t.Fatalf("KOReader pull survived %s: %+v %v", after, p, err)
		}
	}
	version := func() int {
		t.Helper()
		var v int
		if err := f.Pool.QueryRow(ctx, `SELECT version FROM users WHERE id=$1`, member.User.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	profile := set(t)
	if err := f.Accounts.RevokeUserAccess(ctx, f.Owner, member.User.ID); err != nil {
		t.Fatal(err)
	}
	refused(t, profile, "revoke-access")

	profile = set(t)
	off, on := false, true
	if _, err := f.Accounts.UpdateUser(ctx, f.Owner, member.User.ID, identity.UserPatch{Version: version(), Active: &off}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Accounts.UpdateUser(ctx, f.Owner, member.User.ID, identity.UserPatch{Version: version(), Active: &on}, false); err != nil {
		t.Fatal(err)
	}
	refused(t, profile, "deactivation and reactivation")

	profile = set(t)
	now := time.Now()
	if _, err := f.Pool.Exec(ctx, `SELECT pastime_apply_service_access($1,$2,$3,'[]'::jsonb)`, "https://access.example", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	refused(t, profile, "a service-access lapse")
	if _, err := f.Pool.Exec(ctx, `UPDATE service_access_policy SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "reader_member", key); err != nil {
		t.Fatal("KOReader key refused after access returned", err)
	}

	profile = set(t)
	session, _ := f.Login(t, "reader_member")
	if err := f.Accounts.ChangePassword(ctx, session, testkit.Password, "synthetic-changed-password-2026"); err != nil {
		t.Fatal(err)
	}
	refused(t, profile, "a password change")
}
