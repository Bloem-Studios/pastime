package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// TestNativeAcceptanceFixture is an explicitly selected subprocess fixture. It
// serves the real API with synthetic metadata and an isolated migrated schema.
func TestNativeAcceptanceFixture(t *testing.T) {
	settings := os.Getenv("PASTIME_NATIVE_FIXTURE_SETTINGS")
	if settings == "" {
		t.Skip("cross-repository acceptance fixture only")
	}
	if !strings.HasPrefix(settings, os.TempDir()+"/") {
		t.Fatalf("fixture output must be temporary: settings=%q temporary=%q", settings, os.TempDir())
	}
	instance := os.Getenv("PASTIME_NATIVE_FIXTURE_INSTANCE")
	scope := os.Getenv("PASTIME_NATIVE_FIXTURE_SCOPE")
	// With an identity issuer the instance is registered under it, and a synthetic
	// single sign-on provider is declared as its user_uuid sign-in equivalent, the
	// way production pairs the Bloem and Pastime provider applications.
	identityIssuer := os.Getenv("PASTIME_NATIVE_FIXTURE_IDENTITY_ISSUER")
	f := testkit.New(t)
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": syntheticTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	options := Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Catalog: cat, Secrets: box}
	var sso *fakeIssuer
	if identityIssuer != "" {
		sso = newFakeIssuer(t)
		defer sso.Close()
		options.OIDC = OIDC{Issuer: sso.URL, ClientID: "pastime", ClientSecret: "fixture", Name: "Fixture sign-in", AutoCreate: true}
	}
	s, err := New(ready, nil, "synthetic", options)
	if err != nil {
		t.Fatal(err)
	}
	store := bloem.Store{Pool: f.Pool, Box: box}
	_, token, err := store.Register(t.Context(), f.Owner, bloem.InstanceInput{ID: instance, Scope: scope, URL: "https://lib.example.test", IdentityIssuer: identityIssuer})
	if err != nil {
		t.Fatal(err)
	}
	if sso != nil {
		// The provider runs on loopback HTTP, which the operator command refuses, so
		// the equivalence is recorded directly.
		if _, err = f.Pool.Exec(t.Context(), `INSERT INTO bloem_login_issuers(instance_id,login_issuer,subject_mode) VALUES($1,$2,$3)`, instance, sso.URL, bloem.SubjectUserUUID); err != nil {
			t.Fatal(err)
		}
		serveIdentityFixture(s, f, sso, instance)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finish := make(chan struct{}, 1)
	// Test-only control invokes the same personal mutation boundary as native
	// controls, with fresh profile/account authority in the real transaction.
	var availabilityMu sync.Mutex
	availabilityActors := map[int64]identity.Actor{}
	fixtureViewer := func(ctx context.Context, profile int64) (identity.Actor, error) {
		var err error
		availabilityMu.Lock()
		a, exists := availabilityActors[profile]
		if !exists {
			var account int64
			err = f.Pool.QueryRow(ctx, `SELECT account_id FROM tracking_profiles WHERE id=$1`, profile).Scan(&account)
			var reset string
			if err == nil {
				_, reset, err = f.Accounts.ResetLink(ctx, f.Owner, account)
			}
			var user identity.User
			if err == nil {
				user, err = f.Accounts.Claim(ctx, reset, "", "Synthetic availability viewer", testkit.Password)
			}
			var rawSession string
			if err == nil {
				_, rawSession, err = f.Accounts.Login(ctx, user.Username, testkit.Password)
			}
			if err == nil {
				a, err = f.Accounts.Authenticate(ctx, rawSession, "session")
			}
			if err == nil {
				a, err = f.Accounts.SelectProfile(ctx, a, profile)
			}
			if err == nil {
				availabilityActors[profile] = a
			}
		}
		availabilityMu.Unlock()
		return a, err

	}
	s.Router.Get("/fixture/availability", func(w http.ResponseWriter, r *http.Request) {
		title, err := cat.Resolve(r.Context(), "tmdb", "movie", "123")
		if err != nil {
			http.Error(w, "fixture title", 500)
			return
		}
		var profile int64
		err = f.Pool.QueryRow(r.Context(), `SELECT profile_id FROM bloem_profiles WHERE instance_id=$1 AND source_profile_id=$2 AND active AND NOT excluded`, instance, r.URL.Query().Get("profile")).Scan(&profile)
		if err != nil {
			http.Error(w, "fixture profile", 404)
			return
		}
		a, err := fixtureViewer(r.Context(), profile)
		if err != nil {
			http.Error(w, "fixture actor", 403)
			return
		}
		out, err := store.Availability(r.Context(), a, []int64{title.ID})
		if err != nil {
			http.Error(w, "fixture availability", 500)
			return
		}
		respond(w, 200, map[string]any{"items": out})
	})
	s.Router.Post("/fixture/personal", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Profile, Kind, TMDB string
			Present             bool
			Rating              *int
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "fixture input", 400)
			return
		}
		title, err := cat.Resolve(r.Context(), "tmdb", "movie", in.TMDB)
		if err != nil {
			http.Error(w, "fixture catalog", 500)
			return
		}
		tx, err := f.Pool.Begin(r.Context())
		if err != nil {
			http.Error(w, "fixture transaction", 500)
			return
		}
		defer tx.Rollback(r.Context())
		var profile int64
		err = tx.QueryRow(r.Context(), `SELECT profile_id FROM bloem_profiles WHERE instance_id=$1 AND source_profile_id=$2 AND active AND NOT excluded`, instance, in.Profile).Scan(&profile)
		if err != nil {
			http.Error(w, "fixture profile", 404)
			return
		}
		a, err := identity.ResolveWorkerProfile(r.Context(), tx, profile, "fixture", []string{"library:write"}, true)
		if err == nil {
			_, err = s.library.ApplyPersonalStateTx(r.Context(), tx, a, title.ID, library.PersonalState{Kind: in.Kind, Present: in.Present, Rating: in.Rating}, time.Now().UTC(), "user:fixture")
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			http.Error(w, "fixture personal mutation", 500)
			return
		}
		respond(w, 200, map[string]bool{"accepted": true})
	})
	s.Router.Get("/fixture/state", func(w http.ResponseWriter, r *http.Request) {
		rows, err := f.Pool.Query(r.Context(), `SELECT p.source_profile_id,t.name,p.active,(SELECT count(*) FROM bloem_consumption_receipts c WHERE c.instance_id=p.instance_id AND c.profile_id=p.profile_id),(SELECT coalesce(max(position_seconds),0) FROM playback_progress x WHERE x.profile_id=p.profile_id) FROM bloem_profiles p JOIN tracking_profiles t ON t.id=p.profile_id WHERE p.instance_id=$1 ORDER BY p.source_profile_id`, instance)
		if err != nil {
			http.Error(w, "fixture query failed", 500)
			return
		}
		defer rows.Close()
		profiles := []struct {
			ID, Name        string
			Active          bool
			Plays, Position int
		}{}
		for rows.Next() {
			var p struct {
				ID, Name        string
				Active          bool
				Plays, Position int
			}
			if err := rows.Scan(&p.ID, &p.Name, &p.Active, &p.Plays, &p.Position); err != nil {
				http.Error(w, "fixture scan failed", 500)
				return
			}
			profiles = append(profiles, p)
		}
		var accounts int
		f.Pool.QueryRow(r.Context(), `SELECT count(*) FROM bloem_accounts WHERE instance_id=$1`, instance).Scan(&accounts)
		json.NewEncoder(w).Encode(struct {
			Accounts int
			Profiles any
		}{accounts, profiles})
	})
	s.Router.Get("/fixture/calendar", func(w http.ResponseWriter, r *http.Request) {
		rows, err := f.Pool.Query(r.Context(), `SELECT p.source_profile_id,count(DISTINCT w.snapshot_id),count(e.event_id) FROM bloem_profiles p LEFT JOIN bloem_calendar_windows w ON w.instance_id=p.instance_id AND w.profile_id=p.profile_id LEFT JOIN bloem_calendar_events e ON e.snapshot_id=w.snapshot_id WHERE p.instance_id=$1 GROUP BY p.source_profile_id`, instance)
		if err != nil {
			http.Error(w, "calendar fixture query failed", 500)
			return
		}
		defer rows.Close()
		out := map[string]struct{ Windows, Events int }{}
		for rows.Next() {
			var id string
			var counts struct{ Windows, Events int }
			if err = rows.Scan(&id, &counts.Windows, &counts.Events); err != nil {
				http.Error(w, "calendar fixture scan failed", 500)
				return
			}
			out[id] = counts
		}
		if rows.Err() != nil {
			http.Error(w, "calendar fixture rows failed", 500)
			return
		}
		respond(w, 200, out)
	})

	libraryActor := func(ctx context.Context, sourceProfile string) (identity.Actor, error) {
		var profile int64
		if err := f.Pool.QueryRow(ctx, `SELECT profile_id FROM bloem_profiles WHERE instance_id=$1 AND source_profile_id=$2 AND active AND NOT excluded`, instance, sourceProfile).Scan(&profile); err != nil {
			return identity.Actor{}, err
		}
		return fixtureViewer(ctx, profile)
	}
	serveListAcceptanceFixture(s, f, cat, libraryActor)
	s.Router.Post("/fixture/library-query", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Profile, View string }
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "fixture input", 400)
			return
		}
		a, err := libraryActor(r.Context(), in.Profile)
		if err != nil {
			http.Error(w, "fixture actor", 500)
			return
		}
		out, err := s.bloemStore().RequestLibrary(r.Context(), a, bloem.LibraryQuery{InstanceID: instance, View: in.View, Limit: 20})
		if err != nil {
			http.Error(w, "fixture query", 500)
			return
		}
		respond(w, 200, out)
	})
	s.Router.Get("/fixture/library-result", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		var profile int64
		if err := f.Pool.QueryRow(r.Context(), `SELECT profile_id FROM bloem_library_requests WHERE instance_id=$1 AND id=$2`, instance, id).Scan(&profile); err != nil {
			http.Error(w, "fixture request", 404)
			return
		}
		a, err := fixtureViewer(r.Context(), profile)
		if err != nil {
			http.Error(w, "fixture actor", 500)
			return
		}
		out, err := s.bloemStore().LibraryResult(r.Context(), a, id)
		if err != nil {
			http.Error(w, "fixture result", 500)
			return
		}
		respond(w, 200, out)
	})
	s.Router.Post("/fixture/library-change", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Profile, Event string
			Intent         *bloem.LibraryMutationIntent
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "fixture input", 400)
			return
		}
		a, err := libraryActor(r.Context(), in.Profile)
		if err != nil {
			http.Error(w, "fixture actor", 500)
			return
		}
		intent := bloem.LibraryMutationIntent{InstanceID: instance, EventID: in.Event, Operation: "collection.update", TargetID: "personal:fixture", ExpectedRevision: 1, Name: "Updated from Pastime"}
		if in.Intent != nil {
			intent = *in.Intent
			intent.InstanceID = instance
			intent.EventID = in.Event
		}
		out, err := s.bloemStore().RequestLibraryMutation(r.Context(), a, intent)
		if err != nil {
			http.Error(w, "fixture change", 500)
			return
		}
		respond(w, 200, out)
	})
	s.Router.Get("/fixture/library-change-result", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		var profile int64
		if err := f.Pool.QueryRow(r.Context(), `SELECT profile_id FROM bloem_library_mutations WHERE instance_id=$1 AND id=$2`, instance, id).Scan(&profile); err != nil {
			http.Error(w, "fixture request", 404)
			return
		}
		a, err := fixtureViewer(r.Context(), profile)
		if err != nil {
			http.Error(w, "fixture actor", 500)
			return
		}
		out, err := s.bloemStore().LibraryMutationResult(r.Context(), a, id)
		if err != nil {
			http.Error(w, "fixture result", 500)
			return
		}
		respond(w, 200, out)
	})

	s.Router.Post("/fixture/recommendation-feedback", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Profile, Content string
			Dismissed        bool
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "fixture input", 400)
			return
		}
		tx, err := f.Pool.Begin(r.Context())
		if err != nil {
			http.Error(w, "fixture transaction", 500)
			return
		}
		defer tx.Rollback(r.Context())
		var profile int64
		err = tx.QueryRow(r.Context(), `SELECT profile_id FROM bloem_profiles WHERE instance_id=$1 AND source_profile_id=$2 AND active AND NOT excluded`, instance, in.Profile).Scan(&profile)
		if err != nil {
			http.Error(w, "fixture profile", 404)
			return
		}
		err = tx.Commit(r.Context())
		var a identity.Actor
		if err == nil {
			a, err = fixtureViewer(r.Context(), profile)
		}
		if err == nil {
			err = s.bloemStore().SetRecommendationChoice(r.Context(), a, bloem.RecommendationChoice{InstanceID: instance, ContentID: in.Content, Dismissed: in.Dismissed})
		}
		if err != nil {
			http.Error(w, "fixture feedback", 500)
			return
		}
		respond(w, 200, map[string]bool{"accepted": true})
	})
	s.Router.Get("/fixture/ai-recommendations", func(w http.ResponseWriter, r *http.Request) {
		rows, err := f.Pool.Query(r.Context(), `SELECT p.source_profile_id,jsonb_array_length(a.payload) FROM bloem_ai_recommendations a JOIN bloem_profiles p ON p.instance_id=a.instance_id AND p.profile_id=a.profile_id WHERE a.instance_id=$1 AND a.expires_at>clock_timestamp()`, instance)
		if err != nil {
			http.Error(w, "recommendations fixture query failed", 500)
			return
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var profile string
			var count int
			if err = rows.Scan(&profile, &count); err != nil {
				http.Error(w, "recommendations fixture scan failed", 500)
				return
			}
			out[profile] = count
		}
		if rows.Err() != nil {
			http.Error(w, "recommendations fixture rows failed", 500)
			return
		}
		respond(w, 200, out)
	})
	s.Router.Post("/fixture/finish", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		select {
		case finish <- struct{}{}:
		default:
		}
	})
	httpServer := &http.Server{Handler: s.Router, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	defer httpServer.Close()
	raw, _ := json.Marshal(map[string]any{"url": "http://" + listener.Addr().String(), "provision_credential": token, "allow_loopback_http": true})
	// These are generated fixture credentials, never copied from an environment.
	if err = os.WriteFile(settings, raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finish:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(120 * time.Second):
		t.Fatal("acceptance controller did not finish")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = httpServer.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
