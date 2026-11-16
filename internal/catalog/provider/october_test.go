package provider

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestMangaBakaSearchFetchAndMerge(t *testing.T) {
	series := `{"id":%d,"state":"%s","merged_with":%s,"title":"Synthetic Manga","native_title":"合成","description":"About.","year":2020,"total_chapters":"147","status":"completed",
		"genres":["fantasy"],"authors":["A. Writer"],"artists":["A. Artist"],"type":"manga","cover":{"raw":{"url":"https://images.example/raw"},"x350":{"x2":"https://cdn.example/x350"}},
		"published":{"start_date":"2020-04-28"},"source":{"anilist":{"id":118586},"my_anime_list":{"id":126287},"anime_planet":{"id":"slug"}}}`
	c := fixture(routes{
		"api.mangabaka.org/v1/series/search": `{"status":200,"data":[` + fmtSeries(series, 7, "active", "null") + `,` + fmtSeries(series, 8, "merged", "7") + `]}`,
		"api.mangabaka.org/v1/series/8":      `{"status":200,"data":` + fmtSeries(series, 8, "merged", "7") + `}`,
		"api.mangabaka.org/v1/series/7":      `{"status":200,"data":` + fmtSeries(series, 7, "active", "null") + `}`,
	})
	p := MangaBaka{Client: c}
	found, err := p.Search(context.Background(), "manga", "synthetic")
	if err != nil || len(found) != 1 || found[0].ProviderID != "7" {
		t.Fatal(found, err)
	}
	r, err := p.Fetch(context.Background(), "manga", "8")
	if err != nil || r.ProviderID != "7" {
		t.Fatal("merged series not followed", r, err)
	}
	if r.Title != "Synthetic Manga" || r.OriginalTitle != "合成" || *r.Total != 147 || r.ReleaseStatus != "finished" || r.ImageURL != "https://cdn.example/x350" ||
		r.ExternalIDs["anilist"] != "118586" || r.ExternalIDs["mal"] != "126287" || len(r.Credits) != 2 || r.ReleaseDate.Year() != 2020 {
		t.Fatalf("%+v", r)
	}
	if _, err = p.Search(context.Background(), "comic", "x"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func fmtSeries(template string, id int, state, merged string) string {
	s := strings.Replace(template, "%d", strconv.Itoa(id), 1)
	s = strings.Replace(s, "%s", state, 1)
	return strings.Replace(s, "%s", merged, 1)
}

func TestGCDSearchAndFetchWithOptionalCredentials(t *testing.T) {
	var auth string
	rt := routes{
		"www.comics.org/api/series/name/watchmen/": `{"results":[{"api_url":"https://www.comics.org/api/series/21965/?format=json","name":"Absolute Watchmen","year_began":2005,"year_ended":2005,"active_issues":["https://www.comics.org/api/issue/338481/?format=json"]},{"api_url":"https://example.com/bad","name":"Bad"}]}`,
		"www.comics.org/api/series/21965/":         `{"api_url":"https://www.comics.org/api/series/21965/?format=json","name":"Absolute Watchmen","year_began":2005,"year_ended":2005,"country":"us","language":"en","active_issues":["https://www.comics.org/api/issue/338481/?format=json","https://www.comics.org/api/issue/2/?format=json"]}`,
		"www.comics.org/api/issue/338481/":         `{"cover":"https://files1.comics.org/cover.jpg","indicia_publisher":"DC Comics"}`,
	}
	c := fixture(routes{})
	c.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		auth = r.Header.Get("Authorization")
		return rt.RoundTrip(r)
	})
	p := GCD{Client: c}
	found, err := p.Search(context.Background(), "comic", "watchmen")
	if err != nil || len(found) != 1 || found[0].ProviderID != "21965" || auth != "" {
		t.Fatal(found, err, auth)
	}
	p.Username, p.Password = "synthetic", "secret"
	r, err := p.Fetch(context.Background(), "comic", "21965")
	if err != nil || *r.Total != 2 || r.ImageURL != "https://files1.comics.org/cover.jpg" || r.Credits[0] != "DC Comics" || r.ReleaseStatus != "ended" || !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("%+v %v %q", r, err, auth)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTVDBLogsInOnceAndBuildsSeasons(t *testing.T) {
	logins := 0
	rt := routes{
		"api4.thetvdb.com/v4/login":  `{"status":"success","data":{"token":"jwt"}}`,
		"api4.thetvdb.com/v4/search": `{"status":"success","data":[{"tvdb_id":"81189","name":"Synthetic Bad","translations":{"eng":"Synthetic Good"},"image_url":"https://artworks.example/p.jpg","first_air_time":"2008-01-20","status":"Ended"},{"tvdb_id":"x","name":"Bad"}]}`,
		"api4.thetvdb.com/v4/series/81189/extended": `{"status":"success","data":{"id":81189,"name":"Synthetic Good","overview":"About.","image":"https://artworks.example/p.jpg","firstAired":"2008-01-20","averageRuntime":47,
			"status":{"name":"Ended"},"genres":[{"name":"Drama"}],"remoteIds":[{"id":"tt0903747","sourceName":"IMDB"}],
			"seasons":[{"id":1,"number":1,"type":{"type":"official"}},{"id":2,"number":1,"type":{"type":"dvd"}},{"id":3,"number":0,"type":{"type":"official"}}],
			"episodes":[{"id":11,"seasonNumber":1,"number":2,"name":"Second","aired":"2008-01-27"},{"id":10,"seasonNumber":1,"number":1,"name":"Pilot","aired":"2008-01-20","runtime":58},{"id":9,"seasonNumber":0,"number":1,"name":null}]}}`,
	}
	c := fixture(routes{})
	c.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v4/login" {
			logins++
		} else if r.Header.Get("Authorization") != "Bearer jwt" {
			t.Errorf("missing token on %s", r.URL.Path)
		}
		return rt.RoundTrip(r)
	})
	p := NewTVDB(c, "synthetic", "")
	found, err := p.Search(context.Background(), "series", "synthetic")
	if err != nil || len(found) != 1 || found[0].Title != "Synthetic Good" || found[0].OriginalTitle != "Synthetic Bad" || found[0].ExternalIDs["tvdb"] != "81189" {
		t.Fatal(found, err)
	}
	show, err := p.Fetch(context.Background(), "series", "81189")
	if err != nil || logins != 1 {
		t.Fatal(logins, err)
	}
	if *show.Total != 2 || len(show.Children) != 2 || show.ExternalIDs["imdb"] != "tt0903747" || show.ReleaseStatus != "Ended" || *show.RuntimeMinutes != 47 {
		t.Fatalf("%+v", show)
	}
	specials, first := show.Children[0], show.Children[1]
	if specials.Title != "Specials" || len(specials.Children) != 1 || specials.Children[0].Title != "Episode 1" {
		t.Fatalf("specials %+v", specials)
	}
	if len(first.Children) != 2 || first.Children[0].Title != "Pilot" || first.Children[0].ProviderID != "10" || !first.ChildrenLoaded {
		t.Fatalf("season %+v", first)
	}
	if _, err = (TVDB{Client: c}).Search(context.Background(), "series", "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("missing key not reported", err)
	}
}

