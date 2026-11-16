package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// routes answers requests by host+path with synthetic bodies; unmatched paths are 404.
type routes map[string]string

func (r routes) RoundTrip(req *http.Request) (*http.Response, error) {
	body, ok := r[req.URL.Host+req.URL.Path]
	status := 200
	if !ok {
		status, body = 404, `{}`
	}
	if strings.HasPrefix(body, "STATUS:") {
		status, body = 503, ""
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func fixture(r routes) *Client {
	c := NewClient("test")
	c.HTTP.Transport = r
	return c
}

func TestTMDBShowSeasonsAndEpisodes(t *testing.T) {
	c := fixture(routes{
		"api.themoviedb.org/3/tv/42": `{"id":42,"name":"Synthetic Coast","original_name":"Synthetic Coast","first_air_date":"2020-03-01","poster_path":"/p.jpg",
			"genres":[{"name":"Drama"}],"episode_run_time":[44],"external_ids":{"imdb_id":"tt0000042","tvdb_id":9},
			"seasons":[{"season_number":0,"name":"Specials","episode_count":2},{"season_number":1,"name":"Season 1","episode_count":8},{"season_number":2,"name":"Season 2","episode_count":6}]}`,
		"api.themoviedb.org/3/tv/42/season/1": `{"season_number":1,"name":"Season 1","episodes":[{"id":501,"name":"","episode_number":1,"season_number":1,"air_date":"2020-03-01","runtime":45},{"id":502,"name":"Second","episode_number":2,"season_number":1}]}`,
	})
	p := TMDB{Client: c, Key: "synthetic"}
	show, err := p.Fetch(context.Background(), "series", "42")
	if err != nil {
		t.Fatal(err)
	}
	if show.Title != "Synthetic Coast" || *show.Total != 14 || len(show.Children) != 3 || show.ExternalIDs["tvdb"] != "9" || *show.RuntimeMinutes != 44 || show.ImageURL != "https://image.tmdb.org/t/p/w500/p.jpg" {
		t.Fatalf("show: %+v", show)
	}
	season, err := p.FetchChild(context.Background(), show, show.Children[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(season.Children) != 2 || season.Children[0].Title != "Episode 1" || *season.Children[1].EpisodeNumber != 2 || season.Children[0].ProviderID != "501" {
		t.Fatalf("season: %+v", season)
	}
	if _, err = (TMDB{Client: c}).Fetch(context.Background(), "movie", "1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("missing key not reported", err)
	}
	if _, err = p.Fetch(context.Background(), "movie", "../etc"); !errors.Is(err, ErrNotFound) {
		t.Fatal("path traversal id accepted", err)
	}
	if _, err = Normalize(show); err != nil {
		t.Fatal(err)
	}
}

func TestKeylessAdapters(t *testing.T) {
	c := fixture(routes{
		"openlibrary.org/search.json":     `{"docs":[{"key":"/works/OL1W","title":"Synthetic Atlas","author_name":["A. Writer"],"first_publish_year":1999,"cover_i":77,"number_of_pages_median":320,"isbn":["123","9780000000001"]}]}`,
		"openlibrary.org/works/OL1W.json": `{"description":{"value":"A synthetic book."}}`,
		"graphql.anilist.co":              `{"data":{"Media":{"id":5,"idMal":55,"title":{"romaji":"Gousei","english":"Synthetic Star","native":"合成"},"startDate":{"year":2001,"month":4},"episodes":26,"duration":24,"averageScore":81,"description":"<b>Bold</b> text","genres":["Action"]}}}`,
		"itunes.apple.com/lookup":         `{"results":[{"wrapperType":"track","kind":"podcast","collectionId":9,"collectionName":"Synthetic Talk","artistName":"Host","trackCount":1,"feedUrl":"https://feed.example/rss"},{"wrapperType":"podcastEpisode","trackId":91,"trackName":"Pilot","trackTimeMillis":1800000,"releaseDate":"2024-01-02T00:00:00Z"}]}`,
		"musicbrainz.org/ws/2/release-group/00000000-0000-0000-0000-000000000001": `{"id":"00000000-0000-0000-0000-000000000001","title":"Synthetic Album","first-release-date":"1995-05","artist-credit":[{"name":"Band"}],"releases":[{"id":"00000000-0000-0000-0000-000000000002"}]}`,
		"musicbrainz.org/ws/2/release/00000000-0000-0000-0000-000000000002":       `{"media":[{"position":1,"track-count":2,"tracks":[{"position":1,"title":"Opening","length":241000,"recording":{"id":"00000000-0000-0000-0000-00000000000a"}},{"position":2,"title":"","recording":{"id":"00000000-0000-0000-0000-00000000000b"}}]},{"position":2,"track-count":1,"tracks":[{"position":1,"title":"Encore","recording":{"id":"00000000-0000-0000-0000-00000000000c"}}]}]}`,
	})
	ctx := context.Background()
	book, err := OpenLibrary{Client: c}.Fetch(ctx, "book", "OL1W")
	if err != nil || book.Overview != "A synthetic book." || *book.Total != 320 || book.ExternalIDs["isbn"] != "9780000000001" || book.ImageURL != "https://covers.openlibrary.org/b/id/77-L.jpg" {
		t.Fatalf("book: %+v %v", book, err)
	}
	anime, err := AniList{Client: c}.Fetch(ctx, "anime", "5")
	if err != nil || anime.Title != "Synthetic Star" || anime.Overview != "Bold text" || *anime.Total != 26 || *anime.Rating != 8.1 || anime.ExternalIDs["mal"] != "55" || anime.ReleaseDate.Format("2006-01-02") != "2001-04-01" {
		t.Fatalf("anime: %+v %v", anime, err)
	}
	show, err := ITunes{Client: c}.Fetch(ctx, "podcast", "9")
	if err != nil || len(show.Children) != 1 || *show.Children[0].RuntimeMinutes != 30 || show.ExternalIDs["feed"] != "https://feed.example/rss" {
		t.Fatalf("podcast: %+v %v", show, err)
	}
	album, err := MusicBrainz{Client: c}.Fetch(ctx, "music", "00000000-0000-0000-0000-000000000001")
	if err != nil || *album.Total != 3 || album.Credits[0] != "Band" || len(album.Children) != 3 || *album.Children[0].RuntimeMinutes != 5 || album.Children[1].Title != "Track 2" || *album.Children[2].SeasonNumber != 2 {
		t.Fatalf("album: %+v %v", album, err)
	}
	if _, err = (MusicBrainz{Client: c}).Fetch(ctx, "music", "not-a-mbid"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestKeyedAdapters(t *testing.T) {
	c := fixture(routes{
		"id.twitch.tv/oauth2/token":                 `{"access_token":"synthetic","expires_in":3600}`,
		"api.igdb.com/v4/games":                     `[{"id":3,"name":"Synthetic Quest","first_release_date":946684800,"cover":{"image_id":"abc"},"total_rating":88,"involved_companies":[{"developer":true,"company":{"name":"Studio"}}]}]`,
		"comicvine.gamespot.com/api/volume/4050-8/": `{"status_code":1,"results":{"id":8,"name":"Synthetic Comics","start_year":"1990","count_of_issues":2,"issues":[{"id":81,"name":"","issue_number":"1"}]}}`,
		"comicvine.gamespot.com/api/volume/4050-9/": `{"status_code":101,"results":[]}`,
		"boardgamegeek.com/xmlapi2/thing": `<items><item id="13"><name type="primary" value="Synthetic Board"/><yearpublished value="2010"/><playingtime value="60"/>
			<link type="boardgamedesigner" value="Designer"/><statistics><ratings><average value="7.5"/></ratings></statistics></item></items>`,
	})
	ctx := context.Background()
	game, err := NewIGDB(c, "id", "secret").Fetch(ctx, "game", "3")
	if err != nil || game.Title != "Synthetic Quest" || game.ReleaseDate.Year() != 2000 || *game.Rating != 8.8 || game.Credits[0] != "Studio" {
		t.Fatalf("game: %+v %v", game, err)
	}
	comic, err := ComicVine{Client: c, Key: "k"}.Fetch(ctx, "comic", "8")
	if err != nil || len(comic.Children) != 1 || comic.Children[0].Title != "#1" {
		t.Fatalf("comic: %+v %v", comic, err)
	}
	if _, err = (ComicVine{Client: c, Key: "k"}).Fetch(ctx, "comic", "9"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	board, err := BGG{Client: c, Token: "t"}.Fetch(ctx, "boardgame", "13")
	if err != nil || board.Title != "Synthetic Board" || *board.RuntimeMinutes != 60 || board.Credits[0] != "Designer" {
		t.Fatalf("board: %+v %v", board, err)
	}
	if escapeApicalypse(`a"; fields *; b`) != `a\"; fields *; b` {
		t.Fatal("search text escapes its string literal")
	}
}

func TestClientBoundsAndFailures(t *testing.T) {
	big := strings.Repeat("x", maxBody+10)
	c := fixture(routes{"a.test/big": big, "a.test/down": "STATUS:503"})
	if _, err := c.Do(context.Background(), Request{URL: "https://a.test/big"}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatal("oversized body accepted", err)
	}
	if _, err := c.Do(context.Background(), Request{URL: "https://a.test/down", Retries: 1}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	r, err := Normalize(Record{Title: " T\x00itle ", ProviderID: "1", Kind: "book", ImageURL: "http://insecure.example/x.jpg", Genres: []string{"a", "a", " "}})
	if err != nil || r.Title != "Title" || r.ImageURL != "" || len(r.Genres) != 1 || r.Unit != "pages" {
		t.Fatalf("normalize: %+v %v", r, err)
	}
}

func TestMusicBrainzArtist(t *testing.T) {
	c := fixture(routes{
		"musicbrainz.org/ws/2/artist/00000000-0000-0000-0000-0000000000a1": `{"name":"Band","type":"Group","country":"GB","life-span":{"begin":"1985"}}`,
		"musicbrainz.org/ws/2/release-group": `{"release-groups":[{"id":"00000000-0000-0000-0000-000000000001","title":"First","first-release-date":"1993","primary-type":"Album"},
			{"id":"00000000-0000-0000-0000-000000000002","title":"Second","first-release-date":"1997","primary-type":"Album"}]}`,
	})
	p, err := MusicBrainz{Client: c}.Person(context.Background(), "00000000-0000-0000-0000-0000000000a1")
	if err != nil || p.Name != "Band" || p.KnownFor != "Band" || len(p.Credits) != 2 || p.Credits[0].Record.Title != "Second" || p.Birthday == nil {
		t.Fatalf("artist %+v %v", p, err)
	}
}

func TestTMDBReleaseDatesKeepEarliestDatePerTypeAndCountry(t *testing.T) {
	var v tmdbReleaseDates
	body := `{"results":[{"iso_3166_1":"NL","release_dates":[{"release_date":"2030-12-05T00:00:00.000Z","type":4},{"release_date":"2030-12-01T00:00:00.000Z","type":4},{"release_date":"2030-11-01T00:00:00.000Z","type":3},{"release_date":"2030-10-01T00:00:00.000Z","type":1}]},{"iso_3166_1":"XYZ","release_dates":[{"release_date":"2030-01-01","type":3}]},{"iso_3166_1":"US","release_dates":[{"release_date":"bad","type":5}]}]}`
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	got := releaseDates(&v)
	nl, ok := got["NL"].(map[string]any)
	if !ok || len(got) != 1 || nl["digital"] != "2030-12-01" || nl["theatrical"] != "2030-11-01" || len(nl) != 2 {
		t.Fatal(got)
	}
}
