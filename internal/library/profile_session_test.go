package library_test

import (
	"errors"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Collaborative lists and recommendations are shared between accounts, so
// only someone acting for the whole household may use them.
func TestProfileSessionCannotUseCollabOrRecommend(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := t.Context()
	shared, err := s.CreateCollab(ctx, f.Owner, library.CollabInput{Name: "Household picks"})
	if err != nil {
		t.Fatal(err)
	}
	_, sam, _ := f.ProfileLogin(t, "Sam", "sam")
	// A token Sam mints is bound to Sam's profile; it can't reach them either.
	_, samKey, err := f.Accounts.MintToken(ctx, sam, identity.TokenInput{Name: "Sam lists", Scopes: []string{"lists:read", "lists:write"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	samToken, err := f.Accounts.Authenticate(ctx, samKey, identity.KindToken)
	if err != nil {
		t.Fatal(err)
	}
	for who, a := range map[string]identity.Actor{"profile session": sam, "profile token": samToken} {
		checks := map[string]error{}
		_, checks["CollabLists"] = s.CollabLists(ctx, a)
		_, checks["Collab"] = s.Collab(ctx, a, shared.ID)
		_, checks["CreateCollab"] = s.CreateCollab(ctx, a, library.CollabInput{Name: "Mine"})
		checks["UpdateCollab"] = s.UpdateCollab(ctx, a, shared.ID, library.CollabInput{Name: "Renamed", Version: shared.Version})
		checks["DeleteCollab"] = s.DeleteCollab(ctx, a, shared.ID)
		checks["RemoveCollabMember"] = s.RemoveCollabMember(ctx, a, shared.ID, "fixture_owner")
		_, checks["Recommend"] = s.Recommend(ctx, a, library.RecommendInput{To: "fixture_owner", CatalogID: "1"})
		_, checks["Recipients"] = s.Recipients(ctx, a, "fix")
		_, checks["Recommendations"] = s.Recommendations(ctx, a, "inbox")
		for name, err := range checks {
			if !errors.Is(err, fault.Forbidden) {
				t.Errorf("%s from a %s: %v", name, who, err)
			}
		}
	}
	// The primary user's own token on their default profile keeps working.
	_, ownerKey, err := f.Accounts.MintToken(ctx, f.Owner, identity.TokenInput{Name: "Owner lists", Scopes: []string{"lists:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := f.Accounts.Authenticate(ctx, ownerKey, identity.KindToken)
	if err != nil {
		t.Fatal(err)
	}
	if lists, err := s.CollabLists(ctx, ownerToken); err != nil || len(lists) != 1 {
		t.Fatalf("owner token lost its collaborative lists: %+v %v", lists, err)
	}
}
