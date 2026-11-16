package identity_test

import (
	"context"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestProfileTokenInventoryBoundToItsProfile(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := f.Accounts.MintToken(ctx, selected, identity.TokenInput{Name: "Profile inventory", Scopes: []string{"profile:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.Accounts.Authenticate(ctx, raw, "token")
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := f.Accounts.Profiles(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].ID != child.ID {
		t.Fatalf("profile token exposed household inventory: %+v", profiles)
	}
}
