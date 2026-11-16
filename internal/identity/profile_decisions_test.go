package identity_test

import (
	"errors"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// samActor signs in as the household's profile "Sam" and returns the
// authenticated profile session.
func samActor(t *testing.T, f *testkit.Fixture) (identity.TrackingProfile, identity.Actor, string) {
	t.Helper()
	sam, _, raw := samSession(t, f, f.Owner)
	a, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindSession)
	if err != nil {
		t.Fatal(err)
	}
	return sam, a, raw
}

func TestProfileSessionTokensStayOnItsProfile(t *testing.T) {
	f := testkit.New(t)
	sam, a, _ := samActor(t, f)
	ownerToken, _, err := f.Accounts.MintToken(t.Context(), f.Owner, identity.TokenInput{Name: "Owner scrobbler", Scopes: []string{"library:write"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	// The profile person proves it's them with their own password.
	if _, _, err = f.Accounts.MintToken(t.Context(), a, identity.TokenInput{Name: "Wrong", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: "synthetic-wrong-password-2026"}); err == nil {
		t.Fatal("minted a token with the wrong password")
	}
	samToken, raw, err := f.Accounts.MintToken(t.Context(), a, identity.TokenInput{Name: "Sam scrobbler", Scopes: []string{"library:write"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	if samToken.ProfileID != sam.ID {
		t.Fatalf("token profile: %d, want %d", samToken.ProfileID, sam.ID)
	}
	bound, err := f.Accounts.Authenticate(t.Context(), raw, identity.KindToken)
	if err != nil || bound.TrackingID() != sam.ID {
		t.Fatal("token not bound to Sam", bound.TrackingID(), err)
	}
	tokens, err := f.Accounts.Tokens(t.Context(), a)
	if err != nil || len(tokens) != 1 || tokens[0].ID != samToken.ID {
		t.Fatalf("profile token list: %+v %v", tokens, err)
	}
	if err = f.Accounts.RevokeToken(t.Context(), a, ownerToken.ID); !errors.Is(err, fault.NotFound) {
		t.Fatalf("profile session revoked the owner's token: %v", err)
	}
	all, err := f.Accounts.Tokens(t.Context(), f.Owner)
	if err != nil || len(all) != 2 {
		t.Fatalf("household token list: %+v %v", all, err)
	}
	for _, tk := range all {
		if tk.ProfileID == 0 {
			t.Fatalf("household token without its profile: %+v", tk)
		}
	}
	if err = f.Accounts.RevokeToken(t.Context(), a, samToken.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAccountSessionListExcludesProfileSessions(t *testing.T) {
	f := testkit.New(t)
	_, a, raw := samActor(t, f)
	_, other := f.Login(t, "fixture_owner")
	owner, err := f.Accounts.Sessions(t.Context(), f.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(owner) != 2 {
		t.Fatalf("owner sessions: %d, want 2 (no profile sessions)", len(owner))
	}
	for _, s := range owner {
		if s.ID == a.CredentialID {
			t.Fatal("owner's list shows the profile session")
		}
	}
	mine, err := f.Accounts.Sessions(t.Context(), a)
	if err != nil || len(mine) != 1 || mine[0].ID != a.CredentialID || !mine[0].Current {
		t.Fatalf("profile session list: %+v %v", mine, err)
	}
	otherActor, err := f.Accounts.Authenticate(t.Context(), other, identity.KindSession)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Accounts.RevokeSession(t.Context(), a, otherActor.CredentialID); !errors.Is(err, fault.NotFound) {
		t.Fatalf("profile session revoked an owner session: %v", err)
	}
	if err = f.Accounts.RevokeSession(t.Context(), f.Owner, a.CredentialID); !errors.Is(err, fault.NotFound) {
		t.Fatalf("owner revoked a profile session from their own list: %v", err)
	}
	if err = f.Accounts.RevokeSession(t.Context(), f.Owner, otherActor.CredentialID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); err != nil {
		t.Fatal("revoking an owner session ended the profile session", err)
	}
	if err = f.Accounts.Logout(t.Context(), a); err != nil {
		t.Fatal("profile person signs out", err)
	}
	if _, err = f.Accounts.Authenticate(t.Context(), raw, identity.KindSession); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("signed-out profile session still works: %v", err)
	}
}

func TestProfileChangePassword(t *testing.T) {
	f := testkit.New(t)
	_, a, _ := samActor(t, f)
	if err := f.Accounts.ChangePassword(t.Context(), a, testkit.Password, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Accounts.Login(t.Context(), "sam", testkit.Password); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("old password still signs in: %v", err)
	}
	signInWith(t, f, "sam", newPassword)
	signIn(t, f, "fixture_owner")
}

func TestProfileApprovedDeviceTokenBindsProfile(t *testing.T) {
	f := testkit.New(t)
	sam, a, _ := samActor(t, f)
	start, err := f.Accounts.StartDevice(t.Context(), "Synthetic TV", []string{"library:read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.LookupDevice(t.Context(), a, start.UserCode); err != nil {
		t.Fatal("profile person looks up a device code", err)
	}
	if err = f.Accounts.DecideDevice(t.Context(), a, start.UserCode, true); err != nil {
		t.Fatal(err)
	}
	poll, err := f.Accounts.PollDevice(t.Context(), start.DeviceCode)
	if err != nil || poll.Token == nil {
		t.Fatalf("poll: %+v %v", poll, err)
	}
	token, err := f.Accounts.Authenticate(t.Context(), *poll.Token, identity.KindToken)
	if err != nil || token.TrackingID() != sam.ID {
		t.Fatalf("device token bound to profile %d (%v), want %d", token.TrackingID(), err, sam.ID)
	}
	// An approval by the account keeps binding to the default profile.
	start, err = f.Accounts.StartDevice(t.Context(), "Synthetic phone", []string{"library:read"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Accounts.DecideDevice(t.Context(), f.Owner, start.UserCode, true); err != nil {
		t.Fatal(err)
	}
	if poll, err = f.Accounts.PollDevice(t.Context(), start.DeviceCode); err != nil || poll.Token == nil {
		t.Fatalf("poll: %+v %v", poll, err)
	}
	if token, err = f.Accounts.Authenticate(t.Context(), *poll.Token, identity.KindToken); err != nil || token.TrackingID() != f.Owner.TrackingID() {
		t.Fatalf("account device token bound to %d (%v)", token.TrackingID(), err)
	}
}

func TestProfileSessionHouseholdOnlyIdentity(t *testing.T) {
	f := testkit.New(t)
	_, a, _ := samActor(t, f)
	checks := map[string]error{}
	_, checks["TOTPStatus"] = f.Accounts.TOTPStatus(t.Context(), a)
	_, checks["BeginTOTP"] = f.Accounts.BeginTOTP(t.Context(), a, testkit.Password, "Pastime")
	_, checks["ConfirmTOTP"] = f.Accounts.ConfirmTOTP(t.Context(), a, "000000")
	checks["DisableTOTP"] = f.Accounts.DisableTOTP(t.Context(), a, testkit.Password)
	_, checks["OIDCIdentities"] = f.Accounts.OIDCIdentities(t.Context(), a)
	checks["UnlinkOIDC"] = f.Accounts.UnlinkOIDC(t.Context(), a, "https://sso.example.test/")
	_, _, checks["OIDCSignIn link"] = f.Accounts.OIDCSignIn(t.Context(), identity.OIDCClaims{Issuer: "https://sso.example.test/", Subject: "00000000-0000-4000-8000-000000000099"}, &a, false)
	for name, err := range checks {
		if !errors.Is(err, fault.Forbidden) {
			t.Errorf("%s from a profile session: %v", name, err)
		}
	}
}

func TestProfileSessionCalendarFeed(t *testing.T) {
	f := testkit.New(t)
	sam, a, _ := samActor(t, f)
	raw, err := f.Accounts.RotateFeed(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	feed, err := f.Accounts.FeedActor(t.Context(), raw)
	if err != nil || feed.TrackingID() != sam.ID {
		t.Fatalf("feed bound to %d (%v), want %d", feed.TrackingID(), err, sam.ID)
	}
	if created, err := f.Accounts.FeedStatus(t.Context(), a); err != nil || created == nil {
		t.Fatal("feed status", created, err)
	}
	if err = f.Accounts.RevokeFeed(t.Context(), a); err != nil {
		t.Fatal(err)
	}
}
