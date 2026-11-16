package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
)

func TestICSEscapesAndFolds(t *testing.T) {
	s, e := 1, 2
	doc := ics([]library.Release{
		{Date: "2026-10-01", CatalogID: 9, Kind: "episode", Title: "Commas, semicolons; and \\ slashes", SeasonNumber: &s, EpisodeNumber: &e, EntryTitle: strings.Repeat("Ω", 60), ParentCatalog: 3},
		{Date: "bad", CatalogID: 10},
	}, time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC))
	unfolded := strings.ReplaceAll(doc, "\r\n ", "")
	if !strings.Contains(unfolded, `Commas\, semicolons\; and \\ slashes`) || !strings.Contains(doc, "DTSTART;VALUE=DATE:20261001") || strings.Count(doc, "BEGIN:VEVENT") != 1 {
		t.Fatalf("unexpected document:\n%s", doc)
	}
	for _, line := range strings.Split(strings.TrimSuffix(doc, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Fatalf("unfolded line of %d octets", len(line))
		}
	}
	if strings.Contains(doc, "\n") && !strings.Contains(doc, "\r\n") {
		t.Fatal("lines must end with CRLF")
	}
}

func TestICSGivesEachRegionalReleaseItsOwnEvent(t *testing.T) {
	doc := ics([]library.Release{
		{Date: "2030-11-01", CatalogID: 7, ParentCatalog: 7, Kind: "movie", EntryTitle: "Regional", ReleaseType: "theatrical"},
		{Date: "2030-12-01", CatalogID: 7, ParentCatalog: 7, Kind: "movie", EntryTitle: "Regional", ReleaseType: "digital"},
	}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(doc, "UID:release-7-theatrical@pastime") || !strings.Contains(doc, "UID:release-7-digital@pastime") || !strings.Contains(doc, "SUMMARY:Regional (digital)") {
		t.Fatal(doc)
	}
}
