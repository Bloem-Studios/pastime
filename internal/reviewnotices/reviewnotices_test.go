package reviewnotices_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/reviewnotices"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestDue(t *testing.T) {
	for at, want := range map[time.Time]bool{
		time.Date(2026, 12, 27, 8, 59, 0, 0, time.UTC):  false,
		time.Date(2026, 12, 27, 9, 0, 0, 0, time.UTC):   true,
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC): true,
		time.Date(2027, 1, 2, 12, 0, 0, 0, time.UTC):    false,
		time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC):   false,
	} {
		if year, due := reviewnotices.Due(at); due != want || year != at.Year() {
			t.Errorf("Due(%v) = %d %v, want %v", at, year, due, want)
		}
	}
}

func TestPostMapsTheCustomerSystemsAnswers(t *testing.T) {
	answer := func(code int, status string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			if status != "" {
				_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
			}
		}))
	}
	x := reviewnotices.Summary{AccountID: 1, Subject: "s", Minutes: 60, Titles: 1}
	for _, tc := range []struct {
		code      int
		answer    string
		want      string
		temporary bool
	}{
		{202, "queued", "sent", false},
		{200, "already_queued", "sent", false},
		{200, "opted_out", "opted_out", false},
		{200, "would_queue", "would_send", false},
		{404, "", "unknown", false},
		{400, "", "refused", false},
		{503, "", "", true},
		{429, "", "", true},
		{200, "surprise", "", true},
	} {
		srv := answer(tc.code, tc.answer)
		got, err := reviewnotices.Sender{URL: srv.URL, Token: "t", Client: srv.Client()}.Post(t.Context(), 2026, x, false)
		srv.Close()
		if got != tc.want || (tc.temporary && err == nil) {
			t.Errorf("%d %q: status %q err %v, want %q", tc.code, tc.answer, got, err, tc.want)
		}
	}
	srv := answer(401, "")
	defer srv.Close()
	if _, err := (reviewnotices.Sender{URL: srv.URL, Token: "t", Client: srv.Client()}).Post(t.Context(), 2026, x, false); !errors.Is(err, reviewnotices.ErrUnauthorized) {
		t.Fatalf("401 = %v", err)
	}
}

// fakeGarden records each notice and answers with the queued status, or the
// code set in fail.
type fakeGarden struct {
	mu      sync.Mutex
	notices []map[string]any
	auth    []string
	fail    int
}

func (g *fakeGarden) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	g.notices = append(g.notices, body)
	g.auth = append(g.auth, r.Header.Get("Authorization"))
	if g.fail != 0 {
		w.WriteHeader(g.fail)
		return
	}
	status, code := "queued", http.StatusAccepted
	if body["dry_run"] == true {
		status, code = "would_queue", http.StatusOK
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func TestRunSendsEachCustomersYearOnceAndRetriesTemporaryFailures(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	subject := "0b6a1f0e-6b44-4c4a-9d55-2f6a5d8e9c11"
	exec(`INSERT INTO service_access_policy(id,issuer,enabled,generated_at,fresh_until) VALUES(1,'https://auth.example.test/',true,clock_timestamp(),clock_timestamp()+interval '100 seconds')`)
	exec(`INSERT INTO service_access_grants(subject,expires_at) VALUES($1,clock_timestamp()+interval '1 hour')`, subject)
	exec(`INSERT INTO oidc_identities(issuer,subject,user_id) VALUES('https://auth.example.test/',$1,$2)`, subject, f.Owner.User.ID)
	var film, entry int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb','900','Film','minutes',100,100) RETURNING id`).Scan(&film); err != nil {
		t.Fatal(err)
	}
	m, err := library.Store{Pool: f.Pool}.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: film})
	if err != nil {
		t.Fatal(err)
	}
	entry = m.ID
	for _, at := range []time.Time{time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 20, 0, 0, 0, time.UTC), time.Date(2025, 7, 1, 20, 0, 0, 0, time.UTC)} {
		exec(`INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,source) VALUES($1,$2,$3,1,$4,'manual')`, f.Owner.TrackingID(), entry, film, at)
	}
	garden := &fakeGarden{}
	srv := httptest.NewServer(garden)
	defer srv.Close()
	sender := reviewnotices.Sender{Pool: f.Pool, URL: srv.URL, Token: "bgtrk_test", Client: srv.Client()}

	summaries, err := sender.Summaries(ctx, 2026)
	if err != nil || len(summaries) != 1 || summaries[0].Subject != subject || summaries[0].Minutes != 200 || summaries[0].Titles != 1 {
		t.Fatalf("summaries %+v %v", summaries, err)
	}
	if res, err := sender.Run(ctx, time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC), 10); err != nil || len(res) != 0 || len(garden.notices) != 0 {
		t.Fatalf("before 27 December: %v %v %d", res, err, len(garden.notices))
	}
	checked, err := sender.Check(ctx, 2026)
	if err != nil || checked["would_send"] != 1 || garden.notices[0]["dry_run"] != true {
		t.Fatalf("check %v %v %+v", checked, err, garden.notices)
	}
	var rows int
	if err := f.Pool.QueryRow(ctx, `SELECT count(*) FROM review_notices`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("a check recorded %d rows", rows)
	}

	garden.fail = http.StatusServiceUnavailable
	due := time.Date(2026, 12, 27, 10, 0, 0, 0, time.UTC)
	if res, err := sender.Run(ctx, due, 10); err != nil || res["pending"] != 1 {
		t.Fatalf("temporary failure %v %v", res, err)
	}
	var status string
	var attempts int
	var next *time.Time
	if err := f.Pool.QueryRow(ctx, `SELECT status,attempts,next_attempt_at FROM review_notices WHERE account_id=$1 AND year=2026`, f.Owner.User.ID).Scan(&status, &attempts, &next); err != nil ||
		status != "pending" || attempts != 1 || next == nil {
		t.Fatalf("after a failure: %s %d %v %v", status, attempts, next, err)
	}
	exec(`UPDATE review_notices SET next_attempt_at=NULL`)
	garden.fail = 0
	if res, err := sender.Run(ctx, due, 10); err != nil || res["sent"] != 1 {
		t.Fatalf("send %v %v", res, err)
	}
	if res, err := sender.Run(ctx, due.Add(time.Hour), 10); err != nil || len(res) != 0 {
		t.Fatalf("second pass sent again: %v %v", res, err)
	}
	last := garden.notices[len(garden.notices)-1]
	if len(garden.notices) != 3 || last["kind"] != "year_in_review" || last["subject"] != subject || last["year"] != 2026.0 ||
		last["minutes"] != 200.0 || last["titles"] != 1.0 || last["dry_run"] != nil || garden.auth[2] != "Bearer bgtrk_test" {
		t.Fatalf("notices %+v auth %v", garden.notices, garden.auth)
	}

	exec(`DELETE FROM review_notices`)
	garden.fail = http.StatusUnauthorized
	if _, err := sender.Run(ctx, due, 10); !errors.Is(err, reviewnotices.ErrUnauthorized) {
		t.Fatalf("a refused token = %v", err)
	}
	if err := f.Pool.QueryRow(ctx, `SELECT status,attempts FROM review_notices WHERE account_id=$1`, f.Owner.User.ID).Scan(&status, &attempts); err != nil || status != "pending" || attempts != 0 {
		t.Fatalf("a refused token changed the row: %s %d %v", status, attempts, err)
	}
}
