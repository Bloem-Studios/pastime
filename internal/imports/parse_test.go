package imports

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestParseTrakt(t *testing.T) {
	data := zipOf(t, map[string]string{
		"export/watched-history-1.json": `[
			{"id":11,"watched_at":"2026-01-02T20:00:00.000Z","type":"movie","movie":{"title":"Synthetic Film","year":2001,"ids":{"trakt":1,"imdb":"tt42","tmdb":42}}},
			{"id":12,"watched_at":"2026-01-05T20:00:00.000Z","type":"movie","movie":{"title":"Synthetic Film","year":2001,"ids":{"trakt":1,"imdb":"tt42","tmdb":42}}},
			{"id":13,"watched_at":"2026-02-01T21:00:00.000Z","type":"episode","show":{"title":"Synthetic Harbour","year":2020,"ids":{"trakt":7,"tmdb":100,"tvdb":700}},"episode":{"season":1,"number":2}}]`,
		"export/ratings-movies.json":    `[{"rated_at":"2026-01-06T00:00:00.000Z","rating":8,"type":"movie","movie":{"title":"Synthetic Film","year":2001,"ids":{"trakt":1,"imdb":"tt42","tmdb":42}}}]`,
		"export/lists-watchlist-1.json": `[{"listed_at":"2026-01-01T00:00:00.000Z","type":"show","show":{"title":"Queued Show","year":2022,"ids":{"trakt":9,"tmdb":900}}}]`,
		"export/user-profile.json":      `{}`,
	})
	recs, err := ParseTrakt(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3 (movie, show, watchlist show)", len(recs))
	}
	var movie, show, queued *Record
	for i := range recs {
		switch recs[i].Label {
		case "Synthetic Film (2001)":
			movie = &recs[i]
		case "Synthetic Harbour":
			show = &recs[i]
		case "Queued Show":
			queued = &recs[i]
		}
	}
	if movie == nil || len(movie.Plays) != 2 || *movie.Rating != 8 || movie.Video.TMDB != "42" || movie.Plays[0].ExternalID != "history:11" {
		t.Fatalf("movie: %+v", movie)
	}
	if show == nil || len(show.Plays) != 1 || *show.Plays[0].Season != 1 || *show.Plays[0].Episode != 2 || show.Video.ShowTVDB != "700" {
		t.Fatalf("show: %+v", show)
	}
	if queued == nil || queued.Status != "planned" || len(queued.Plays) != 0 {
		t.Fatalf("watchlist: %+v", queued)
	}
	if _, err = ParseTrakt([]byte("not a zip")); !errors.Is(err, ErrFormat) {
		t.Fatal("non-zip accepted", err)
	}
	if _, err = ParseTrakt(zipOf(t, map[string]string{"readme.txt": "x"})); !errors.Is(err, ErrFormat) {
		t.Fatal("archive without Trakt files accepted", err)
	}
}

func TestParseLetterboxd(t *testing.T) {
	data := zipOf(t, map[string]string{
		"diary.csv":     "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Tags,Watched Date\n2026-01-03,Synthetic Film,2001,https://boxd.it/a,4.5,,,2026-01-02\n2026-03-01,Synthetic Film,2001,https://boxd.it/b,4.5,Yes,,2026-02-28\n",
		"ratings.csv":   "Date,Name,Year,Letterboxd URI,Rating\n2026-01-03,Synthetic Film,2001,https://boxd.it/x,4.5\n",
		"watched.csv":   "Date,Name,Year,Letterboxd URI\n2025-01-01,Undated Classic,1950,https://boxd.it/c\n",
		"watchlist.csv": "Date,Name,Year,Letterboxd URI\n2025-01-01,Someday Film,2030,https://boxd.it/d\n",
	})
	recs, err := ParseLetterboxd(data)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Record{}
	for _, r := range recs {
		by[r.Label] = r
	}
	if f := by["Synthetic Film (2001)"]; len(f.Plays) != 2 || *f.Rating != 9 || f.Status != "completed" {
		t.Fatalf("diary film: %+v", f)
	}
	if u := by["Undated Classic (1950)"]; u.Status != "completed" || len(u.Plays) != 0 {
		t.Fatalf("watched without date: %+v", u)
	}
	if w := by["Someday Film (2030)"]; w.Status != "planned" {
		t.Fatalf("watchlist: %+v", w)
	}
	if _, err = ParseLetterboxd([]byte("Title,Author\nx,y\n")); !errors.Is(err, ErrFormat) {
		t.Fatal("foreign CSV accepted", err)
	}
}

