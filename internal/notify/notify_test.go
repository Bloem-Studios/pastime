package notify_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/notify"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestReleaseNoticesAreSignedDedupedAndMutable(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("signing-secret"))
		mac.Write(body)
		if r.Header.Get("X-Pastime-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			http.Error(w, "bad signature", 401)
			return
		}
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	defer srv.Close()
	box, err := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	if err != nil {
		t.Fatal(err)
	}
	store := notify.Store{Pool: f.Pool, Box: box, AllowPrivate: true}
	target, err := store.SaveTarget(ctx, f.Owner, 0, notify.TargetInput{Kind: "webhook", Name: "Home", Config: notify.Config{URL: srv.URL, Secret: "signing-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target.Description, "signing") {
		t.Fatal("secret leaked into the description")
	}
	var sealedConfig []byte
	_ = f.Pool.QueryRow(ctx, "SELECT sealed_config FROM notification_targets WHERE id=$1", target.ID).Scan(&sealedConfig)
	if strings.Contains(string(sealedConfig), "signing-secret") || strings.Contains(string(sealedConfig), srv.URL) {
		t.Fatal("destination stored in clear")
	}
	if tested, err := store.Test(ctx, f.Owner, target.ID); err != nil || tested.LastStatus != "Delivered" {
		t.Fatalf("test notice: %+v %v", tested, err)
	}
	// A film released today that the owner plans to watch.
	var movieID int64
	today := time.Now().UTC().Format("2006-01-02")
	if err = f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date) VALUES('movie','tmdb','n1','Premiere Night','minutes',100,$1::date) RETURNING id`, today).Scan(&movieID); err != nil {
		t.Fatal(err)
	}
	lib := library.Store{Pool: f.Pool}
	entry, err := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: movieID})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"date": today})
	for range 2 {
		if err = store.Handler(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if len(bodies) != 2 || !strings.Contains(bodies[1], "Premiere Night") {
		t.Fatalf("expected the test notice and one release notice, got %d: %v", len(bodies), bodies)
	}
	mu.Unlock()
	// A muted title is not announced.
	var secondID int64
	_ = f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date) VALUES('movie','tmdb','n2','Muted Premiere','minutes',100,$1::date) RETURNING id`, today).Scan(&secondID)
	muted, _ := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: secondID})
	if err = store.SetExcluded(ctx, f.Owner, muted.ID, true); err != nil {
		t.Fatal(err)
	}
	_, _ = f.Pool.Exec(ctx, "DELETE FROM notification_log")
	_ = store.Handler(ctx, payload)
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 || strings.Contains(bodies[2], "Muted Premiere") {
		t.Fatalf("muted title announced: %v", bodies)
	}
	_ = entry
	// Without private-network permission, a loopback destination is refused.
	guarded := notify.Store{Pool: f.Pool, Box: box}
	if _, err = guarded.Test(ctx, f.Owner, target.ID); err == nil {
		t.Fatal("loopback destination reached")
	}
}

func TestFailedDestinationRetriesWithoutRepeatingSuccessfulDestination(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	var mu sync.Mutex
	good, bad := 0, 0
	recovered := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/good" {
			good++
			return
		}
		bad++
		if !recovered {
			http.Error(w, "synthetic outage", 503)
		}
	}))
	defer srv.Close()
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	s := notify.Store{Pool: f.Pool, Box: box, AllowPrivate: true}
	for _, path := range []string{"/good", "/bad"} {
		if _, err := s.SaveTarget(ctx, f.Owner, 0, notify.TargetInput{Kind: "webhook", Name: path, Config: notify.Config{URL: srv.URL + path}}); err != nil {
			t.Fatal(err)
		}
	}
	today := time.Now().UTC().Format("2006-01-02")
	var id int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(kind,provider,provider_id,title,unit,release_date) VALUES('movie','manual','retry','Retry','minutes',$1::date) RETURNING id`, today).Scan(&id); err != nil {
		t.Fatal(err)
	}
	lib := library.Store{Pool: f.Pool}
	if _, err := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: id}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"date": today})
	if err := s.Handler(ctx, payload); err == nil {
		t.Fatal("destination failure hidden from job retry")
	}
	mu.Lock()
	recovered = true
	mu.Unlock()
	if err := s.Handler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if good != 1 || bad != 2 {
		t.Fatal("per-destination delivery lost or repeated", good, bad)
	}
}

func TestLeasedDeliveryKeepsJobRetryable(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	s := notify.Store{Pool: f.Pool, Box: box}
	target, err := s.SaveTarget(ctx, f.Owner, 0, notify.TargetInput{Kind: "webhook", Name: "Leased", Config: notify.Config{URL: "https://example.invalid/notice"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `INSERT INTO notification_deliveries(user_id,target_id,notice,leased_until) VALUES($1,$2,'{}',clock_timestamp()+interval '2 minutes')`, f.Owner.User.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"date": time.Now().UTC().Format("2006-01-02")})
	if err = s.Handler(ctx, payload); err == nil {
		t.Fatal("leased pending delivery acknowledged as complete")
	}
}
