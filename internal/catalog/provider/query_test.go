package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type queryTransport func(*http.Request) (*http.Response, error)

func (f queryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func queryResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestUsabilityTMDBPagesAndAppliesBrowseFilters(t *testing.T) {
	c := NewClient("synthetic")
	c.HTTP.Transport = queryTransport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.URL.Path != "/3/discover/tv" || q.Get("page") != "2" || q.Get("first_air_date_year") != "2020" || q.Get("with_genres") != "18" || q.Get("sort_by") != "vote_average.desc" || q.Get("include_adult") != "false" {
			t.Errorf("wrong request %s", r.URL)
		}
		return queryResponse(r, `{"page":2,"total_pages":3,"results":[{"id":7,"name":"Synthetic series"}]}`), nil
	})
	q, ok := any(TMDB{Client: c, Key: "synthetic"}).(Querier)
	if !ok {
		t.Fatal("TMDB has no paged query adapter")
	}
	page, err := q.Query(context.Background(), "series", QueryOptions{Page: 2, Year: 2020, Genre: "18", Sort: "rating"})
	if err != nil || page.NextPage != 3 || len(page.Items) != 1 || page.Items[0].Kind != "series" {
		t.Fatalf("page %+v: %v", page, err)
	}
}

func TestUsabilityAniListBrowseAndGraphQLErrors(t *testing.T) {
	c := NewClient("synthetic")
	fail := false
	c.HTTP.Transport = queryTransport(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Query     string
			Variables map[string]any
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Variables["page"] != float64(2) || body.Variables["yearString"] != "2020%" || body.Variables["genre"] != "Drama" || !strings.Contains(body.Query, "pageInfo") {
			t.Errorf("missing filters: %+v", body)
		}
		if fail {
			return queryResponse(r, `{"errors":[{"message":"synthetic outage"}]}`), nil
		}
		return queryResponse(r, `{"data":{"Page":{"pageInfo":{"hasNextPage":true},"media":[{"id":7,"title":{"english":"Synthetic anime"}}]}}}`), nil
	})
	q, ok := any(AniList{Client: c}).(Querier)
	if !ok {
		t.Fatal("AniList has no browse adapter")
	}
	page, err := q.Query(context.Background(), "anime", QueryOptions{Page: 2, Year: 2020, Genre: "Drama", Sort: "popular"})
	if err != nil || page.NextPage != 3 || len(page.Items) != 1 {
		t.Fatalf("page %+v: %v", page, err)
	}
	fail = true
	if _, err = q.Query(context.Background(), "anime", QueryOptions{Page: 2, Year: 2020, Genre: "Drama"}); err == nil {
		t.Fatal("GraphQL outage treated as empty success")
	}
}

func TestUsabilityOpenLibrarySearchYearAndFinalPage(t *testing.T) {
	c := NewClient("synthetic")
	c.HTTP.Transport = queryTransport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if q.Get("page") != "2" || q.Get("limit") != "20" || !strings.Contains(q.Get("q"), "first_publish_year:2020") {
			t.Errorf("wrong filters %s", r.URL)
		}
		return queryResponse(r, `{"start":20,"numFound":21,"docs":[{"key":"/works/OL7W","title":"Synthetic book"}]}`), nil
	})
	q, ok := any(OpenLibrary{Client: c}).(Querier)
	if !ok {
		t.Fatal("OpenLibrary has no paged query adapter")
	}
	page, err := q.Query(context.Background(), "book", QueryOptions{Query: "Atlas", Page: 2, Year: 2020})
	if err != nil || page.NextPage != 0 || len(page.Items) != 1 {
		t.Fatalf("page %+v: %v", page, err)
	}
}

func TestUsabilityAniListFetchCarriesRelatedTitles(t *testing.T){
 c:=fixture(routes{"graphql.anilist.co":`{"data":{"Media":{"id":1,"title":{"english":"Synthetic seed"},"recommendations":{"nodes":[{"mediaRecommendation":{"id":2,"type":"ANIME","title":{"english":"Synthetic related"},"coverImage":{"large":"https://images.example/2.jpg"}}}]}}}}`})
 r,err:=(AniList{Client:c}).Fetch(context.Background(),"anime","1")
 if err!=nil{t.Fatal(err)}
 raw,_:=json.Marshal(r.Details["related"])
 if !strings.Contains(string(raw),"Synthetic related") || !strings.Contains(string(raw),`"kind":"anime"`){t.Fatalf("no usable related titles: %s",raw)}
}
