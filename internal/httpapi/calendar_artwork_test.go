package httpapi

import (
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
)

func TestCalendarPostersRequireUnambiguousRootIdentity(t *testing.T) {
	h := application(t)
	add := func(kind, provider, providerID, external string) int64 {
		t.Helper()
		var id int64
		if err := h.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,image_url,external_ids) VALUES(0,$1,$2,$3,'Synthetic title','episodes','https://image.tmdb.org/t/p/w500/synthetic.jpg',$4::jsonb) RETURNING id`, kind, provider, providerID, external).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Pool.Exec(t.Context(), `UPDATE catalog_items SET root_id=id WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	series := add("series", "tmdb", "100", `{"tvdb":200}`)
	add("movie", "tmdb", "100", `{}`) // Same numeric ID, different media namespace.
	for _, tc := range []struct {
		name  string
		event bloem.CalendarEvent
		want  int64
	}{
		{"episode uses series ids", bloem.CalendarEvent{Kind: "episode", ExternalIDs: map[string]string{"tmdb": "999"}, SeriesExternalIDs: map[string]string{"tvdb": "200"}}, series},
		{"contradictory series ids", bloem.CalendarEvent{Kind: "episode", SeriesExternalIDs: map[string]string{"tmdb": "101", "tvdb": "200"}}, 0},
		{"same title no identity", bloem.CalendarEvent{Kind: "movie", Title: "Synthetic title"}, 0},
		{"episode id is not series id", bloem.CalendarEvent{Kind: "episode", ExternalIDs: map[string]string{"tmdb": "100"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.event.InstanceID = "source"
			tc.event.EventID = "release"
			out := []calendarRelease{sourceRelease(tc.event)}
			if err := h.server.enrichCalendarTitles(t.Context(), h.Owner, out, []bloem.CalendarEvent{tc.event}); err != nil {
				t.Fatal(err)
			}
			if tc.want == 0 {
				if out[0].ParentCatalog != nil || out[0].ImageURL != "" {
					t.Fatal("invented poster/title", out[0])
				}
			} else if out[0].ParentCatalog == nil || *out[0].ParentCatalog != strconv.FormatInt(tc.want, 10) || out[0].ImageURL == "" {
				t.Fatal("missing verified root", out[0])
			}
			if out[0].CatalogID != nil || out[0].EntryID != nil {
				t.Fatal("source release became native/personal", out[0])
			}
		})
	}
	add("series", "tvdb", "200", `{}`)
	event := bloem.CalendarEvent{InstanceID: "source", EventID: "ambiguous", Kind: "episode", SeriesExternalIDs: map[string]string{"tvdb": "200"}}
	out := []calendarRelease{sourceRelease(event)}
	if err := h.server.enrichCalendarTitles(t.Context(), h.Owner, out, []bloem.CalendarEvent{event}); err != nil {
		t.Fatal(err)
	}
	if out[0].ParentCatalog != nil {
		t.Fatal("ambiguous root selected", out[0])
	}
}

func TestConnectedBooksUseExactCachedArtworkIdentities(t *testing.T) {
	h := application(t)
	for _, tc := range []struct{ source, kind, scheme, value, sourceValue string }{
		{"ebook", "book", "isbn", "9780000000002", "978-0000000002"},
		{"audiobook", "audiobook", "asin", "B000000001", "b000000001"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			var id int64
			raw := `{"` + tc.scheme + `":"` + tc.value + `"}`
			if err := h.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,image_url,external_ids) VALUES(0,$1,'synthetic',$2,'Synthetic book','pages','https://images.example.test/book.jpg',$3::jsonb) RETURNING id`, tc.kind, tc.source, raw).Scan(&id); err != nil {
				t.Fatal(err)
			}
			event := bloem.CalendarEvent{InstanceID: "source", EventID: tc.source, Kind: tc.source, ExternalIDs: map[string]string{tc.scheme: tc.sourceValue}}
			out := []calendarRelease{sourceRelease(event)}
			if err := h.server.enrichCalendarTitles(t.Context(), h.Owner, out, []bloem.CalendarEvent{event}); err != nil {
				t.Fatal(err)
			}
			if out[0].ImageURL != "https://images.example.test/book.jpg" || out[0].ParentCatalog == nil || *out[0].ParentCatalog != strconv.FormatInt(id, 10) {
				t.Fatal("missing exact book artwork", out[0])
			}
			if out[0].EntryID != nil {
				t.Fatal("artwork tracked a book")
			}
		})
	}
}
