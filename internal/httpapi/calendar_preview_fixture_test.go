package httpapi

import (
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Explicitly selected, isolated fixture for desktop/mobile calendar acceptance.
func TestCalendarPreviewFixture(t *testing.T) {
	if os.Getenv("PASTIME_CALENDAR_PREVIEW") != "1" {
		t.Skip("explicit browser fixture only")
	}
	h := application(t)
	h.server.catalog = &catalog.Service{Pool: h.Pool}
	h.server.origin = "http://127.0.0.1:18165"
	// A synthetic key so the Connect Silo page can create keys in this fixture.
	h.server.secrets, _ = sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	member := h.User(t, "calendar_viewer", "member")
	const issuer = "https://sso.example.test/bloem"
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, "calendar-viewer", member.User.ID); err != nil {
		t.Fatal(err)
	}
	store := bloem.Store{Pool: h.Pool}
	instance, raw, err := store.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://media.example.test", Scope: "calendar", IdentityIssuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Provision(t.Context(), raw, bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", DefaultProfileID: "main", AccountName: "Household", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: issuer, IdentitySubject: "calendar-viewer"}); err != nil {
		t.Fatal(err)
	}
	// A favourite movie with no library entry, a followed (planned) series and a
	// live Silo snapshot that must stay hidden.
	today := time.Now().UTC()
	for _, title := range []struct{ kind, id, name, art string }{{"movie", "4242", "A Synthetic Premiere", "film"}, {"series", "4343", "A Synthetic Series", "series"}} {
		var id int64
		if err = h.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,image_url,backdrop_url) VALUES(0,$1,'tmdb',$2,$3,'episodes',$4,'/fixture/poster/wide.svg') RETURNING id`, title.kind, title.id, title.name, "/fixture/poster/"+title.art+".svg").Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err = h.Pool.Exec(t.Context(), `UPDATE catalog_items SET root_id=id WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if title.kind == "movie" {
			if _, err = h.Pool.Exec(t.Context(), `UPDATE catalog_items SET release_date=$2 WHERE id=$1`, id, today); err != nil {
				t.Fatal(err)
			}
			if err = h.server.library.SetMembership(t.Context(), member, testkit.Key(t, h.Fixture), id, library.PersonalMembershipInput{Kind: "favorite", Present: true}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err = h.Pool.Exec(t.Context(), `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number,release_date) VALUES('tmdb','synthetic-episode','episode','A New Chapter','episodes',$1,1,1,$2)`, id, today.AddDate(0, 0, 1)); err != nil {
			t.Fatal(err)
		}
		if _, err = h.server.library.Track(t.Context(), member, testkit.Key(t, h.Fixture), library.TrackInput{CatalogID: id}); err != nil {
			t.Fatal(err)
		}
	}
	h.server.Router.Get("/fixture/poster/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		if r.URL.Path == "/fixture/poster/wide.svg" {
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 450"><rect width="800" height="450" fill="#29434d"/><circle cx="540" cy="170" r="100" fill="#a8c6bc"/><path d="M0 450L270 130L570 450M380 450L610 220L800 450" fill="#142f38"/><text x="36" y="410" fill="#d6e0df" font-size="28">SYNTHETIC PREMIERE</text></svg>`))
			return
		}

		w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 300"><rect width="200" height="300" fill="#243747"/><circle cx="100" cy="105" r="62" fill="#718d94"/><path d="M0 270L95 130L200 270" fill="#172b35"/><text x="100" y="275" text-anchor="middle" fill="#d6e0df" font-size="16">SYNTHETIC PREMIERE</text></svg>`))
	})
	day := today.Format("2006-01-02")
	page := bloem.CalendarInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", SnapshotID: testkit.Key(t, h.Fixture), Start: day, End: today.AddDate(0, 0, 2).Format("2006-01-02"), VisibilityDigest: "scope", Revision: 1, Complete: true, ExpiresAt: time.Now().Add(120 * time.Second), Events: []bloem.CalendarEvent{
		{EventID: "film", ContentID: "movie/a?b#c", Kind: "movie", Title: "A Synthetic Premiere", Date: day, Playable: true, Favourite: true, ExternalIDs: map[string]string{"tmdb": "4242"}},
		{EventID: "silo-only", ContentID: "silo-only", Kind: "movie", Title: "A Silo-only Release", Date: day, Playable: true, Favourite: true},
	}}
	if _, err = store.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatal(err)
	}
	if err = store.PublishCalendar(t.Context(), raw, page.SnapshotID); err != nil {
		t.Fatal(err)
	}
	h.server.Router.Post("/fixture/renew", func(w http.ResponseWriter, r *http.Request) {
		// Keeps the Silo snapshot live, so the page proves it is ignored rather than expired.
		if _, err := h.Pool.Exec(r.Context(), `UPDATE bloem_calendar_snapshots SET expires_at=now()+interval '120 seconds'`); err != nil {
			http.Error(w, "fixture renewal failed", 500)
			return
		}
		w.WriteHeader(204)
	})
	finish := make(chan struct{}, 1)
	h.server.Router.Post("/fixture/finish", func(w http.ResponseWriter, r *http.Request) { finish <- struct{}{}; w.WriteHeader(204) })
	listener, err := net.Listen("tcp", "127.0.0.1:18165")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: h.server.Router, ReadHeaderTimeout: time.Second}
	defer server.Close()
	go server.Serve(listener)
	t.Log("synthetic calendar preview ready on port 18165")
	select {
	case <-finish:
	case <-t.Context().Done():
	}
}
