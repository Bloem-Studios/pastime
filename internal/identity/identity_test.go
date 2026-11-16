package identity_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testdb"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func ptr[T any](v T) *T { return &v }
func TestPasswordPolicyAndHashBounds(t *testing.T) {
	p := identity.NewPasswords()
	ctx := context.Background()
	for _, password := range []string{"short", "😀😀😀", strings.Repeat("a", 129)} {
		if _, e := p.Hash(ctx, password); e == nil {
			t.Fatal("accepted invalid password")
		}
	}
	h, e := p.Hash(ctx, testkit.Password)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(h, testkit.Password) || !strings.HasPrefix(h, "$argon2id$v=19$m=32768,t=2,p=1$") {
		t.Fatal("wrong password representation")
	}
	for _, tc := range []struct {
		password, hash string
		valid          bool
	}{{testkit.Password, h, true}, {"nonexistent-account-dummy-password", "", false}, {"wrong password", h, false}, {testkit.Password, strings.Replace(h, "m=32768", "m=999999999", 1), false}, {testkit.Password, "bad", false}, {strings.Repeat("x", 600), h, false}} {
		got, e := p.Verify(ctx, tc.password, tc.hash)
		if e != nil || got != tc.valid {
			t.Fatal("verification mismatch", e)
		}
	}
}
func TestBootstrapRaceAndNoTakeover(t *testing.T) {
	s := identity.New(testdb.Isolated(t))
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"owner_one", "owner_two"} {
		wg.Go(func() { _, err := s.Bootstrap(context.Background(), name, name, testkit.Password); errs <- err })
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, fault.Conflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("bootstrap outcomes %d/%d", success, conflict)
	}
}
func TestPendingAccountCannotUseDummyPasswordOrReplaceLastOwner(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	pending, _, _, err := f.Accounts.CreateUser(ctx, f.Owner, identity.CreateUserInput{Username: "pending_owner", Role: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if pending.PasswordReady {
		t.Fatal("pending owner has a password")
	}
	if _, _, err = f.Accounts.Login(ctx, pending.Username, "nonexistent-account-dummy-password"); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("dummy password granted access", err)
	}
	_, err = f.Accounts.UpdateUser(ctx, f.Owner, f.Owner.User.ID, identity.UserPatch{Version: f.Owner.User.Version, Active: ptr(false)}, false)
	if !errors.Is(err, fault.LastOwner) {
		t.Fatal("pending owner bypassed last-owner protection", err)
	}
}
func TestLastOwnerConcurrentDemotion(t *testing.T) {
	f := testkit.New(t)
	second := f.User(t, "second_owner", "owner")
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, a := range []identity.Actor{f.Owner, second} {
		wg.Go(func() {
			_, e := f.Accounts.UpdateUser(context.Background(), a, a.User.ID, identity.UserPatch{Version: a.User.Version, Role: ptr("member")}, false)
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	success, blocked := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, fault.LastOwner) {
			blocked++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatalf("outcomes %d/%d", success, blocked)
	}
}
func TestRoleBoundariesAndCurrentRevocation(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	member := f.User(t, "ordinary_member", "member")
	admin := f.User(t, "site_admin", "admin")
	if _, e := f.Accounts.Users(ctx, member, identity.UserFilter{}); !errors.Is(e, fault.Forbidden) {
		t.Fatal("member listed accounts", e)
	}
	if _, _, e := f.Accounts.Invite(ctx, admin, identity.InviteInput{Role: "owner"}); !errors.Is(e, fault.Forbidden) {
		t.Fatal("admin issued owner invite", e)
	}
	if _, e := f.Accounts.UpdateUser(ctx, admin, f.Owner.User.ID, identity.UserPatch{Version: f.Owner.User.Version, Active: ptr(false)}, false); !errors.Is(e, fault.Forbidden) {
		t.Fatal("admin changed owner", e)
	}
	if _, e := f.Accounts.UpdateUser(ctx, member, member.User.ID, identity.UserPatch{Version: member.User.Version, Role: ptr("owner")}, true); !errors.Is(e, fault.Forbidden) {
		t.Fatal("self-escalation", e)
	}
	_, raw, e := f.Accounts.MintToken(ctx, member, identity.TokenInput{Name: "Reader", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if e != nil {
		t.Fatal(e)
	}
	disabled, e := f.Accounts.UpdateUser(ctx, admin, member.User.ID, identity.UserPatch{Version: member.User.Version, Active: ptr(false)}, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.Authenticate(ctx, raw, "token"); !errors.Is(e, fault.Unauthenticated) {
		t.Fatal("disabled token accepted", e)
	}
	if _, _, e = f.Accounts.Login(ctx, member.User.Username, testkit.Password); !errors.Is(e, fault.Unauthenticated) {
		t.Fatal("disabled login accepted", e)
	}
	if _, e = f.Accounts.UpdateUser(ctx, admin, member.User.ID, identity.UserPatch{Version: disabled.Version, Active: ptr(true)}, false); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.Authenticate(ctx, raw, "token"); !errors.Is(e, fault.Unauthenticated) {
		t.Fatal("reactivation revived token", e)
	}
	if _, _, e = f.Accounts.Login(ctx, member.User.Username, testkit.Password); e != nil {
		t.Fatal("reactivated login denied", e)
	}
}
func TestInvitationClaimRaceExpiryAndIssuerAuthority(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	_, raw, e := f.Accounts.Invite(ctx, f.Owner, identity.InviteInput{Role: "member"})
	if e != nil {
		t.Fatal(e)
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"claim_one", "claim_two"} {
		wg.Go(func() { _, e := f.Accounts.Claim(ctx, raw, name, name, testkit.Password); errs <- e })
	}
	wg.Wait()
	close(errs)
	success, unavailable := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, fault.UnavailableInvite) {
			unavailable++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || unavailable != 1 {
		t.Fatalf("claim outcomes %d/%d", success, unavailable)
	}
	admin := f.User(t, "inviter_admin", "admin")
	invite, raw, e := f.Accounts.Invite(ctx, admin, identity.InviteInput{Role: "member"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Pool.Exec(ctx, "UPDATE invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", invite.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.PreviewInvite(ctx, raw); !errors.Is(e, fault.UnavailableInvite) {
		t.Fatal("expired invitation accepted", e)
	}
	_, raw, e = f.Accounts.Invite(ctx, admin, identity.InviteInput{Role: "member"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.UpdateUser(ctx, f.Owner, admin.User.ID, identity.UserPatch{Version: admin.User.Version, Role: ptr("member")}, false); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.Claim(ctx, raw, "blocked_claim", "Blocked", testkit.Password); !errors.Is(e, fault.UnavailableInvite) {
		t.Fatal("demoted issuer invitation accepted", e)
	}
}
func TestResetPreservesRoleAndRevokesOldAccess(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	member := f.User(t, "reset_member", "member")
	_, token, e := f.Accounts.MintToken(ctx, member, identity.TokenInput{Name: "Old", Scopes: []string{"profile:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if e != nil {
		t.Fatal(e)
	}
	_, link, e := f.Accounts.ResetLink(ctx, f.Owner, member.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.Authenticate(ctx, token, "token"); e != nil {
		t.Fatal("issuing link prematurely revoked token", e)
	}
	u, e := f.Accounts.Claim(ctx, link, "ignored_username", "Ignored", "a-new-synthetic-password")
	if e != nil {
		t.Fatal(e)
	}
	if u.Role != "member" || u.Username != member.User.Username {
		t.Fatal("reset changed identity/role")
	}
	if _, e = f.Accounts.Authenticate(ctx, token, "token"); !errors.Is(e, fault.Unauthenticated) {
		t.Fatal("old token survived reset", e)
	}
	if _, _, e = f.Accounts.Login(ctx, u.Username, testkit.Password); !errors.Is(e, fault.Unauthenticated) {
		t.Fatal("old password survived reset", e)
	}
	if _, _, e = f.Accounts.Login(ctx, u.Username, "a-new-synthetic-password"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.Claim(ctx, link, "", "", "another-new-password"); !errors.Is(e, fault.UnavailableInvite) {
		t.Fatal("reset replayed", e)
	}
}
func TestScopedTokensCannotAdministerAndRevokeImmediately(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	token, raw, e := f.Accounts.MintToken(ctx, f.Owner, identity.TokenInput{Name: "Restricted", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.Accounts.Authenticate(ctx, raw, "token")
	if e != nil {
		t.Fatal(e)
	}
	if a.IsAdmin() || !a.Allows("library:read") || a.Allows("library:write") {
		t.Fatal("token scope escaped")
	}
	if _, _, e = f.Accounts.MintToken(ctx, a, identity.TokenInput{Name: "Escaped", Scopes: []string{"profile:read"}, ExpiresInDays: 1, Password: testkit.Password}); !errors.Is(e, fault.Forbidden) {
		t.Fatal("token minted credential", e)
	}
	if e = f.Accounts.RevokeToken(ctx, f.Owner, token.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Accounts.Authenticate(ctx, raw, "token"); !errors.Is(e, fault.Unauthenticated) {
		t.Fatal("revoked token accepted", e)
	}
}
func TestDatabaseRateLimitSurvivesNewStore(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	if e := f.Accounts.Limit(ctx, "synthetic-ip", 1, time.Minute); e != nil {
		t.Fatal(e)
	}
	second := identity.New(f.Pool)
	if e := second.Limit(ctx, "synthetic-ip", 1, time.Minute); !errors.Is(e, fault.Limited) {
		t.Fatal("new process reset rate limit", e)
	}
}

func TestUnlinkIssuerPreservesSignIn(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	_, err := f.Pool.Exec(ctx, "UPDATE users SET password_hash='' WHERE id=$1", f.Owner.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Pool.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id) VALUES('https://synthetic.invalid','one',$1),('https://synthetic.invalid','two',$1)", f.Owner.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Accounts.UnlinkOIDC(ctx, f.Owner, "https://synthetic.invalid"); err == nil {
		t.Fatal("removed all remaining sign-in identities")
	}
	var count int
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM oidc_identities WHERE user_id=$1", f.Owner.User.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestOIDCLinkRequiresInitiatingSession(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	a := f.User(t, "oidc_link_member", "member")
	_, err := f.Accounts.BeginOIDC(ctx, "/account", &a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", a.User.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.Accounts.OIDCSignIn(ctx, identity.OIDCClaims{Issuer: "https://synthetic.invalid", Subject: "synthetic"}, &a, false)
	if !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("revoked session linked identity", err)
	}
}

func TestSweepRemovesExpiredSessions(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, "UPDATE sessions SET expires_at=clock_timestamp()-interval '2 days' WHERE user_id=$1", f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Accounts.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.Pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", f.Owner.User.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired session retained", count, err)
	}
}
