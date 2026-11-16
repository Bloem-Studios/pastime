package imports

import (
	"errors"
	"testing"
)

func TestParseYamtrack(t *testing.T) {
	data := []byte(`"row_type","media_id","source","media_type","title","season_number","episode_number","score","progress","status","start_date","end_date","notes","name"
"media","42","tmdb","movie","Synthetic Film","","","8.5","","Completed","","2026-01-02 20:00:00+00:00","liked it",""
"media","100","tmdb","tv","Synthetic Harbour","","","","","In progress","","","",""
"media","100","tmdb","season","Synthetic Harbour","1","","","","In progress","","","",""
"media","100","tmdb","episode","Synthetic Harbour","1","2","","","","","2026-02-01 21:00:00+00:00","",""
"media","321","mal","anime","Synthetic Anime","","","7","12","Paused","","","",""
"media","620","igdb","game","Synthetic Portal","","","","12:30","Completed","","","",""
"media","","manual","book","Handwritten","","","","","Planning","","","",""
"list","","","","","","","","","","","","","Weekend"
`)
	recs, err := ParseYamtrack(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 {
		t.Fatalf("records %d: %+v", len(recs), recs)
	}
	film, show, anime, game, book := recs[0], recs[1], recs[2], recs[3], recs[4]
	if film.Direct.ID != "42" || *film.Rating != 9 || film.Status != "completed" || len(film.Plays) != 1 || film.Notes != "liked it" {
		t.Fatalf("film %+v", film)
	}
	if show.Direct.Kind != "series" || show.Status != "in_progress" || len(show.Plays) != 1 || *show.Plays[0].Episode != 2 {
		t.Fatalf("show %+v", show)
	}
	if anime.External.Scheme != "mal" || *anime.Progress != 12 || anime.Status != "paused" {
		t.Fatalf("anime %+v", anime)
	}
	if *game.Progress != 750 || game.Direct.Provider != "igdb" {
		t.Fatalf("game %+v", game)
	}
	if book.Direct != nil || book.Title.Title != "Handwritten" || book.Status != "planned" {
		t.Fatalf("book %+v", book)
	}
	if _, err := ParseYamtrack([]byte("Title,Year\nX,2000\n")); !errors.Is(err, ErrFormat) {
		t.Fatal("wrong file accepted")
	}
}

func TestParseGrouvee(t *testing.T) {
	recs, err := ParseGrouvee(zipOf(t, map[string]string{"export/grouvee.json": `{"collection":[
		{"name":"Synthetic Quest","igdb_id":77,"shelves":{"Played":{},"Backlog":{},"Steam":{}},"rating":4,"review":"Great.",
		 "dates":[{"date_started":"2025-01-01","date_finished":"2025-02-01","seconds_played":7200,"notes":"first run"}]},
		{"name":"Wished Game","shelves":{"Wish List":{}}}]}`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Status != "completed" || *recs[0].Rating != 8 || *recs[0].Progress != 120 || len(recs[0].Plays) != 1 || recs[0].Notes != "first run\n\nGreat." {
		t.Fatalf("grouvee %+v", recs)
	}
	if recs[1].Direct != nil || recs[1].Status != "planned" {
		t.Fatalf("wish %+v", recs[1])
	}
}

func TestParseTVTime(t *testing.T) {
	recs, err := ParseTVTime(zipOf(t, map[string]string{
		"tracking-prod-records.csv":    "series_name,season_number,episode_number,created_at\nSynthetic Harbour,1,1,2026-02-01 21:00:00\nSynthetic Harbour,1,2,2026-02-02 21:00:00\n",
		"tracking-prod-records-v2.csv": "movie_name,type,updated_at\nSynthetic Film,watch,2026-01-02 20:00:00\nOther Film,follow,2026-01-03 20:00:00\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var plays int
	for _, r := range recs {
		plays += len(r.Plays)
	}
	if len(recs) != 2 || plays != 3 {
		t.Fatalf("tvtime %+v", recs)
	}
}

func TestParseGenericCSV(t *testing.T) {
	recs, err := ParseGenericCSV([]byte("Name,Type,Year,Status,Stars,Date,IMDb ID,Season,Episode\n" +
		"Synthetic Film,Movie,2001,watched,4.5,2026-01-02,tt42,,\n" +
		"Synthetic Harbour,TV,2020,watching,,2026-02-01,,1,3\n" +
		"Synthetic Board,Board Game,2019,,,,,,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || recs[0].Video.IMDB != "tt42" || *recs[0].Rating != 9 || recs[0].Status != "completed" {
		t.Fatalf("film %+v", recs)
	}
	if recs[1].Kind != "series" || *recs[1].Plays[0].Episode != 3 || recs[2].Title.Kind != "boardgame" {
		t.Fatalf("csv %+v %+v", recs[1], recs[2])
	}
	if _, err := ParseGenericCSV([]byte("foo,bar\n1,2\n")); !errors.Is(err, ErrFormat) {
		t.Fatal("columnless CSV accepted")
	}
}

func TestParseCLZ(t *testing.T) {
	recs, err := ParseCLZ([]byte("\ufeffIndex,Title,Release Year,IMDb Url,Format,Purchase Price,Purchase Date,Quantity,Collection Status,Case Type,Region\n" +
		"1,Synthetic Film,2001,https://www.imdb.com/title/tt42000/,Blu-ray,\"$19.99\",2024-05-01,2,In Collection,Steelbook,B\n" +
		"2,Wanted Film,2030,,4K Ultra HD,,,1,Wish List,,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("records %+v", recs)
	}
	film := recs[0]
	if film.Video.IMDB != "tt42000" || len(film.Copies) != 2 || film.Copies[0].Format != "bluray" || *film.Copies[0].PriceCents != 1999 || film.Copies[0].Currency != "USD" ||
		*film.Copies[0].AcquiredOn != "2024-05-01" || film.Copies[0].Custom["Case Type"] != "Steelbook" || film.Copies[1].ExternalID != "clz:1:1" {
		t.Fatalf("film %+v %+v", film, film.Copies)
	}
	if len(recs[1].Copies) != 0 || recs[1].Status != "planned" {
		t.Fatalf("wish list %+v", recs[1])
	}
	books, _ := ParseCLZ([]byte("Title,Author,ISBN,Format,Read\nSynthetic Atlas,A. Writer,9780000000001,Hardcover,Yes\n"))
	if books[0].Kind != "book" || books[0].Book.ISBN != "9780000000001" || books[0].Copies[0].Format != "hardcover" || books[0].Status != "completed" {
		t.Fatalf("book %+v", books[0])
	}
	if p, c := clzPrice("19,99 €"); *p != 1999 || c != "EUR" {
		t.Fatal("euro price", *p, c)
	}
	if p, _ := clzPrice("1,299.00"); *p != 129900 {
		t.Fatal("thousands", *p)
	}
}
