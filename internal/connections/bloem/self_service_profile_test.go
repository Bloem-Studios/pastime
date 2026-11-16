package bloem_test

import (
	"errors"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// A profile person connects Silo for their own profile only, and the Connect
// Silo page doesn't show them the household's other profiles.
func TestProfileSessionSiloConnectSeesOnlyItself(t *testing.T) {
	f := testkit.New(t)
	box, err := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	s := bloem.Store{Pool: f.Pool, Box: box}
	sam, a, _ := f.ProfileLogin(t, "Sam", "sam")
	view, err := s.ConnectView(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Profiles) != 1 || view.Profiles[0].ProfileID != sam.ID {
		t.Fatalf("profile session sees: %+v", view.Profiles)
	}
	household, err := s.ConnectView(t.Context(), f.Owner)
	if err != nil || len(household.Profiles) != 2 {
		t.Fatalf("household sees: %+v %v", household.Profiles, err)
	}
	if _, err = s.IssueSelfServiceCredential(t.Context(), a, f.Owner.TrackingID(), ""); !errors.Is(err, fault.NotFound) {
		t.Fatalf("profile session issued a key for the default profile: %v", err)
	}
	if err = s.RevokeSelfServiceCredentials(t.Context(), a, f.Owner.TrackingID()); !errors.Is(err, fault.NotFound) {
		t.Fatalf("profile session revoked the default profile's keys: %v", err)
	}
	if err = s.RevokeSelfServiceCredentials(t.Context(), a, sam.ID); err != nil {
		t.Fatal("profile session revokes its own keys", err)
	}
}
