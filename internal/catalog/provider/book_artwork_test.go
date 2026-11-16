package provider

import (
	"errors"
	"net/http"
	"testing"
)

func TestOpenLibraryExactISBN(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		found      bool
	}{
		{"exact", `{"numFound":1,"docs":[{"key":"/works/OL1W","title":"Synthetic Book","cover_i":77,"isbn":["9780000000002","9780000000001"]}]}`, true},
		{"different ISBN", `{"numFound":1,"docs":[{"key":"/works/OL1W","title":"Synthetic Book","isbn":["9780000000002"]}]}`, false},
		{"ambiguous", `{"numFound":2,"docs":[{"key":"/works/OL1W","title":"One","isbn":["9780000000001"]},{"key":"/works/OL2W","title":"Two","isbn":["9780000000001"]}]}`, false},
		{"truncated", `{"numFound":21,"docs":[{"key":"/works/OL1W","title":"One","isbn":["9780000000001"]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(nil)
			c.HTTP.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
				if req.URL.Query().Get("isbn") != "9780000000001" || req.URL.Query().Get("q") != "" || req.URL.Query().Get("limit") != "20" {
					t.Fatalf("not bounded ISBN query: %s", req.URL)
				}
				return routes{"openlibrary.org/search.json": tc.body}.RoundTrip(req)
			})
			rec, err := (OpenLibrary{Client: c}).FetchISBN(t.Context(), "book", "978-0000000001")
			if !tc.found {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("unsafe match: %+v %v", rec, err)
				}
				return
			}
			if err != nil || rec.ExternalIDs["isbn"] != "9780000000001" || rec.ImageURL != "https://covers.openlibrary.org/b/id/77-L.jpg" {
				t.Fatalf("exact metadata: %+v %v", rec, err)
			}
		})
	}
}

func TestAudnexusExactASIN(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		found      bool
	}{
		{"exact", `{"asin":"B000000001","title":"Synthetic Audio","image":"https://images.example/cover.jpg","authors":[{"name":"A. Writer"}]}`, true},
		{"different ASIN", `{"asin":"B000000002","title":"Synthetic Audio","image":"https://images.example/cover.jpg"}`, false},
		{"missing ASIN", `{"title":"Synthetic Audio","image":"https://images.example/cover.jpg"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(routes{"api.audnex.us/books/B000000001": tc.body})
			rec, err := (Audnexus{Client: c}).Fetch(t.Context(), "audiobook", "B000000001")
			if !tc.found {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("unsafe match: %+v %v", rec, err)
				}
				return
			}
			if err != nil || rec.ProviderID != "B000000001" || rec.ExternalIDs["asin"] != "B000000001" || rec.ImageURL != "https://images.example/cover.jpg" {
				t.Fatalf("exact metadata: %+v %v", rec, err)
			}
		})
	}
	p := Audnexus{Client: fixture(nil)}
	for _, id := range []string{"../escape", "B123", "B00000000?"} {
		if _, err := p.Fetch(t.Context(), "audiobook", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid ASIN accepted: %q %v", id, err)
		}
	}
	if _, err := p.Search(t.Context(), "audiobook", "Synthetic Audio"); !errors.Is(err, ErrNotFound) {
		t.Fatal("title-only search accepted", err)
	}
}
