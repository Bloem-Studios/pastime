// Package testkit creates independently authored synthetic accounts in isolated test schemas.
package testkit

import (
	"context"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Password = "synthetic-test-password-2026"

type Fixture struct {
	Pool     *pgxpool.Pool
	Accounts *identity.Store
	Owner    identity.Actor
	OwnerRaw string
}

func New(t testing.TB) *Fixture {
	t.Helper()
	p := testdb.Isolated(t)
	f := &Fixture{Pool: p, Accounts: identity.New(p)}
	if _, err := f.Accounts.Bootstrap(context.Background(), "fixture_owner", "Fixture Owner", Password); err != nil {
		t.Fatal(err)
	}
	f.Owner, f.OwnerRaw = f.Login(t, "fixture_owner")
	return f
}
func (f *Fixture) Login(t testing.TB, username string) (identity.Actor, string) {
	t.Helper()
	_, raw, err := f.Accounts.Login(context.Background(), username, Password)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.Accounts.Authenticate(context.Background(), raw, "session")
	if err != nil {
		t.Fatal(err)
	}
	return a, raw
}
func (f *Fixture) User(t testing.TB, username, role string) identity.Actor {
	t.Helper()
	ctx := context.Background()
	_, raw, err := f.Accounts.Invite(ctx, f.Owner, identity.InviteInput{Role: role})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Claim(ctx, raw, username, username, Password); err != nil {
		t.Fatal(err)
	}
	a, _ := f.Login(t, username)
	return a
}

// ProfileLogin adds the household profile name to the fixture owner's account,
// gives it the login loginName with Password, signs in with it, and returns the
// profile, the authenticated profile session and its raw cookie value.
func (f *Fixture) ProfileLogin(t testing.TB, name, loginName string) (identity.TrackingProfile, identity.Actor, string) {
	t.Helper()
	ctx := context.Background()
	p, err := f.Accounts.CreateProfile(ctx, f.Owner, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.SetProfileLogin(ctx, f.Owner, p.ID, identity.ProfileLoginInput{LoginName: loginName, Password: Password}); err != nil {
		t.Fatal(err)
	}
	a, raw := f.Login(t, loginName)
	if a.Kind != identity.KindProfileSession {
		t.Fatal("profile login did not open a profile session")
	}
	return p, a, raw
}
func Key(t testing.TB, f *Fixture) string {
	t.Helper()
	var key string
	if err := f.Pool.QueryRow(context.Background(), "SELECT gen_random_uuid()::text").Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}