func TestParseReadingAndIMDb(t *testing.T) {
	imdb, err := ParseIMDb([]byte("Const,Your Rating,Date Rated,Title,Title Type,Year\ntt42,8,2026-01-01,Synthetic Film,Movie,2001\ntt77,9,2026-01-01,Synthetic Harbour,TV Series,2020\ntt99,7,2026-01-01,Pilot,TV Episode,2020\n"))
	if err != nil || len(imdb) != 2 || imdb[0].Video.IMDB != "tt42" || imdb[1].Video.ShowIMDB != "tt77" || imdb[0].Status != "completed" {
		t.Fatalf("imdb: %+v %v", imdb, err)
	}
	gr, err := ParseGoodreads([]byte("Book Id,Title,Author,ISBN,ISBN13,My Rating,Exclusive Shelf,Date Read,Private Notes\n1,Synthetic Atlas,A. Writer,\"=\"\"0000000000\"\"\",\"=\"\"9780000000001\"\"\",4,read,2026/01/15,kept\n2,Next Book,B. Writer,,,0,to-read,,\n3,Hard Book,C. Writer,,,0,did-not-finish,,\n"))
	if err != nil || len(gr) != 3 || gr[0].Book.ISBN != "9780000000001" || *gr[0].Rating != 8 || len(gr[0].Plays) != 1 || gr[1].Status != "planned" || gr[2].Status != "dropped" || gr[0].Notes != "kept" {
		t.Fatalf("goodreads: %+v %v", gr, err)
	}
	sg, err := ParseStoryGraph([]byte("Title,Authors,ISBN/UID,Format,Read Status,Dates Read,Star Rating,Review\nSynthetic Atlas,\"A. Writer, B. Editor\",9780000000001,audio,read,\"2025/12/01-2026/01/02, 2026/03/04\",4.25,\n"))
	if err != nil || len(sg) != 1 || sg[0].Kind != "audiobook" || sg[0].Book.Author != "A. Writer" || len(sg[0].Plays) != 2 || *sg[0].Rating != 9 {
		t.Fatalf("storygraph: %+v %v", sg, err)
	}
	if _, err = ParseGoodreads([]byte("Const,Title\nx,y\n")); !errors.Is(err, ErrFormat) {
		t.Fatal("IMDb file accepted as Goodreads", err)
	}
}

func TestParseNative(t *testing.T) {
	recs, err := ParseNative([]byte(`{"type":"entry","provider":"tmdb","provider_id":"100","kind":"series","title":"Synthetic Harbour","status":"in_progress","progress":1,"plays":[{"at":"2026-01-02T20:00:00Z","season":1,"episode":1,"id":"5"}]}
`))
	if err != nil || len(recs) != 1 || recs[0].Direct.ID != "100" || recs[0].Plays[0].ExternalID != "native:5" {
		t.Fatalf("native: %+v %v", recs, err)
	}
	if _, err = ParseNative([]byte("not json")); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
}

func TestArchiveRejectsAmbiguousBasenames(t *testing.T) {
	_, err := readZip(zipOf(t, map[string]string{"one/ratings.csv": "first", "two/ratings.csv": "second"}))
	if !errors.Is(err, ErrFormat) {
		t.Fatal("archive silently overwrote a file", err)
	}
}

func TestLegacyNativeManualEntriesAreNotSilentlyDiscarded(t *testing.T) {
	if _, err := ParseNative([]byte(`{"type":"entry","kind":"book","title":"Manual Notes","status":"planned","progress":0}`)); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
}
