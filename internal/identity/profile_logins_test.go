package identity_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

const newPassword = "synthetic-new-password-2026"

func TestPrimarySetsAndResetsProfileLogin(t *testing.T) {
	f := testkit.New(t)
	sam, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	login, err := f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: " Sam ", Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	if login.ProfileID != sam.ID || login.LoginName != "sam" {
		t.Fatalf("created login: %+v", login)
	}
	raw := signIn(t, f, "sam")
	// Renaming without a password keeps the person signed in.
	if login, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: "sam.k"}); err != nil || login.LoginName != "sam.k" {
		t.Fatal("rename", login, err)
	}
	if _, err = f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); err != nil {
		t.Fatal("rename signed the profile person out", err)
	}
	if _, _, err = f.Accounts.Login(t.Context(), "sam", testkit.Password); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("the old login name still signs in", err)
	}
	// A reset signs them out everywhere.
	if _, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: "sam.k", Password: newPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("reset kept the old session: %v", err)
	}
	if _, _, err = f.Accounts.Login(t.Context(), "sam.k", newPassword); err != nil {
		t.Fatal("new password", err)
	}
	logins, err := f.Accounts.ProfileLogins(t.Context(), f.Owner)
	if err != nil || len(logins) != 1 || logins[0].LoginName != "sam.k" || logins[0].LastLoginAt == nil {
		t.Fatalf("listed logins: %+v %v", logins, err)
	}
	// Turning it off ends the session and the sign-in.
	raw = signInWith(t, f, "sam.k", newPassword)
	if err = f.Accounts.RemoveProfileLogin(t.Context(), f.Owner, sam.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("removal kept the session: %v", err)
	}
	if err = f.Accounts.RemoveProfileLogin(t.Context(), f.Owner, sam.ID); !errors.Is(err, fault.NotFound) {
		t.Fatalf("removing a missing login: %v", err)
	}
}

func signInWith(t *testing.T, f *testkit.Fixture, name, password string) string {
	t.Helper()
	_, raw, err := f.Accounts.Login(t.Context(), name, password)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCreatingLoginNeedsPassword(t *testing.T) {
	f := testkit.New(t)
	sam, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	var invalid *fault.Error
	if _, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: "sam"}); !errors.As(err, &invalid) || invalid.Status != 422 {
		t.Fatalf("login without a password: %v", err)
	}
}

func TestProfileLoginRefusesDefaultAndArchived(t *testing.T) {
	f := testkit.New(t)
	old, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE tracking_profiles SET archived_at=now() WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]int64{"default": f.Owner.TrackingID(), "archived": old.ID} {
		var invalid *fault.Error
		_, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, id, identity.ProfileLoginInput{LoginName: "sam", Password: testkit.Password})
		if !errors.As(err, &invalid) || invalid.Status != 422 {
			t.Fatalf("%s profile: %v", name, err)
		}
	}
}

func TestArchivingProfileRemovesLogin(t *testing.T) {
	f := testkit.New(t)
	sam, _, raw := samSession(t, f, f.Owner)
	if _, err := f.Pool.Exec(t.Context(), `UPDATE tracking_profiles SET archived_at=now() WHERE id=$1`, sam.ID); err != nil {
		t.Fatal(err)
	}
	// Bloem can bring an archived profile back; its old login must not return with it.
	if _, err := f.Pool.Exec(t.Context(), `UPDATE tracking_profiles SET archived_at=NULL WHERE id=$1`, sam.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("session survived archiving: %v", err)
	}
	if _, _, err := f.Accounts.Login(t.Context(), "sam", testkit.Password); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("login survived archiving: %v", err)
	}
}

func TestProfileLoginNameCollision(t *testing.T) {
	f := testkit.New(t)
	sam, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	kit, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Kit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: "Fixture_Owner", Password: testkit.Password}); !errors.Is(err, fault.Conflict) {
		t.Fatalf("login named after the account: %v", err)
	}
	if _, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: "sam", Password: testkit.Password}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, kit.ID, identity.ProfileLoginInput{LoginName: "SAM", Password: testkit.Password}); !errors.Is(err, fault.Conflict) {
		t.Fatalf("second login with the same name: %v", err)
	}
}

