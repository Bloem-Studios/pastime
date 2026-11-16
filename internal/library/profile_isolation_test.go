package library_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestManagedProfileLibraryIsolation(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	initial := create(t, f, lib, f.Owner, "Existing private history")
	profiles, err := f.Accounts.Profiles(ctx, f.Owner)
	if err != nil || len(profiles) != 1 || !profiles[0].Default {
		t.Fatalf("default profiles: %+v %v", profiles, err)
	}
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.User.ID != f.Owner.User.ID || selected.IsAdmin() != f.Owner.IsAdmin() {
		t.Fatal("profile changed login identity")
	}
	if _, err = lib.Get(ctx, selected, initial.ID); !errors.Is(err, fault.NotFound) {
		t.Fatal("profile read default history", err)
	}
	// An identical idempotency key is independent for each profile.
	key := testkit.Key(t, f)
	first, err := lib.Create(ctx, f.Owner, key, input("Household title"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Create(ctx, selected, key, input("Household title"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("profile mutation receipt was shared")
	}
	if _, err = lib.Get(ctx, f.Owner, second.ID); !errors.Is(err, fault.NotFound) {
		t.Fatal("default read child", err)
	}
	if _, err = lib.Replace(ctx, f.Owner, second.ID, testkit.Key(t, f), library.EntryInput{Version: 1, Title: "Oops", MediaType: "book", Unit: "pages", Status: "planned"}); !errors.Is(err, fault.NotFound) {
		t.Fatal("default changed child", err)
	}
	stats, err := lib.Stats(ctx, selected)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 {
		t.Fatalf("child stats leaked: %+v", stats)
	}
	cache := &memoryCache{data: map[string][]byte{}}
	lib.Cache = cache
	from, to := time.Now().AddDate(-1, 0, 0), time.Now().Add(time.Hour)
	if _, err = lib.Insights(ctx, f.Owner, from, to); err != nil {
		t.Fatal(err)
	}
	if _, err = lib.Insights(ctx, selected, from, to); err != nil {
		t.Fatal(err)
	}
	if cache.hits != 0 {
		t.Fatal("profile statistics shared cache")
	}
	member := f.User(t, "different_household", "member")
	if _, err = f.Accounts.SelectProfile(ctx, member, child.ID); !errors.Is(err, fault.NotFound) {
		t.Fatal("cross account selection", err)
	}
	token, raw, err := f.Accounts.MintToken(ctx, selected, identity.TokenInput{Name: "Child read", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: testkit.Password})
	_ = token
	if err != nil {
		t.Fatal(err)
	}
	bound, err := f.Accounts.Authenticate(ctx, raw, "token")
	if err != nil {
		t.Fatal(err)
	}
	if bound.TrackingID() != child.ID {
		t.Fatal("token lost profile binding")
	}
	if _, err = f.Accounts.SelectProfile(ctx, bound, profiles[0].ID); !errors.Is(err, fault.Forbidden) {
		t.Fatal("token switched profiles", err)
	}
}

func TestHouseholdPublicShareNamesRemainUnambiguous(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	slug := "household-share"
	if _, err = lib.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Main shelf", Visibility: "public", Slug: &slug}); err != nil {
		t.Fatal(err)
	}
	if _, err = lib.SaveList(ctx, selected, 0, testkit.Key(t, f), library.ListInput{Name: "Child shelf", Visibility: "public", Slug: &slug}); fault.Public(err).Code != "duplicate_slug" {
		t.Fatal("same public URL addressed different household profiles", err)
	}
	public, err := lib.Public(ctx, f.Owner.User.Username, slug)
	if err != nil || public.Name != "Main shelf" {
		t.Fatal("share resolved wrong profile", public, err)
	}
}