func TestYouTubeResolvesLinksThroughOEmbed(t *testing.T) {
	for raw, want := range map[string]string{
		"dQw4w9WgXcQ": "dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42": "dQw4w9WgXcQ", "https://youtu.be/dQw4w9WgXcQ?si=x": "dQw4w9WgXcQ",
		"https://m.youtube.com/shorts/dQw4w9WgXcQ": "dQw4w9WgXcQ", "https://www.youtube.com/embed/dQw4w9WgXcQ": "dQw4w9WgXcQ",
		"https://evil.example/watch?v=dQw4w9WgXcQ": "", "never gonna": "", "javascript:alert(1)": "",
	} {
		if got := YouTubeVideoID(raw); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
	c := fixture(routes{"www.youtube.com/oembed": `{"title":"Synthetic Video","author_name":"Synthetic Channel","author_url":"https://www.youtube.com/@synthetic","thumbnail_url":"https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg"}`})
	p := YouTube{Client: c}
	found, err := p.Search(context.Background(), "video", "https://youtu.be/dQw4w9WgXcQ")
	if err != nil || len(found) != 1 {
		t.Fatal(found, err)
	}
	v := found[0]
	if v.ProviderID != "dQw4w9WgXcQ" || v.Title != "Synthetic Video" || v.Credits[0] != "Synthetic Channel" || v.ImageURL == "" || v.Details["url"] != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("%+v", v)
	}
	if none, err := p.Search(context.Background(), "video", "a keyword search"); err != nil || len(none) != 0 {
		t.Fatal("keyword search should find nothing without a key", none, err)
	}
	if _, err = p.Fetch(context.Background(), "video", "../x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