func TestProfileSessionCannotManageLogins(t *testing.T) {
	f := testkit.New(t)
	sam, _, raw := samSession(t, f, f.Owner)
	a, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]error{}
	_, checks["SetProfileLogin"] = f.Accounts.SetProfileLogin(t.Context(), a, sam.ID, identity.ProfileLoginInput{LoginName: "sam", Password: newPassword})
	checks["RemoveProfileLogin"] = f.Accounts.RemoveProfileLogin(t.Context(), a, sam.ID)
	_, checks["ProfileLogins"] = f.Accounts.ProfileLogins(t.Context(), a)
	_, checks["CreateProfile"] = f.Accounts.CreateProfile(t.Context(), a, "Another")
	for name, err := range checks {
		if !errors.Is(err, fault.Forbidden) {
			t.Errorf("%s from a profile session: %v", name, err)
		}
	}
	// The account's own session can't use the profile person's password change.
	if err = f.Accounts.ChangeProfileLoginPassword(t.Context(), f.Owner, testkit.Password, newPassword); !errors.Is(err, fault.Forbidden) {
		t.Errorf("ChangeProfileLoginPassword from the account session: %v", err)
	}
}

func TestProfilePersonChangesOwnPassword(t *testing.T) {
	f := testkit.New(t)
	_, _, raw := samSession(t, f, f.Owner)
	a, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession)
	if err != nil {
		t.Fatal(err)
	}
	var invalid *fault.Error
	if err = f.Accounts.ChangeProfileLoginPassword(t.Context(), a, "synthetic-wrong-password-2026", newPassword); !errors.As(err, &invalid) || invalid.Status != 422 {
		t.Fatalf("wrong current password: %v", err)
	}
	if err = f.Accounts.ChangeProfileLoginPassword(t.Context(), a, testkit.Password, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("password change kept the session: %v", err)
	}
	signInWith(t, f, "sam", newPassword)
	if _, err = f.Accounts.Authenticate(t.Context(), f.OwnerRaw, identity.KindSession); err != nil {
		t.Fatal("profile password change signed the primary user out", err)
	}
}

func TestProfileLoginAudited(t *testing.T) {
	f := testkit.New(t)
	sam, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: "sam", Password: testkit.Password}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.SetProfileLogin(t.Context(), f.Owner, sam.ID, identity.ProfileLoginInput{LoginName: "sam", Password: newPassword}); err != nil {
		t.Fatal(err)
	}
	if err = f.Accounts.RemoveProfileLogin(t.Context(), f.Owner, sam.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := f.Pool.Query(t.Context(), `SELECT action,target_id,metadata::text FROM audit_events WHERE action LIKE 'profile%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, target, metadata string
		if err = rows.Scan(&action, &target, &metadata); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(metadata, testkit.Password) || strings.Contains(metadata, newPassword) || strings.Contains(metadata, "password_hash") {
			t.Fatalf("%s audit metadata holds a password: %s", action, metadata)
		}
		actions = append(actions, action)
	}
	if got := strings.Join(actions, ","); got != "profile.created,profile_login.created,profile_login.password_reset,profile_login.removed" {
		t.Fatalf("audit actions: %s", got)
	}
}

// Single sign-on creates an account named after the person's preferred
// username. A profile login already using that name must not make every
// sign-in of that person fail; the account takes the next free name.
func TestSSOAccountSkipsProfileLoginNames(t *testing.T) {
	f := testkit.New(t)
	sam, err := f.Accounts.CreateProfile(t.Context(), f.Owner, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	addLogin(t, f, f.Owner, sam.ID, "newcomer", testkit.Password)
	u, _, err := f.Accounts.OIDCSignIn(t.Context(), identity.OIDCClaims{Issuer: "https://sso.example.test/", Subject: "00000000-0000-4000-8000-000000000123", PreferredUsername: "newcomer"}, nil, true)
	if err != nil {
		t.Fatalf("SSO account creation collided with a profile login: %v", err)
	}
	if u.Username != "newcomer2" {
		t.Fatalf("username %q, want newcomer2", u.Username)
	}
}
