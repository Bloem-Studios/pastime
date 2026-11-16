package library_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestCalendarAndHomeKeepRootBackdrop(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := t.Context()
	now := time.Now().UTC()
	date := now.AddDate(0, 0, 5)
	for _, kind := range []string{"movie", "series"} {
		var root int64
		if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,image_url,backdrop_url,release_date) VALUES('manual',$1,$1,'Synthetic title','episodes','https://example.test/poster.jpg','https://example.test/wide.jpg',$2) RETURNING id`, kind, date).Scan(&root); err != nil {
			t.Fatal(err)
		}
		if kind == "series" {
			if _, err := f.Pool.Exec(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number,release_date,image_url,backdrop_url) VALUES('manual','episode','episode','Synthetic episode','episodes',$1,1,1,$2,'https://example.test/episode.jpg','https://example.test/episode-wide.jpg')`, root, date); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: root}); err != nil {
			t.Fatal(err)
		}
	}
	calendar, err := s.Calendar(ctx, f.Owner, now, date.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.Home(ctx, f.Owner, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, releases := range [][]library.Release{calendar, home.Upcoming} {
		if len(releases) != 2 {
			t.Fatalf("expected movie and episode, got %d", len(releases))
		}
		for _, r := range releases {
			raw, _ := json.Marshal(r)
			var fields map[string]any
			json.Unmarshal(raw, &fields)
			if fields["backdrop_url"] != "https://example.test/wide.jpg" || r.ImageURL != "https://example.test/poster.jpg" {
				t.Fatalf("root artwork lost for %s: %s", r.Kind, raw)
			}
		}
	}
}
