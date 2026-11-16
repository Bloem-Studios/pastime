package bloem

import (
	"testing"

	"github.com/Bloem-Studios/pastime/internal/library"
)

func TestListSnapshotInvalidIdentityIsUnmatched(t *testing.T) {
	for _, e := range []library.ListExportEntry{
		{ID: "entry:1", Title: "Synthetic", MediaType: "movie", Provider: "tmdb", ProviderID: "oops"},
		{ID: "entry:1", Title: "Synthetic", MediaType: "book", ExternalIDs: map[string]string{"isbn": "9780140328722"}},
		{ID: "entry:1", Title: "Synthetic", MediaType: "series", ExternalIDs: map[string]string{"imdb": "garbage"}},
	} {
		got := exportListEntry(e)
		if got.Kind != "unsupported" || got.UnsupportedReason != "invalid_identity" || len(got.ExternalIDs) != 0 {
			t.Fatal(got)
		}
	}
}

func TestListSnapshotChosenEditionIdentity(t *testing.T) {
	got := exportListEntry(library.ListExportEntry{ID: "entry:1", Title: "Synthetic", MediaType: "book", EditionISBN: "9780140328721", ExternalIDs: map[string]string{"isbn": "9780142410370"}})
	if got.Kind != "ebook" || got.ExternalIDs["isbn"] != "9780140328721" || len(got.ExternalIDs) != 1 {
		t.Fatal(got)
	}
}

func TestListSnapshotCanonicalDigest(t *testing.T) {
	got, err := sourceListDigest([]ListSnapshotEntry{{Kind: "movie", Title: "A", Year: 2020, ExternalIDs: map[string]string{"imdb": "tt0000042", "tmdb": "42"}}})
	if err != nil || got != "7a377cc39fe1e489d55aaed7ca3beb74a08fb95b137e0a822699742a8a5e33c0" {
		t.Fatal(got, err)
	}
}
