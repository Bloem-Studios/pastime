package catalog_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
)

type calendarTMDB struct{ fakeTV }

func (*calendarTMDB) Name() string { return "tmdb" }
func (f *calendarTMDB) Fetch(ctx context.Context, kind, id string) (provider.Record, error) {
	rec, err := f.fakeTV.Fetch(ctx, kind, id)
	rec.Provider = "tmdb"
	return rec, err
}

func TestCalendarMetadataJobsAreSharedBoundedAndDoNotTrack(t *testing.T) {
	f, s, _ := setup(t)
	p := &calendarTMDB{}
	s.Providers = map[string]provider.Provider{"tmdb": p}
	now := time.Now()
	ids := map[string]string{"tmdb": "42"}
	for i := 0; i < 2; i++ {
		added, err := s.EnqueueCalendarTitles(t.Context(), []catalog.CalendarTitleRequest{{Kind: "movie", IDs: ids}}, now)
		if err != nil || added != 1-i {
			t.Fatal("dedupe", added, err)
		}
	}
	var payload json.RawMessage
	if err := f.Pool.QueryRow(t.Context(), `SELECT payload FROM jobs WHERE kind=$1`, catalog.CalendarTitleJob).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.CalendarTitleHandler(t.Context(), payload); err != nil {
			t.Fatal(err)
		}
	}
	var titles, entries int
	if err := f.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM catalog_items),(SELECT count(*) FROM library_entries)`).Scan(&titles, &entries); err != nil {
		t.Fatal(err)
	}
	if titles != 1 || entries != 0 || p.fetches.Load() != 1 {
		t.Fatal("metadata replay enrolled profile or fetched repeatedly", titles, entries, p.fetches.Load())
	}
	requests := []catalog.CalendarTitleRequest{}
	for i := 0; i < 70; i++ {
		requests = append(requests, catalog.CalendarTitleRequest{Kind: "movie", IDs: map[string]string{"tmdb": strconv.Itoa(1000 + i)}})
	}
	for _, want := range []int{50, 20, 0} {
		n, err := s.EnqueueCalendarTitles(t.Context(), requests, now)
		if err != nil || n != want {
			t.Fatal("bounded batch failed/starved later identities", n, want, err)
		}
	}
	for _, bad := range []json.RawMessage{[]byte(`{"kind":"episode","provider":"tmdb","scheme":"tmdb","value":"42"}`), []byte(`{"kind":"movie","provider":"http","scheme":"tmdb","value":"42"}`)} {
		if err := s.CalendarTitleHandler(t.Context(), bad); err == nil {
			t.Fatal("invalid job accepted")
		}
	}
}

type calendarBookTransport struct{ calls atomic.Int32 }

func (c *calendarBookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	body := `{"numFound":1,"docs":[{"key":"/works/OL1W","title":"Synthetic Book","cover_i":77,"isbn":["9780000000001"]}]}`
	if req.URL.Host == "api.audnex.us" {
		body = `{"asin":"B000000001","title":"Synthetic Audio","image":"https://images.example/audio.jpg"}`
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func TestCalendarBooksUseExactIdentitiesWithoutTracking(t *testing.T) {
	f, s, _ := setup(t)
	c := provider.NewClient("test")
	transport := &calendarBookTransport{}
	c.HTTP.Transport = transport
	s.Providers = map[string]provider.Provider{"openlibrary": provider.OpenLibrary{Client: c}, "audnexus": provider.Audnexus{Client: c}}
	requests := []catalog.CalendarTitleRequest{
		{Kind: "ebook", IDs: map[string]string{"isbn": "978-0000000001"}},
		{Kind: "audiobook", IDs: map[string]string{"asin": "b000000001"}},
		{Kind: "ebook", IDs: map[string]string{"isbn": "../invalid"}},
		{Kind: "audiobook", IDs: map[string]string{"title": "Synthetic Audio"}},
	}
	n, err := s.EnqueueCalendarTitles(t.Context(), requests, time.Now())
	if err != nil || n != 2 {
		t.Fatal("book jobs", n, err)
	}
	rows, err := f.Pool.Query(t.Context(), `SELECT payload FROM jobs WHERE kind=$1`, catalog.CalendarTitleJob)
	if err != nil {
		t.Fatal(err)
	}
	payloads := []json.RawMessage{}
	for rows.Next() {
		var p json.RawMessage
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	for _, p := range payloads {
		for i := 0; i < 2; i++ {
			if err := s.CalendarTitleHandler(t.Context(), p); err != nil {
				t.Fatal(string(p), err)
			}
		}
	}
	var titles, entries, exact int
	if err := f.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM catalog_items),(SELECT count(*) FROM library_entries),(SELECT count(*) FROM catalog_items WHERE (kind='book' AND external_ids->>'isbn'='9780000000001') OR (kind='audiobook' AND external_ids->>'asin'='B000000001'))`).Scan(&titles, &entries, &exact); err != nil {
		t.Fatal(err)
	}
	if titles != 2 || entries != 0 || exact != 2 || transport.calls.Load() != 2 {
		t.Fatal("unsafe/repeated metadata lookup", titles, entries, exact, transport.calls.Load())
	}
	for _, p := range []string{`{"kind":"book","provider":"audnexus","scheme":"isbn","value":"9780000000001"}`, `{"kind":"audiobook","provider":"audnexus","scheme":"asin","value":"../invalid"}`, `{"kind":"ebook","provider":"openlibrary","scheme":"isbn","value":"9780000000001"}`} {
		if err := s.CalendarTitleHandler(t.Context(), json.RawMessage(p)); err == nil {
			t.Fatal("invalid book job accepted", p)
		}
	}
}

func TestCalendarISBNDoesNotOverwriteAnotherEdition(t *testing.T) {
	f, s, _ := setup(t)
	c := provider.NewClient("test")
	c.HTTP.Transport = &calendarBookTransport{}
	s.Providers = map[string]provider.Provider{"openlibrary": provider.OpenLibrary{Client: c}}
	for _, tc := range []struct{ isbn, image, want string }{
		{"9780000000002", "https://images.example/existing.jpg", "https://images.example/existing.jpg"},
		{"9780000000002", "", ""},
		{"9780000000001", "", "https://covers.openlibrary.org/b/id/77-L.jpg"},
	} {
		_, err := f.Pool.Exec(t.Context(), `INSERT INTO catalog_items(kind,provider,provider_id,title,overview,image_url,unit,external_ids) VALUES('book','openlibrary','OL1W','Keep title','Keep overview',$1,'pages',jsonb_build_object('isbn',$2::text)) ON CONFLICT(provider,kind,provider_id) DO UPDATE SET external_ids=EXCLUDED.external_ids,image_url=EXCLUDED.image_url`, tc.image, tc.isbn)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CalendarTitleHandler(t.Context(), json.RawMessage(`{"kind":"book","provider":"openlibrary","scheme":"isbn","value":"9780000000001"}`)); err != nil {
			t.Fatal(err)
		}
		var title, overview, isbn, image string
		if err := f.Pool.QueryRow(t.Context(), `SELECT title,overview,external_ids->>'isbn',image_url FROM catalog_items WHERE provider='openlibrary' AND provider_id='OL1W'`).Scan(&title, &overview, &isbn, &image); err != nil {
			t.Fatal(err)
		}
		if title != "Keep title" || overview != "Keep overview" || isbn != tc.isbn || image != tc.want {
			t.Fatal("edition metadata replaced", title, overview, isbn, image, tc)
		}
	}
}
