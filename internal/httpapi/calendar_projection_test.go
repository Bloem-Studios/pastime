package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/library"
)

func TestCalendarMergeUsesReliableIdentityNeverTitle(t *testing.T) {
	native := []library.Release{{CatalogID: 1, ParentCatalog: 1, Kind: "movie", Title: "Same", EntryTitle: "Same", Date: "2026-10-12"}}
	ids := map[int64]calendarIdentity{1: {IDs: map[string]string{"tmdb": "10"}}}
	source := []bloem.CalendarEvent{{InstanceID: "a", EventID: "shared", ContentID: "item", Kind: "movie", Title: "Same", Date: "2026-10-12", ExternalIDs: map[string]string{"tmdb": "10"}}, {InstanceID: "a", EventID: "other", ContentID: "other", Kind: "movie", Title: "Same", Date: "2026-10-12", ExternalIDs: map[string]string{"tmdb": "11"}}}
	out := mergeCalendar(native, source, ids, "all")
	if len(out) != 2 || out[0].SourceEventID != "shared" || out[1].CatalogID != nil {
		t.Fatalf("identity or source-only merge: %+v", out)
	}
	source[0].ReleaseType = "digital"
	if got := mergeCalendar(native, source, ids, "all"); len(got) != 3 {
		t.Fatalf("merged different release type: %+v", got)
	}
}
func TestCalendarMergeDoesNotCollapseDifferentEpisodes(t *testing.T) {
	s1, e1, e2 := 1, 1, 2
	native := []library.Release{{CatalogID: 2, ParentCatalog: 1, Kind: "episode", Title: "Episode", EntryTitle: "Series", Date: "2026-10-12", SeasonNumber: &s1, EpisodeNumber: &e1}}
	series := map[string]string{"tvdb": "9"}
	ids := map[int64]calendarIdentity{2: {SeriesIDs: series}}
	season, episode := int32(s1), int32(e2)
	source := []bloem.CalendarEvent{{InstanceID: "source", EventID: "event", Kind: "episode", Title: "Series", Date: "2026-10-12", SeriesExternalIDs: series, SeasonNumber: &season, EpisodeNumber: &episode}}
	if out := mergeCalendar(native, source, ids, "all"); len(out) != 2 {
		t.Fatal("different episodes collapsed")
	}
	episode = int32(e1)
	if out := mergeCalendar(native, source, ids, "all"); len(out) != 1 {
		t.Fatal("same episode not merged")
	}
}
func TestCalendarICSPreservesInstantsDatesAndStableSourceUIDs(t *testing.T) {
	air := time.Date(2026, 3, 29, 0, 30, 0, 0, time.FixedZone("CET", 3600))
	source := []bloem.CalendarEvent{{InstanceID: "source", EventID: "timed", ContentID: "timed", Kind: "episode", Title: "Épisode", Date: "2026-03-29", AirAt: &air, AirTimezone: "Europe/Paris"}, {InstanceID: "source", EventID: "date", ContentID: "date", Kind: "movie", Title: "Film", Date: "2026-03-29"}}
	events := mergeCalendar(nil, source, nil, "all")
	s := calendarICS(events, time.Now())
	for _, want := range []string{"DTSTART:20260328T233000Z", "DTSTART;VALUE=DATE:20260329", "DTEND;VALUE=DATE:20260330"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, "release-0@pastime") {
		t.Fatal("fabricated catalog UID")
	}
}

func TestCalendarMergeRejectsContradictoryEpisodeIDs(t *testing.T) {
	season, episode := 1, 1
	s, e := int32(1), int32(1)
	native := []library.Release{{CatalogID: 2, Kind: "episode", Date: "2026-10-12", SeasonNumber: &season, EpisodeNumber: &episode}}
	ids := map[int64]calendarIdentity{2: {IDs: map[string]string{"tvdb": "100"}, SeriesIDs: map[string]string{"tvdb": "9"}}}
	source := []bloem.CalendarEvent{{Kind: "episode", Date: "2026-10-12", SeasonNumber: &s, EpisodeNumber: &e, ExternalIDs: map[string]string{"tvdb": "101"}, SeriesExternalIDs: map[string]string{"tvdb": "9"}}}
	if got := mergeCalendar(native, source, ids, "all"); len(got) != 2 {
		t.Fatal("series match overrode contradictory episode identity")
	}
}

func TestCalendarMergeDeduplicatesReliableSourceEvents(t *testing.T) {
	source := []bloem.CalendarEvent{
		{InstanceID: "a", EventID: "event-a", Kind: "movie", Title: "Movie", Date: "2026-10-12", ExternalIDs: map[string]string{"tmdb": "10"}},
		{InstanceID: "b", EventID: "event-b", Kind: "movie", Title: "Different title", Date: "2026-10-12", ExternalIDs: map[string]string{"tmdb": "10"}, Favourite: true},
		{InstanceID: "b", EventID: "event-c", Kind: "movie", Title: "Movie", Date: "2026-10-12", ExternalIDs: map[string]string{"tmdb": "11"}},
	}
	out := mergeCalendar(nil, source, nil, "all")
	if len(out) != 2 {
		t.Fatalf("reliable source duplicate retained: %+v", out)
	}
	if out[0].SourceEventID != "event-a" || !out[0].Favourite {
		t.Fatal("source identity/filter flags lost", out[0])
	}
}

func TestCalendarICSPreservesFallDSTAndUnicode(t *testing.T) {
	air := time.Date(2026, 10, 25, 1, 30, 0, 0, time.FixedZone("CET", 3600))
	source := []bloem.CalendarEvent{{InstanceID: "source", EventID: "fall", Kind: "movie", Title: strings.Repeat("É", 80), Date: "2026-10-25", AirAt: &air, AirTimezone: "Europe/Paris"}}
	doc := calendarICS(mergeCalendar(nil, source, nil, "all"), time.Now())
	if !strings.Contains(doc, "DTSTART:20261025T003000Z") {
		t.Fatal(doc)
	}
	for _, line := range strings.Split(strings.TrimSuffix(doc, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Fatalf("long line: %d", len(line))
		}
	}
	if !strings.Contains(strings.ReplaceAll(doc, "\r\n ", ""), strings.Repeat("É", 80)) {
		t.Fatal("Unicode lost")
	}
}
