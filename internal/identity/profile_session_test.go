package identity_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// addLogin gives profileID, a profile of household, a profile login directly
// in the database, so these tests don't depend on the management methods.
func addLogin(t *testing.T, f *testkit.Fixture, household identity.Actor, profileID int64, name, password string) int64 {
	t.Helper()
	hash, err := f.Accounts.Passwords.Hash(t.Context(), password)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = f.Pool.QueryRow(t.Context(), `INSERT INTO profile_logins(account_id,profile_id,login_name,password_hash) VALUES($1,$2,$3,$4) RETURNING id`, household.User.ID, profileID, name, hash).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// samSession creates household's profile "Sam" with the login "sam" and signs
// in with it.
func samSession(t *testing.T, f *testkit.Fixture, household identity.Actor) (identity.TrackingProfile, int64, string) {
	t.Helper()
	sam, err := f.Accounts.CreateProfile(t.Context(), household, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	login := addLogin(t, f, household, sam.ID, "sam", testkit.Password)
	return sam, login, signIn(t, f, "SAM ")
}

func signIn(t *testing.T, f *testkit.Fixture, name string) string {
	t.Helper()
	_, raw, err := f.Accounts.Login(t.Context(), name, testkit.Password)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProfileLoginSignsInBoundToProfile(t *testing.T) {
	f := testkit.New(t)
	sam, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	addLogin(t, f, f.Owner, sam.ID, "sam", testkit.Password)
	u, raw, err := f.Accounts.Login(t.Context(), "SAM ", testkit.Password)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "sam" || u.DisplayName != "Sam" || u.Email != "" || u.Role != "member" {
		t.Fatalf("sign-in returned the household account: %+v", u)
	}
	a, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession)
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != identity.KindProfileSession || a.TrackingID() != sam.ID || a.ProfileLoginID() == 0 || a.CSRF == "" {
		t.Fatalf("profile session not bound to its profile: kind=%q profile=%d login=%d", a.Kind, a.TrackingID(), a.ProfileLoginID())
	}
	if a.User.ID != f.Owner.User.ID {
		t.Fatal("profile session must belong to the household's account")
	}
	if got := a.PublicUser(); got.Username != "sam" || got.Email != "" {
		t.Fatalf("public user: %+v", got)
	}
	var lastLogin *time.Time
	if err = f.Pool.QueryRow(t.Context(), `SELECT last_login_at FROM profile_logins WHERE profile_id=$1`, sam.ID).Scan(&lastLogin); err != nil || lastLogin == nil {
		t.Fatal("sign-in did not update the login's last use", err)
	}
}

func TestProfileSessionCannotSelectSibling(t *testing.T) {
	f := testkit.New(t)
	_, _, raw := samSession(t, f, f.Owner)
	a, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.SelectProfile(t.Context(), a, f.Owner.TrackingID()); !errors.Is(err, fault.Forbidden) {
		t.Fatalf("profile session selected the household's default profile: %v", err)
	}
	tx, err := f.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	// An actor whose kind was changed after authentication is refused.
	forged := a
	forged.Kind = identity.KindSession
	if _, err = identity.AuthorizeTx(t.Context(), tx, forged, ""); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("forged account kind on a profile session: %v", err)
	}
}

func TestProfileSessionEndsOnRevocation(t *testing.T) {
	const issuer = "https://sso.example.test/application/o/pastime/"
	const subject = "00000000-0000-4000-8000-000000000077"
	publish := func(t *testing.T, f *testkit.Fixture, grants string) {
		t.Helper()
		now := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := f.Pool.Exec(t.Context(), `SELECT pastime_apply_service_access($1,$2,$3,$4::jsonb)`, issuer, now, now.Add(time.Minute), grants); err != nil {
			t.Fatal(err)
		}
	}
	for name, revoke := range map[string]func(t *testing.T, f *testkit.Fixture, household identity.Actor, profile int64){
		"login removed": func(t *testing.T, f *testkit.Fixture, _ identity.Actor, profile int64) {
			if _, err := f.Pool.Exec(t.Context(), `DELETE FROM profile_logins WHERE profile_id=$1`, profile); err != nil {
				t.Fatal(err)
			}
		},
		"profile archived": func(t *testing.T, f *testkit.Fixture, _ identity.Actor, profile int64) {
			if _, err := f.Pool.Exec(t.Context(), `UPDATE tracking_profiles SET archived_at=now() WHERE id=$1`, profile); err != nil {
				t.Fatal(err)
			}
		},
		"account deactivated": func(t *testing.T, f *testkit.Fixture, household identity.Actor, _ int64) {
			if _, err := f.Pool.Exec(t.Context(), `UPDATE users SET active=false WHERE id=$1`, household.User.ID); err != nil {
				t.Fatal(err)
			}
		},
		"account revoked": func(t *testing.T, f *testkit.Fixture, household identity.Actor, _ int64) {
			if _, err := f.Pool.Exec(t.Context(), `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, household.User.ID); err != nil {
				t.Fatal(err)
			}
		},
		"service access lost": func(t *testing.T, f *testkit.Fixture, _ identity.Actor, _ int64) {
			publish(t, f, `[]`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := testkit.New(t)
			// Owners and administrators always have service access, so the
			// household here is an ordinary member account.
			household := f.User(t, "paying_household", "member")
			if _, err := f.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, subject, household.User.ID); err != nil {
				t.Fatal(err)
			}
			publish(t, f, `[{"subject":"`+subject+`","expires_at":"`+time.Now().Add(50*time.Second).UTC().Format(time.RFC3339Nano)+`"}]`)
			sam, _, raw := samSession(t, f, household)
			if _, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); err != nil {
				t.Fatal("profile session before revocation", err)
			}
			revoke(t, f, household, sam.ID)
			if _, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); !errors.Is(err, fault.Unauthenticated) {
				t.Fatalf("profile session survived: %v", err)
			}
		})
	}
}

func TestProfileSessionsHaveTheirOwnLimit(t *testing.T) {
	f := testkit.New(t)
	_, login, first := samSession(t, f, f.Owner)
	for range 11 {
		signIn(t, f, "sam")
	}
	if _, err := f.Accounts.Authenticate(t.Context(), f.OwnerRaw, identity.KindSession); err != nil {
		t.Fatal("profile sign-ins pushed out the primary user's session", err)
	}
	if _, err := f.Accounts.Authenticate(t.Context(), first, identity.KindSession); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("the login's oldest session should be over its limit: %v", err)
	}
	var open int
	if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE profile_login_id=$1 AND revoked_at IS NULL`, login).Scan(&open); err != nil || open != 10 {
		t.Fatalf("open sessions for the login: %d (%v), want 10", open, err)
	}
	kept := signIn(t, f, "sam")
	for range 11 {
		signIn(t, f, "fixture_owner")
	}
	if _, err := f.Accounts.Authenticate(t.Context(), kept, identity.KindSession); err != nil {
		t.Fatal("account sign-ins pushed out a profile session", err)
	}
}

func TestWrongPasswordForProfileLogin(t *testing.T) {
	f := testkit.New(t)
	sam, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	addLogin(t, f, f.Owner, sam.ID, "sam", testkit.Password)
	if _, _, err = f.Accounts.Login(t.Context(), "sam", "synthetic-wrong-password-2026"); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err = f.Accounts.Login(t.Context(), "nobody_here", testkit.Password); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("unknown login: %v", err)
	}
}
