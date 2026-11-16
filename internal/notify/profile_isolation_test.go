package notify

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestNotificationPreferencesAreProfileScoped(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool}
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings(ctx, f.Owner)
	if err != nil {
		t.Fatal(err)
	}
	settings.Releases = false
	if _, err = s.SaveSettings(ctx, f.Owner, settings); err != nil {
		t.Fatal(err)
	}
	second, err := s.Settings(ctx, selected)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Releases {
		t.Fatal("child inherited default profile notification preference")
	}
}
