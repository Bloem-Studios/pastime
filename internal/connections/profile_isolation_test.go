package connections_test

import (
	"context"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/connections"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestConnectionsAreBoundToTrackingProfiles(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := connections.Store{Pool: f.Pool}
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	main, _, err := s.Connect(ctx, f.Owner, "generic", connections.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	second, raw, err := s.Connect(ctx, selected, "generic", connections.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if main.ID == second.ID {
		t.Fatal("child replaced default webhook connection")
	}
	for _, a := range []struct {
		profile  bool
		expected string
	}{{false, main.ID}, {true, second.ID}} {
		actor := f.Owner
		if a.profile {
			actor = selected
		}
		rows, err := s.List(ctx, actor)
		if err != nil || len(rows) != 1 || rows[0].ID != a.expected {
			t.Fatalf("connection isolation: %+v %v", rows, err)
		}
	}
	bound, err := s.Authenticate(ctx, "generic", raw)
	if err != nil {
		t.Fatal(err)
	}
	if bound.ProfileID != child.ID {
		t.Fatal("webhook worker did not receive its profile binding")
	}
}
