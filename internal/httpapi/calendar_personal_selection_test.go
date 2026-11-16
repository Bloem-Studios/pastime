package httpapi

import (
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Local favourite and watchlist choices decide the calendar; a Silo snapshot left by
// the earlier plugin neither adds titles nor overrides those choices.
func TestCalendarFollowsLocalMembershipsNotSourceSnapshot(t *testing.T) {
	h := application(t)
	member := h.User(t, "selection_viewer", "member")
	const issuer = "https://sso.example.test/bloem"
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,'selection-viewer',$2)`, issuer, member.User.ID); err != nil {
		t.Fatal(err)
	}
	store := bloem.Store{Pool: h.Pool}
	instance, raw, err := store.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://media.example.test", Scope: "calendar", IdentityIssuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Provision(t.Context(), raw, bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", DefaultProfileID: "main", AccountName: "Household", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: issuer, IdentitySubject: "selection-viewer"}); err != nil {
		t.Fatal(err)
	}
	date := time.Now().UTC().Truncate(24 * time.Hour)
	day := date.Format("2006-01-02")
	var id int64
	if err = h.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,release_date) VALUES(0,'movie','tmdb','4242','Synthetic release','minutes',$1) RETURNING id`, date).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Pool.Exec(t.Context(), `UPDATE catalog_items SET root_id=id WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	page := bloem.CalendarInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", SnapshotID: testkit.Key(t, h.Fixture), Start: day, End: day, VisibilityDigest: "scope", Revision: 1, Complete: true, ExpiresAt: time.Now().Add(time.Minute), Events: []bloem.CalendarEvent{{EventID: "event", ContentID: "content", Kind: "movie", Title: "Synthetic release", Date: day, Favourite: true, Watchlist: true, ExternalIDs: map[string]string{"tmdb": "4242"}}}}
	if _, err = store.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatal(err)
	}
	if err = store.PublishCalendar(t.Context(), raw, page.SnapshotID); err != nil {
		t.Fatal(err)
	}
	check := func(filter string, want int) {
		t.Helper()
		out, err := h.server.mergedCalendar(t.Context(), member, date, date, filter)
		if err != nil || len(out) != want {
			t.Fatalf("filter %s: got %d want %d: %v", filter, len(out), want, err)
		}
		for _, r := range out {
			if r.SourceEventID != "" || r.CatalogID == nil {
				t.Fatalf("source row shown: %+v", r)
			}
		}
	}
	change := func(kind string, present bool) {
		t.Helper()
		tx, err := h.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		if _, err = h.server.library.ApplyPersonalStateTx(t.Context(), tx, member, id, library.PersonalState{Kind: kind, Present: present}, time.Now(), "native"); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	check("all", 0)
	check("favourites", 0)
	change("favorite", true)
	check("all", 1)
	check("favourites", 1)
	check("watchlist", 0)
	check("following", 0)
	// An unrelated profile's tombstone cannot affect this viewer.
	if _, err = h.Pool.Exec(t.Context(), `INSERT INTO profile_catalog_memberships(profile_id,catalog_item_id,kind,present,source) VALUES($1,$2,'favorite',false,'native')`, h.Owner.TrackingID(), id); err != nil {
		t.Fatal(err)
	}
	check("favourites", 1)
	change("favorite", false)
	check("favourites", 0)
	check("all", 0)
	// Joining the watchlist plans the title, so it is followed too.
	change("watchlist", true)
	check("watchlist", 1)
	check("following", 1)
	check("all", 1)
	change("watchlist", false)
	check("watchlist", 0)
	check("favourites", 0)
	change("favorite", true)
	check("favourites", 1)
	check("all", 1)
}
