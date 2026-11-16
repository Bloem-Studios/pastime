package catalog

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUsabilityQueryRejectsUnsupportedOptionsBeforeIO(t *testing.T) {
	s := &Service{Providers: map[string]provider.Provider{"youtube": provider.YouTube{}}, Defaults: map[string]string{"video": "youtube"}}
	for _, opts := range []provider.QueryOptions{{Query: "synthetic", Year: 2020}, {Query: "synthetic", Page: 2}, {Query: "synthetic", Genre: "Drama"}, {Query: "synthetic", Sort: "rating"}, {Query: "synthetic", Page: -1}} {
		_, err := s.Query(context.Background(), "video", "", opts)
		if err == nil || fault.Public(err).Status != 422 {
			t.Fatalf("unsupported options accepted %+v: %v", opts, err)
		}
	}
	out, err := s.Query(context.Background(), "video", "", provider.QueryOptions{})
	if err != nil || out.Availability != "search_only" || out.Degraded || len(out.Items) != 0 {
		t.Fatalf("browse should explain search only: %+v %v", out, err)
	}
}

func TestUsabilityProviderChoiceReportsObservedFailureWithoutSecrets(t *testing.T) {
	c := provider.NewClient("synthetic")
	c.HTTP.Transport = inventoryFailureTransport{}
	var ignored any
	_ = c.JSON(context.Background(), provider.Request{URL: "https://api.themoviedb.org/3/search/movie"}, &ignored)
	s := &Service{Client: c, Providers: map[string]provider.Provider{"tmdb": provider.TMDB{Client: c, Key: "synthetic-never-disclosed"}}, Defaults: map[string]string{"movie": "tmdb"}}
	raw, _ := json.Marshal(s.Choices())
	if strings.Contains(string(raw), "synthetic-never-disclosed") || strings.Contains(string(raw), "TMDB_API_KEY") {
		t.Fatal("provider settings disclosed to ordinary caller")
	}
	var choices []map[string]any
	_ = json.Unmarshal(raw, &choices)
	for _, choice := range choices {
		if choice["name"] == "tmdb" && (choice["health"] != "temporarily_unavailable" || choice["availability"] != "temporarily_unavailable") {
			t.Fatalf("observed outage not exposed: %v", choice)
		}
	}
}

type inventoryFailureTransport struct{ status int }

func (tr inventoryFailureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	status := tr.status
	if status == 0 {
		status = 503
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
}

func TestUsabilityFilteredOutageDoesNotReturnUnfilteredFallback(t *testing.T) {
	s := &Service{Providers: map[string]provider.Provider{"synthetic": failingQuerier{}}, Defaults: map[string]string{"movie": "synthetic"}}
	out, err := s.Query(context.Background(), "movie", "", provider.QueryOptions{Query: "Synthetic", Page: 2, Year: 2020})
	if err != nil || out.Availability != "temporarily_unavailable" || !out.Degraded || len(out.Items) != 0 || out.NextPage != nil {
		t.Fatalf("filtered outage response %+v %v", out, err)
	}
	s.Providers["synthetic"] = failingQuerier{unconfigured: true}
	out, err = s.Query(context.Background(), "movie", "", provider.QueryOptions{Query: "Synthetic", Page: 2})
	if err != nil || out.Availability != "not_configured" {
		t.Fatalf("lost failure cause: %+v %v", out, err)
	}
}

type failingQuerier struct{ unconfigured bool }

func (failingQuerier) Name() string    { return "synthetic" }
func (failingQuerier) Kinds() []string { return []string{"movie"} }
func (failingQuerier) Search(context.Context, string, string) ([]provider.Record, error) {
	return nil, provider.ErrUnavailable
}
func (failingQuerier) Fetch(context.Context, string, string) (provider.Record, error) {
	return provider.Record{}, provider.ErrUnavailable
}
func (failingQuerier) QueryCapabilities() (provider.QueryCapabilities, provider.QueryCapabilities) {
	return provider.QueryCapabilities{Supported: true, Pagination: true, Year: true, Sorts: []string{}}, provider.QueryCapabilities{Sorts: []string{}}
}
func (f failingQuerier) Query(context.Context, string, provider.QueryOptions) (provider.RecordPage, error) {
	if f.unconfigured {
		return provider.RecordPage{}, provider.ErrNotConfigured
	}
	return provider.RecordPage{}, provider.ErrUnavailable
}

func TestUsabilityCapabilitiesAreIndependentOfConfiguration(t *testing.T) {
	s := &Service{}
	for _, c := range s.Choices() {
		if c.Name == "tmdb" {
			if c.Configured || c.Availability != "not_configured" || !c.Search.Supported || !c.Browse.Supported || !c.Browse.Year {
				t.Fatalf("implementation capability conflated with setup: %+v", c)
			}
		}
	}
	out, err := s.Query(context.Background(), "movie", "tmdb", provider.QueryOptions{Query: "Synthetic", Year: 2020, Page: 2})
	if err != nil || out.Availability != "not_configured" || len(out.Items) > 0 || !out.Degraded {
		t.Fatalf("filtered unconfigured request: %+v %v", out, err)
	}
}

func TestUsabilityMissingRecordDoesNotMarkProviderUnavailable(t *testing.T) {
	c := provider.NewClient("synthetic")
	c.HTTP.Transport = inventoryFailureTransport{status: 404}
	var ignored any
	_ = c.JSON(context.Background(), provider.Request{URL: "https://api.themoviedb.org/3/movie/999"}, &ignored)
	s := &Service{Client: c, Providers: map[string]provider.Provider{"tmdb": provider.TMDB{Client: c, Key: "synthetic"}}}
	for _, choice := range s.Choices() {
		if choice.Name == "tmdb" && choice.Availability != "available" {
			t.Fatalf("missing title is not an outage: %+v", choice)
		}
	}
}
