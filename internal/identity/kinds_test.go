package identity

import (
	"testing"
	"time"
)

func TestProfileSessionIsBrowserNotHousehold(t *testing.T) {
	a := Actor{Kind: KindProfileSession, User: User{Role: "owner"}}
	if !a.Browser() {
		t.Fatal("a profile session is a browser session")
	}
	if a.Household() {
		t.Fatal("a profile session must not act for the household")
	}
	if a.IsAdmin() {
		t.Fatal("a profile session must never be an administrator, whatever the account's role")
	}
	if !a.Allows("library:write") {
		t.Fatal("a profile session may do everything for its own profile")
	}
	account := Actor{Kind: KindSession, User: User{Role: "owner"}}
	if !account.Browser() || !account.Household() || !account.IsAdmin() {
		t.Fatal("an account session keeps full trust")
	}
	token := Actor{Kind: KindToken, Scopes: []string{"library:read"}}
	if token.Browser() || token.Household() || token.Allows("library:write") {
		t.Fatal("a token is neither kind of session and keeps its scopes")
	}
}

func TestPublicUserHidesHousehold(t *testing.T) {
	seen := time.Now()
	a := Actor{Kind: KindProfileSession, loginName: "sam", profileName: "Sam", User: User{Username: "family", DisplayName: "Family", Email: "a@example.test", Role: "admin", LastLoginAt: &seen}}
	u := a.PublicUser()
	if u.Email != "" || u.Role != "member" || u.Username != "sam" || u.DisplayName != "Sam" || !u.PasswordReady || u.LastLoginAt != nil {
		t.Fatalf("profile session exposes the household account: %+v", u)
	}
	account := Actor{Kind: KindSession, User: User{Username: "family", Email: "a@example.test", Role: "admin"}}
	if got := account.PublicUser(); got != account.User {
		t.Fatalf("account session user changed: %+v", got)
	}
}

func TestValidateLoginName(t *testing.T) {
	for in, want := range map[string]string{" Sam.K ": "sam.k", "Sam@Example.TEST": "sam@example.test"} {
		got, err := ValidateLoginName(in)
		if err != nil || got != want {
			t.Fatalf("ValidateLoginName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"ab", "no spaces@x", "", "  "} {
		if got, err := ValidateLoginName(in); err == nil {
			t.Fatalf("ValidateLoginName(%q) = %q, want an error", in, got)
		}
	}
}
